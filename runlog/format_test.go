package runlog

import (
	"bytes"
	jsonv1 "encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec" //nolint:depguard // cli.process.1: the test builds this package with the go tool, and this module has no runner
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// oldLine is a record as the writer before encoding/json/v2 wrote it: encoding/json's Marshal of the
// same struct, which escaped HTML and the two JavaScript line separators and sorted a map's keys.
func oldLine(t *testing.T, r Record) []byte {
	t.Helper()
	b, err := jsonv1.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Every record type written by the new writer is byte-equal to the old writer's line: an opening
// line, a closing one, a copy kept and a way back as a caller's own fields, a caller's field of every
// JSON kind, and text that encoding/json escaped (<, >, &,
// U+2028 and U+2029) in each place a string goes.
//
// What it costs when this fails: a log written by one release and appended to by the next holds two
// spellings of one record, and a program that compares or digests a line reads a change that never
// happened.
func TestEveryRecordTypeIsWrittenByteEqualToTheOldWritersLine(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 14, 9, 8, 7, 123456789, time.UTC)
	html := `<a href="x">&</a>` + "  "
	cases := map[string]Record{
		"an opening line":      {ID: "a1", Argv: []string{"app", "--apply"}, Started: at, Changes: []string{}},
		"nil argv and changes": {ID: "f6", Started: at},
		"a closing line":       {ID: "a1", Argv: []string{"app"}, Started: at, Finished: at.Add(time.Second), Code: 2, Outcome: "failed", Summary: "it broke", Changes: []string{"write /etc/x"}},
		"a copy and a way back": {ID: "b2", Argv: []string{"app"}, Started: at, Changes: []string{"write /etc/x"}, Extra: map[string]jsonv1.RawMessage{
			"kept": jsonv1.RawMessage(`[{"file":"/etc/x","copy":"/k/x"},{"file":"/etc/y","copy":"/k/y","after":"` + Digest([]byte("y")) + `"}]`),
			"undo": jsonv1.RawMessage(`[{"how":"copy_back"},{"how":"command","lines":["app undo b2"]},{"how":"none","why":"nothing to put back"}]`),
		}},
		"text json escaped": {ID: "c3", Argv: []string{html, "é", "日本"}, Started: at, Outcome: html, Summary: html,
			Changes: []string{html}, Extra: map[string]jsonv1.RawMessage{"kept": jsonv1.RawMessage(`[{"file":"<a>& ","copy":"<a>&"}]`)}},
		"a caller's fields": {ID: "d4", Argv: []string{"app"}, Started: at, Changes: []string{}, Extra: map[string]jsonv1.RawMessage{
			"z":      jsonv1.RawMessage(`{"b":1,"a":[1,2.5,"x",null,true]}`),
			"images": jsonv1.RawMessage(`[{"container":"<a>&\u2028"}]`),
			"a":      jsonv1.RawMessage(`"<>&"`),
			"n":      jsonv1.RawMessage(`12`),
		}},
	}
	ran := 0
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range cases {
			if !yield(k) {
				return
			}
		}
	}) {
		ran++
		got, err := marshal(cases[name])
		if want := oldLine(t, cases[name]); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s (%v):\nnew %s\nold %s", name, err, got, want)
		}
	}
	if ran != len(cases) || ran != 6 {
		t.Fatalf("ran %d of %d cases", ran, len(cases))
	}
	// A line a Handle writes is the same bytes: the file, not only the function.
	l := logIn(t)
	l.NewID, l.Now = func() string { return "e5" }, func() time.Time { return at }
	h := begin(t, l, []string{"app", html}, map[string]any{"note": html})
	h.Change(html)
	if err := h.Finish(0, "done", html); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := l.Read(t.Context())
	lines := bytes.Split(bytes.TrimRight(file, "\n"), []byte("\n"))
	if err != nil || len(runs) != 1 || len(lines) != 3 || !bytes.Equal(lines[2], oldLine(t, runs[0])) {
		t.Errorf("the last line of a run's file is not the old writer's line (%v, %d runs, %d lines):\n%s", err, len(runs), len(lines), file)
	}
}

