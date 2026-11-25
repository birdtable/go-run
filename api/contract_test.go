// Package contract holds the gate of api/contract.txt: a test, and no code, so that nothing imports it.
package contract

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// contractSystemsOf is the systems a line's third field names: all, or a comma list of darwin, linux and windows.
func contractSystemsOf(field string) ([]string, bool) {
	if field == "all" {
		return []string{"darwin", "linux", "windows"}, true
	}
	list := strings.Split(field, ",")
	for _, s := range list {
		if !slices.Contains([]string{"darwin", "linux", "windows"}, s) {
			return nil, false
		}
	}
	return list, true
}

// contractHolds is whether package dir has, for system, a test file whose name and build constraint admit it on
// both amd64 and arm64 and that declares func name(*testing.T), and whether that function calls Skip, Skipf or
// SkipNow.
func contractHolds(dir, system, name string) (found bool, skipped bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		admitted := true
		for _, arch := range []string{"amd64", "arm64"} {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = system, arch, false
			ok, err := ctx.MatchFile(dir, e.Name())
			if err != nil {
				return false, false, err
			}
			admitted = admitted && ok
		}
		if !admitted {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return false, false, err
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != name || fn.Body == nil || len(fn.Type.Params.List) != 1 {
				continue
			}
			found = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if sel, ok := c.Fun.(*ast.SelectorExpr); ok && slices.Contains([]string{"Skip", "Skipf", "SkipNow"}, sel.Sel.Name) {
						skipped = true
					}
				}
				return true
			})
		}
	}
	return found, skipped, nil
}

// contractProblems is what is wrong with the text of api/contract.txt for the module at root: a line that is not
// four fields, a file that is not sorted or holds a package and test twice, and a test that is not there, or is
// skipped, or sits in a file that shuts a named system out. The lines a tag drops are checked by `task api`, which
// reads the tag.
func contractProblems(root, text string) []string {
	var problems []string
	var lines []string
	for l := range strings.SplitSeq(text, "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	if !slices.IsSorted(lines) {
		problems = append(problems, "the file is not sorted: LC_ALL=C sort puts the lines in another order")
	}
	seen := map[string]bool{}
	for _, l := range lines {
		f := strings.Split(l, "\t")
		if len(f) != 4 || slices.Contains(f, "") {
			problems = append(problems, "not four tab-separated fields: "+l)
			continue
		}
		pkg, name := f[0], f[1]
		if seen[pkg+"\t"+name] {
			problems = append(problems, "twice: "+pkg+" "+name)
		}
		seen[pkg+"\t"+name] = true
		systems, ok := contractSystemsOf(f[2])
		if !ok {
			problems = append(problems, name+": "+f[2]+" is neither all nor a comma list of darwin, linux and windows")
			continue
		}
		for _, s := range systems {
			found, skipped, err := contractHolds(filepath.Join(root, filepath.FromSlash(pkg)), s, name)
			switch {
			case err != nil:
				problems = append(problems, name+": "+err.Error())
			case !found:
				problems = append(problems, name+": no test of that name in "+pkg+" is built on "+s)
			case skipped:
				problems = append(problems, name+": skips itself, and the line names "+s)
			}
		}
	}
	return problems
}

// TestTheContractIndexIsHeldByItsTests is the gate of api/contract.txt: each line names a test
// that exists in its package, built and not skipped on each system the line names, and the file is sorted with no
// package and test twice. A line a tag drops must be listed in api/breaks/<tag>.contract.txt, which `task api` reads.
//
// What it costs when this fails: a product cites a line of the index as a promise, and the test that holds it is
// gone, or never runs on the system it rests on.
func TestTheContractIndexIsHeldByItsTests(t *testing.T) {
	t.Parallel()
	text, err := os.ReadFile("contract.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := contractProblems("..", string(text)); len(got) != 0 {
		t.Errorf("api/contract.txt:\n%s", strings.Join(got, "\n"))
	}
}

