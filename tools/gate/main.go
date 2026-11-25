// Command gate is the Go that Taskfile.yml calls in place of shell: the steps a tier runs that had
// pipes, redirections, loops and scratch folders. It is run from the repository's top as
//
//	go -C tools/gate run . -top <the repository's top> <step> [arguments]
//
// and the steps are fmt, lint, plants, contract, size, budget, now, load and secrets-guard. A step's exit is 0 when it
// holds, 1 when it does not, and 2 for arguments it does not take. It is a module of its own, as the tools are,
// so that the library's module keeps to what it requires and the size count leaves it out.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

func nowUnix() int64 { return time.Now().Unix() }

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// steps are the steps by name. Each is run from the repository's top, which the task's own folder is.
var steps = map[string]func(context.Context, []string, io.Writer, io.Writer) error{
	"fmt":           fmtStep,
	"lint":          lintStep,
	"plants":        plantsStep,
	"contract":      contractStep,
	"size":          sizeStep,
	"budget":        budgetStep,
	"cpu-tier":      cpuStep,
	"now":           nowStep,
	"load":          loadStep,
	"secrets-guard": secretsGuard,
}

// errRed is a step that did not hold, whose reason it has already said.
var errRed = errors.New("gate: red")

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) >= 2 && args[0] == "-top" {
		if err := os.Chdir(args[1]); err != nil {
			fmt.Fprintf(stderr, "gate: %v\n", err)
			return 1
		}
		args = args[2:]
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "gate: a step is the first argument")
		return 2
	}
	step, ok := steps[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "gate: no step %q\n", args[0])
		return 2
	}
	if err := step(ctx, args[1:], stdout, stderr); err != nil {
		if !errors.Is(err, errRed) {
			fmt.Fprintf(stderr, "gate: %s: %v\n", args[0], err)
		}
		return 1
	}
	return 0
}

// cmd is name with args, run in dir (the top when empty) with env over the task's own.
func cmd(ctx context.Context, dir string, env []string, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...) //nolint:gosec // the tier's own tools and git, with the gate's arguments
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	return c
}