// A nil list is written null and an empty one [], as v1 wrote them: the old line is the contract,
// whatever json/v2 does by default.
//
// What it costs when this fails: a reader that tells [] from null reads a record of one release
// differently from another's.
func TestANilListIsWrittenNullAndAnEmptyOneEmpty(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	ran := 0
	for want, r := range map[string]Record{
		`{"id":"n1","argv":null,"started":"2026-11-14T00:00:00Z","code":0,"changes":null}`: {ID: "n1", Started: started},
		`{"id":"n1","argv":[],"started":"2026-11-14T00:00:00Z","code":0,"changes":[]}`:     {ID: "n1", Started: started, Argv: []string{}, Changes: []string{}},
	} {
		ran++
		if b, err := marshal(r); err != nil || string(b) != want || !bytes.Equal(b, oldLine(t, r)) {
			t.Errorf("a record is written %q (%v), want %q, as the old writer wrote it", b, err, want)
		}
	}
	if ran != 2 {
		t.Fatalf("ran %d of 2 cases", ran)
	}
}

// No exported GiveGroup or Compact is declared in the package, and the generated API file names
// neither; the reader that finds them finds a planted one.
//
// What it costs when this fails: a caller reaches for a group option or a compaction that rewrites
// files under no lock of its own, and writes a log through code that this package meant to leave to
// the caller's own lock and its file writer.
func TestNoGiveGroupOrCompactIsExportedOrInTheAPIFile(t *testing.T) {
	t.Parallel()
	src := func(path string) []string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return exportedNames(t, string(b))
	}
	var seen []string
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files: %v", err)
	}
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") && f != "nojsonv2.go" {
			seen = append(seen, src(f)...)
		}
	}
	for _, banned := range []string{"GiveGroup", "Compact", "ErrChanged", "Recorder"} {
		if slices.Contains(seen, banned) {
			t.Errorf("runlog exports %s", banned)
		}
	}
	if !slices.Contains(seen, "Lines") {
		t.Errorf("the reader found %q, and misses Lines", seen)
	}
	if got := exportedNames(t, "package p\n\nfunc GiveGroup() {}\ntype T struct{}\nfunc (T) Compact() {}\n"); !slices.Equal(got, []string{"Compact", "GiveGroup", "T"}) {
		t.Errorf("the reader found %q in the planted source, want Compact, GiveGroup and T", got)
	}
}

// exportedNames is the sorted exported names a Go source declares at its top level, and the exported
// methods of its types.
func exportedNames(t *testing.T, src string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				out = append(out, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						out = append(out, s.Name.Name)
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() {
							out = append(out, n.Name)
						}
					}
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// goBuild runs go build on this package from its folder, with the tags, under an environment that
// has no GOEXPERIMENT, and is its output and its error.
func goBuild(t *testing.T, tags string) (string, error) {
	t.Helper()
	args := []string{"build", "-o", os.DevNull}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	cmd := exec.CommandContext(t.Context(), "go", append(args, ".")...) //nolint:gosec // the go tool, with this test's own arguments
	cmd.Env = slices.DeleteFunc(cmd.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GOEXPERIMENT=") })
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The package builds with GOEXPERIMENT unset, and with the build tag nojsonv2 the build fails with a
// message that says why.
//
// What it costs when this fails: the package needs a flag of the Go release to build, and a program
// that imports it cannot build without one, or a build that is meant to refuse json/v2 passes in
// silence and writes lines the reader does not know.
func TestThePackageBuildsWithNoGoExperimentAndFailsWithTheTagNojsonv2Saying(t *testing.T) {
	t.Parallel()
	if out, err := goBuild(t, ""); err != nil {
		t.Fatalf("the build with GOEXPERIMENT unset failed: %v\n%s", err, out)
	}
	out, err := goBuild(t, "nojsonv2")
	if err == nil || !strings.Contains(out, "runlog_needs_encoding_json_v2_so_remove_the_nojsonv2_build_tag") {
		t.Errorf("the build with the tag nojsonv2 gave %v and %q, want a failure that names the reason", err, out)
	}
}