// TestTheContractGateIsRedForEachBreakItNames plants each break in a module made in a folder: the tests of its
// package stand in the files the gate reads, and each plant must name its line.
func TestTheContractGateIsRedForEachBreakItNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"p/a_test.go":             "package p\n\nimport \"testing\"\n\nfunc TestAll(t *testing.T) {}\n\nfunc TestSkips(t *testing.T) { t.Skip(\"x\") }\n",
		"p/modes_unix_test.go":    "//go:build unix\n\npackage p\n\nimport \"testing\"\n\nfunc TestUnixOnly(t *testing.T) {}\n",
		"p/modes_windows_test.go": "package p\n\nimport \"testing\"\n\nfunc TestWindowsOnly(t *testing.T) {}\n",
	}
	for name, body := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const (
		all  = "p\tTestAll\tall\tholds\n"
		unix = "p\tTestUnixOnly\tlinux,darwin\tholds\n"
		win  = "p\tTestWindowsOnly\twindows\tholds\n"
	)
	for _, c := range []struct{ name, text, want string }{
		{"a clean file", all + unix + win, ""},
		{"a test that is not there", all + "p\tTestNone\tall\tholds\n", "TestNone: no test of that name in p is built on darwin"},
		{"the windows line pointed at the unix file", "p\tTestUnixOnly\twindows\tholds\n", "TestUnixOnly: no test of that name in p is built on windows"},
		{"the unix line pointed at the windows file", "p\tTestWindowsOnly\tall\tholds\n", "TestWindowsOnly: no test of that name in p is built on darwin"},
		{"a test that skips itself", "p\tTestSkips\tall\tholds\n", "TestSkips: skips itself"},
		{"an unsorted file", unix + all, "not sorted"},
		{"a line twice", all + all, "twice: p TestAll"},
		{"a line of three fields", "p\tTestAll\tall\n", "not four tab-separated fields"},
		{"a system that is not one", "p\tTestAll\tplan9\tholds\n", "neither all nor a comma list"},
	} {
		got := strings.Join(contractProblems(root, c.text), "\n")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: problems %q, want one with %q", c.name, got, c.want)
		}
	}
}

// removedProblems compares the contract a tag held with the one the tree holds: a line the tag held whose package
// and test the tree has no line for is removed, and the removed lines must be the lines of the breaks file, no
// more and no fewer (api/breaks/<tag>.contract.txt). Each text is lines; a blank line is none.
func removedProblems(base, now, listed string) []string {
	lines := func(text string) []string {
		var out []string
		for l := range strings.SplitSeq(text, "\n") {
			if l != "" {
				out = append(out, l)
			}
		}
		return out
	}
	key := func(l string) string {
		f := strings.Split(l, "\t")
		return strings.Join(f[:min(2, len(f))], "\t")
	}
	held := map[string]bool{}
	for _, l := range lines(now) {
		held[key(l)] = true
	}
	var removed []string
	for _, l := range lines(base) {
		if !held[key(l)] {
			removed = append(removed, l)
		}
	}
	var problems []string
	for _, l := range removed {
		if !slices.Contains(lines(listed), l) {
			problems = append(problems, "removed and not listed: "+l)
		}
	}
	for _, l := range lines(listed) {
		if !slices.Contains(removed, l) {
			problems = append(problems, "listed and not removed: "+l)
		}
	}
	return problems
}

// TestTheRemovedContractLinesAreTheListedOnes is the comparison task api hands the base tag's contract to: the
// file .base-contract.txt is that contract (empty when the tag has none) and .base-breaks.txt is the tag's
// api/breaks/<tag>.contract.txt (empty when it has none); the task writes both and removes them. With
// .base-contract.txt absent there is no tag to compare with and the test has nothing to say.
//
// What it costs when this fails: a promise a product relied on is gone from the index with no one having said so.
func TestTheRemovedContractLinesAreTheListedOnes(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(".base-contract.txt"); err != nil {
		return
	}
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return string(b)
	}
	now := read("contract.txt")
	if got := removedProblems(read(".base-contract.txt"), now, read(".base-breaks.txt")); len(got) != 0 {
		t.Errorf("the contract lines removed since the tag are not the listed ones:\n%s", strings.Join(got, "\n"))
	}
}

// The comparison is red for a removed line that is not listed and for a listed line that is not removed, and
// green for a listed removal and for nothing removed.
func TestTheRemovedLinesComparisonIsRedForEachPlant(t *testing.T) {
	t.Parallel()
	const (
		a = "p\tTestA\tall\tA holds\n"
		b = "p\tTestB\tall\tB holds\n"
	)
	for _, c := range []struct{ name, base, now, listed, want string }{
		{"a removed line not listed", a + b, a, "", "removed and not listed: p\tTestB\tall\tB holds"},
		{"a removed line listed", a + b, a, b, ""},
		{"nothing removed", a + b, a + b, "", ""},
		{"a line reworded is not removed", a + b, a + "p\tTestB\tall\tB holds, said otherwise\n", "", ""},
		{"a line listed that is still there", a + b, a + b, b, "listed and not removed: p\tTestB\tall\tB holds"},
	} {
		got := strings.Join(removedProblems(c.base, c.now, c.listed), "\n")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: problems %q, want one with %q", c.name, got, c.want)
		}
	}
}