// tool is the path of a tool built from tools/<name>.mod by go tool, by its own name.
func tool(ctx context.Context, name string) (string, error) {
	out, err := cmd(ctx, "", nil, "go", "tool", "-modfile=tools/"+name+".mod", "-n", name).Output()
	if err != nil {
		return "", fmt.Errorf("go tool for %s: %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// fmtStep fails on a file gofmt would change.
func fmtStep(ctx context.Context, _ []string, stdout, stderr io.Writer) error {
	out, err := cmd(ctx, "", nil, "gofmt", "-l", ".").Output()
	if err != nil {
		return fmt.Errorf("gofmt: %w", err)
	}
	if len(bytes.TrimSpace(out)) != 0 {
		fmt.Fprintf(stdout, "gofmt would change:\n%s", out)
		return errRed
	}
	return nil
}

// lintStep is golangci-lint for each of the three systems, run together, each one's output as it ends.
func lintStep(ctx context.Context, _ []string, stdout, stderr io.Writer) error {
	bin, err := tool(ctx, "golangci-lint")
	if err != nil {
		return err
	}
	systems := []string{"linux", "darwin", "windows"}
	bad := make([]bool, len(systems))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, goos := range systems {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := cmd(ctx, "", []string{"GOOS=" + goos}, bin, "run", "--allow-parallel-runners", "./...").CombinedOutput()
			mu.Lock()
			defer mu.Unlock()
			if len(out) != 0 {
				fmt.Fprintf(stderr, "lint: %s\n%s", goos, out)
			}
			bad[i] = err != nil
		}()
	}
	wg.Wait()
	for _, b := range bad {
		if b {
			return errRed
		}
	}
	return nil
}

// plant is one file of the layer plants: its package clause, its import, and the list the report
// must name for it. lib stands for any other package of the module, and no plant imports one that would make a cycle.
type plant struct{ file, pkg, imp, list string }

func plants(mod string) []plant {
	return []plant{
		{"exit/p.go", "exit", mod + "/lib", "layer-exit"},
		{"exit/p_test.go", "exit", "golang.org/x/sys/cpu", "layer-exit-tests"},
		{"runlog/p.go", "runlog", mod + "/lib", "layer-runlog"},
		{"runlog/p_test.go", "runlog", mod + "/lib", "layer-runlog-tests"},
		{"version/p.go", "version", mod + "/lib", "layer-version"},
		{"version/p_test.go", "version", "golang.org/x/mod/semver", "layer-version-tests"},
		{"api/p_test.go", "contract", "golang.org/x/mod/module", "layer-api"},
		{"other/p.go", "other", "golang.org/x/mod/module", "layer-no-row"},
		{"exit/sub/p.go", "sub", mod + "/lib", "layer-no-row"},
	}
}

// plantsStep holds the layer rows by plants. A scratch module has the stubs of the packages and one file for each
// row of the layer table, each importing what that row must refuse, and the depguard report must name that file
// with that row's list. A row that is loosened or dropped leaves its plant unreported, and this is red.
func plantsStep(ctx context.Context, _ []string, _, stderr io.Writer) error {
	bin, err := tool(ctx, "golangci-lint")
	if err != nil {
		return err
	}
	top, err := os.Getwd()
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(top, ".lint-plants-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	for _, f := range []string{"go.mod", "go.sum", ".golangci.yml"} {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(work, f), b, 0o600); err != nil {
			return err
		}
	}
	const mod = "github.com/birdtable/go-run"
	for _, p := range []string{"exit", "runlog", "version", "lib"} {
		if err := put(work, p+"/stub.go", "package "+p+"\n"); err != nil {
			return err
		}
	}
	ps := plants(mod)
	for _, p := range ps {
		if err := put(work, p.file, fmt.Sprintf("package %s\n\nimport _ %q\n", p.pkg, p.imp)); err != nil {
			return err
		}
	}
	c := cmd(ctx, work, nil, bin, "run", "--default", "none", "--enable", "depguard", "--max-issues-per-linter", "0", "--max-same-issues", "0", "./...")
	raw, _ := c.CombinedOutput() // red by exit whenever a plant is refused; the report decides
	// On Windows the report names a file with backslashes, and on a hosted runner it opens each line with ##[error].
	report := strings.ReplaceAll(string(raw), `\`, "/")
	bad := false
	for _, p := range ps {
		re := regexp.MustCompile(regexp.QuoteMeta(p.file) + `:[0-9]*:[0-9]*: .*from list '` + regexp.QuoteMeta(p.list) + `'`)
		if !re.MatchString(report) {
			fmt.Fprintf(stderr, "lint: the plant %s (%s) is not refused by %s\n", p.file, p.imp, p.list)
			bad = true
		}
	}
	if bad {
		fmt.Fprint(stderr, report)
		return errRed
	}
	return nil
}

// put writes body to the file named by its slash-separated path under dir, its folders made.
func put(dir, name, body string) error {
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

// contractStep holds the contract after apigate: the contract lines the last tag held and the tree has not are the
// lines of api/breaks/<tag>.contract.txt, and no others. The tag's contract is written out of git, and the
// comparison is the Go test of api/, which reads both. With no tag there is nothing to compare with.
func contractStep(ctx context.Context, _ []string, stdout, stderr io.Writer) error {
	out, err := cmd(ctx, "", nil, "git", "describe", "--tags", "--abbrev=0", "--match", "v*").Output()
	base := strings.TrimSpace(string(out))
	if err != nil || base == "" {
		fmt.Fprintln(stdout, "contract: no tag reachable from the head, so the contract diff is skipped")
		return nil
	}
	cleanup := func() {
		os.Remove("api/.base-contract.txt")
		os.Remove("api/.base-breaks.txt")
	}
	defer cleanup()
	b, err := cmd(ctx, "", nil, "git", "show", base+":api/contract.txt").Output()
	if err != nil {
		b = nil // the tag has no contract
	}
	if err := os.WriteFile(filepath.Join("api", ".base-contract.txt"), b, 0o600); err != nil {
		return err
	}
	want, err := os.ReadFile(filepath.Join("api", "breaks", base+".contract.txt"))
	if err != nil {
		want = nil
	}
	if err := os.WriteFile(filepath.Join("api", ".base-breaks.txt"), want, 0o600); err != nil {
		return err
	}
	got, err := cmd(ctx, "", nil, "go", "test", "./api", "-run", "^TestTheRemovedContractLinesAreTheListedOnes$", "-count=1").CombinedOutput()
	if err != nil {
		fmt.Fprintf(stderr, "%s\ncontract: the contract lines removed since %s are not the lines of api/breaks/%s.contract.txt\n", got, base, base)
		return errRed
	}
	return nil
}

// sizeStep prints the non-test Go lines of the module, and fails above the ceiling in its argument. The measure is
// every *.go file git lists, tracked or not and not ignored, that is not *_test.go, outside tools/.
func sizeStep(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var ceiling int
	if len(args) != 1 {
		return errors.New("the ceiling is the one argument")
	}
	if _, err := fmt.Sscanf(args[0], "%d", &ceiling); err != nil {
		return fmt.Errorf("ceiling %q: %w", args[0], err)
	}
	out, err := cmd(ctx, "", nil, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go", ":(exclude,glob)**/*_test.go", ":(exclude)tools").Output()
	if err != nil {
		return fmt.Errorf("git ls-files: %w", err)
	}
	n := 0
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		n += bytes.Count(b, []byte("\n"))
	}
	fmt.Fprintln(stdout, n)
	if n > ceiling {
		fmt.Fprintf(stderr, "size %d is over the ceiling of %d\n", n, ceiling)
		return errRed
	}
	return nil
}

// budgetStep holds a tier's elapsed seconds to its budget in budgets.yaml, by hatch (hatch holds no
// policy, so the values are named here). Arguments: the tier, the start in seconds, and the 1-minute load before
// it, which is empty where the host gives none.
func budgetStep(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) != 3 {
		return errors.New("arguments are <tier> <start> <load>")
	}
	tier, start, load := args[0], args[1], args[2]
	raw, err := os.ReadFile("budgets.yaml")
	if err != nil {
		return err
	}
	keys := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && !strings.HasPrefix(strings.TrimSpace(k), "#") {
			keys[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	hostOut, _ := cmd(ctx, "", nil, "uname", "-sm").Output()
	host := strings.Join(strings.Fields(string(hostOut)), "-")
	row := ""
	switch {
	case host == keys["host"]:
		row = tier + "-budget"
	case tier == "release" && os.Getenv("GITHUB_ACTIONS") == "":
		if host == "Linux-x86_64" || (strings.HasSuffix(host, "-x86_64") && (strings.HasPrefix(host, "MINGW64_NT-") || strings.HasPrefix(host, "MSYS_NT-"))) {
			row = "release-budget-session"
		}
	}
	if row == "" {
		var s int64
		if _, err := fmt.Sscanf(start, "%d", &s); err != nil {
			return fmt.Errorf("start %q: %w", start, err)
		}
		fmt.Fprintf(stdout, "%s took %ds; no budget for this kind of host (%s), so none applies\n", tier, nowUnix()-s, host)
		return nil
	}
	a := []string{"tool", "-modfile=tools/hatch.mod", "hatch", "budget", "--budgets", "budgets.yaml",
		"--row", `^(?P<id>[a-z-]+): *(?P<limit>[0-9]+) *$`, "--tier", row, "--since", start}
	if tier == "pre-commit" || tier == "pre-push" {
		a = append(a, "--window", "1", "--limit", "15", "--loaded-when", "before", "--noun", tier)
		if load != "" {
			a = append(a, "--load-before", load)
		}
	}
	a = append(a, "--excused", "a loaded host forgives it", "--advice", "see budgets.yaml for the tier's floor and its budget")
	c := cmd(ctx, "", nil, "go", a...)
	c.Stdout, c.Stderr = stdout, stderr
	if err := c.Run(); err != nil {
		return errRed
	}
	return nil
}

// cpuStep is the pre-push tier judged by its CPU time: hatch runs the tier's members, the task
// pre-push-members, in a tree of its own and holds the user plus system time of that tree to the pre-push-cpu row of
// budgets.yaml, which is the same on a quiet host and a loaded one. The pre-push-budget row, the wall row, is hatch's
// hang guard only: a push still running at ten times it is killed. On a host the file does not name, the members run and
// no budget is judged. Arguments: the start in seconds, and the 1-minute load before it, empty where the host gives none.
func cpuStep(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) != 2 {
		return errors.New("arguments are <start> <load>")
	}
	start, load := args[0], args[1]
	raw, err := os.ReadFile("budgets.yaml")
	if err != nil {
		return err
	}
	host := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "host" {
			host = strings.TrimSpace(v)
		}
	}
	members := []string{"tool", "-modfile=tools/task.mod", "task", "pre-push-members"}
	hostOut, _ := cmd(ctx, "", nil, "uname", "-sm").Output()
	kind := strings.Join(strings.Fields(string(hostOut)), "-")
	if kind != host {
		c := cmd(ctx, "", nil, "go", members...)
		c.Stdout, c.Stderr = stdout, stderr
		if err := c.Run(); err != nil {
			return errRed
		}
		fmt.Fprintf(stdout, "pre-push: no budget for this kind of host (%s), so none applies\n", kind)
		return nil
	}
	a := []string{"tool", "-modfile=tools/hatch.mod", "hatch", "budget", "--cpu", "--budgets", "budgets.yaml",
		"--row", `(?m)^(?P<id>pre-push)-budget: *(?P<limit>[0-9]+) *$`,
		"--cpu-row", `(?m)^(?P<id>pre-push)-cpu: *(?P<limit>[0-9]+) *$`,
		"--tier", "pre-push", "--since", start, "--window", "1", "--noun", "pre-push"}
	if load != "" {
		a = append(a, "--load-before", load)
	}
	a = append(a, "--advice", "see budgets.yaml for the tier's CPU row and how it was measured", "--")
	c := cmd(ctx, "", nil, "go", append(a, append([]string{"go"}, members...)...)...)
	c.Stdout, c.Stderr = stdout, stderr
	if err := c.Run(); err != nil {
		return errRed
	}
	return nil
}

// secretsGuard fails where this tree has no history of its own to scan: gitleaks passes, having scanned 0 commits,
// where git reads no history, so git is asked first, by its own exit, and the line says why.
func secretsGuard(ctx context.Context, _ []string, _, stderr io.Writer) error {
	why := ""
	top, err := cmd(ctx, "", nil, "git", "rev-parse", "--show-cdup").Output()
	switch {
	case err != nil:
		why = "git reads no repository here (no .git, or one it may not read)"
	case strings.TrimSpace(string(top)) != "":
		why = "the repository git reads is a folder above this tree, not the tree's own"
	case cmd(ctx, "", nil, "git", "rev-parse", "--verify", "--quiet", "HEAD^{commit}").Run() != nil:
		why = "the repository has no commit"
	}
	if why != "" {
		fmt.Fprintf(stderr, "secrets: no history to scan: %s, and gitleaks would pass having scanned 0 commits. secrets-tree scans the tree alone.\n", why)
		return errRed
	}
	return nil
}

// nowStep prints the time in seconds since the epoch, which a tier takes as its start.
func nowStep(_ context.Context, _ []string, stdout, _ io.Writer) error {
	_, err := fmt.Fprintln(stdout, nowUnix())
	return err
}

// loadStep prints the 1-minute load average where the host gives one, and nothing where it does not: Darwin's
// from sysctl (the second word of "{ 1.0 2.0 3.0 }") and Linux's from /proc/loadavg (the first).
func loadStep(ctx context.Context, _ []string, stdout, _ io.Writer) error {
	var f []string
	switch runtime.GOOS {
	case "darwin":
		out, err := cmd(ctx, "", nil, "/usr/sbin/sysctl", "-n", "vm.loadavg").Output()
		if err == nil {
			if w := strings.Fields(string(out)); len(w) > 1 {
				f = w[1:2]
			}
		}
	case "linux":
		if b, err := os.ReadFile("/proc/loadavg"); err == nil {
			f = strings.Fields(string(b))
			if len(f) > 0 {
				f = f[:1]
			}
		}
	}
	if len(f) == 1 {
		fmt.Fprintln(stdout, f[0])
	}
	return nil
}
