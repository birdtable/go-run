package runlog

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func logIn(t *testing.T) Log {
	t.Helper()
	return Log{Path: filepath.Join(t.TempDir(), "state", "runs.jsonl")}
}

// begin is l.Begin, the test failing when it returns an error.
func begin(t *testing.T, l Log, argv []string, extra map[string]any) *Handle {
	t.Helper()
	h, err := l.Begin(argv, extra)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// What it costs when this fails: the program's runs show the opening of a run
// that finished, with none of what it changed, or a run twice.
func TestARunReadsBackAsItsLastLine(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	h := begin(t, l, []string{"app", "tune", "cpu"}, map[string]any{"build": "test"})
	h.Change("write /etc/tmpfiles.d/app-cpu.conf")
	h.Change("apply /etc/tmpfiles.d/app-cpu.conf now")
	if err := h.Finish(0, "done", "the processor sleeps deeper"); err != nil {
		t.Fatal(err)
	}
	runs, err := l.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("one run read back as %d", len(runs))
	}
	r := runs[0]
	if r.Interrupted() || r.Code != 0 || len(r.Changes) != 2 || !slices.Contains(r.Argv, "cpu") || r.ID != h.ID() {
		t.Errorf("read back %+v", r)
	}
	// The word as the run said it, not one worked out from Code afterwards.
	if r.Outcome != "done" || r.Summary != "the processor sleeps deeper" {
		t.Errorf("the outcome read back as %q %q", r.Outcome, r.Summary)
	}
	var build string
	if ok, err := r.Field("build", &build); !ok || err != nil || build != "test" {
		t.Errorf("the build given to Begin read back as %q (%v, %v)", build, ok, err)
	}
}

// A run killed after its first change reads as interrupted, with what it had
// done so far.
//
// What it costs when this fails: a setup killed halfway by a dropped ssh
// session leaves no trace, and nobody can tell which steps it got through.
func TestARunThatNeverFinishedIsInterrupted(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	h := begin(t, l, []string{"app", "setup"}, nil)
	h.Change("install app-guard.service")
	// No Finish: the process is gone.
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 1 {
		t.Fatalf("%v, %d runs", err, len(runs))
	}
	if !runs[0].Interrupted() || len(runs[0].Changes) != 1 {
		t.Errorf("a run with no closing line read back as %+v", runs[0])
	}
}

// A line torn by the machine going off mid-write is skipped, and the run's line
// before it stands.
//
// What it costs when this fails: one torn line makes the whole history
// unreadable, on exactly the machine that just lost power.
func TestATornLineLeavesTheRestReadable(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	h := begin(t, l, []string{"app", "guard", "install"}, nil)
	h.Change("install app-guard.service")
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"` + h.ID() + `","argv":["ap`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var skipped int
	l.Skipped = &skipped
	runs, err := l.Read(t.Context())
	if err != nil || skipped != 1 || len(runs) != 1 || len(runs[0].Changes) != 1 {
		t.Errorf("after a torn line: %v, %d skipped, %+v: want the run read and the torn line counted", err, skipped, runs)
	}
}

// What it costs when this fails: the runs of a machine that never changed
// anything are an error, rather than nothing to show.
func TestNoLogIsNoRuns(t *testing.T) {
	t.Parallel()
	runs, err := logIn(t).Read(t.Context())
	if err != nil || len(runs) != 0 {
		t.Errorf("%v, %v", runs, err)
	}
}

// A nil handle is a run that changed nothing, and must cost nothing.
//
// What it costs when this fails: a program that runs with no log, which hands
// its code a nil handle, panics or reports an error at the first change it
// makes.
func TestANilHandleWritesNothing(t *testing.T) {
	t.Parallel()
	var h *Handle
	h.Change("x")
	h.Set("images", 1)
	if h.Finish(1, "failed", "could not write") != nil || h.Err() != nil || h.ID() != "" {
		t.Error("a nil handle reported something")
	}
}

// A program that keeps copies and ways back puts them in Extra, and they come back as it wrote
// them: a copy's digest once written replacing the note made at the copy, and each way back in the
// order it was recorded. The record has no field of its own for any of it.
//
// What it costs when this fails: a program's own list of copies or ways back is merged, reordered or
// dropped, and an undo of its own puts a copy back over a file it cannot tell changed.
func TestAProgramsCopiesAndWaysBackKeepTheirOrderInExtra(t *testing.T) {
	t.Parallel()
	type kept struct {
		File  string `json:"file"`
		Copy  string `json:"copy"`
		After string `json:"after,omitempty"`
	}
	type undo struct {
		How   string   `json:"how"`
		Lines []string `json:"lines,omitempty"`
		Why   string   `json:"why,omitempty"`
	}
	l := logIn(t)
	h := begin(t, l, []string{"app", "apps", "add", "notes"}, nil)
	h.Change("keep /etc/app/app.d/apps.toml as /var/lib/app/kept/x/apps.toml")
	h.Set("kept", []kept{{File: "/etc/app/app.d/apps.toml", Copy: "/var/lib/app/kept/x/apps.toml"}})
	h.Set("kept", []kept{{File: "/etc/app/app.d/apps.toml", Copy: "/var/lib/app/kept/x/apps.toml", After: Digest([]byte("new"))}})
	want := []undo{{How: "copy_back", Why: "the copy"}, {How: "restart", Lines: []string{"systemctl restart app"}, Why: "the service"}}
	h.Set("undo", want)
	if err := h.Finish(0, "done", "added"); err != nil {
		t.Fatal(err)
	}
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 1 {
		t.Fatalf("%v, %d runs", err, len(runs))
	}
	r := runs[0]
	var gotKept []kept
	if ok, err := r.Field("kept", &gotKept); !ok || err != nil || len(gotKept) != 1 || gotKept[0].After != "sha256:11507a0e2f5e69d5dfa40a62a1bd7b6ee57e6bcd85c67c9b8431b36fff21c437" {
		t.Errorf("read back kept %+v (%v, %v)", gotKept, ok, err)
	}
	var gotUndo []undo
	if ok, err := r.Field("undo", &gotUndo); !ok || err != nil || len(gotUndo) != len(want) {
		t.Fatalf("the ways back are %+v (%v, %v), want %+v", gotUndo, ok, err, want)
	}
	for i, u := range want {
		if gotUndo[i].How != u.How || gotUndo[i].Why != u.Why || !slices.Equal(gotUndo[i].Lines, u.Lines) {
			t.Errorf("way back %d is %+v, want %+v", i, gotUndo[i], u)
		}
	}
}

// A line is read whatever fields it holds: the record has no version, so a line another release wrote,
// with a version, a kept list and an undo list of its own at the top, reads as a record by its id,
// and each field the record has no name for is kept in Extra beside the ones the line holds under
// extra, for Record.Field to read. A program that versions its fields keeps the version in Extra.
//
// What it costs when this fails: a log written with a record version, or with fields that have since
// moved to Extra, is refused or dropped whole, when only the runs that are not records should be
// skipped, or it is read and the fields it held at the top are gone.
func TestALineWithFieldsTheRecordDoesNotHaveIsStillARecord(t *testing.T) {
	t.Parallel()
	in := `{"version":3,"id":"a","argv":["app"],"started":"2026-11-13T09:00:00Z","code":0,"changes":[],"kept":[{"file":"/x","copy":"/y"}],"undo":[{"how":"none"}],"extra":{"v":7}}` + "\n" +
		`{"version":2,"id":"b","started":"2026-11-13T09:00:00Z"}` + "\n" + `{"id":""}` + "\nnot json\n"
	runs, err := ReadFrom(t.Context(), strings.NewReader(in), 0)
	if err != nil || len(runs) != 2 || runs[0].ID != "a" || runs[1].ID != "b" {
		t.Fatalf("%d runs, %v", len(runs), err)
	}
	var v, version int
	var kept []struct {
		File string `json:"file"`
		Copy string `json:"copy"`
	}
	if ok, err := runs[0].Field("v", &v); !ok || err != nil || v != 7 || len(runs[0].Extra) != 4 {
		t.Errorf("the caller's fields are %v (v %d, %v)", runs[0].Extra, v, err)
	}
	if ok, err := runs[0].Field("version", &version); !ok || err != nil || version != 3 {
		t.Errorf("version read as %d (%v, %v)", version, ok, err)
	}
	if ok, err := runs[0].Field("kept", &kept); !ok || err != nil || len(kept) != 1 || kept[0].File != "/x" || kept[0].Copy != "/y" {
		t.Errorf("kept read as %+v (%v, %v)", kept, ok, err)
	}
	if ok, _ := runs[0].Field("undo", new(any)); !ok || len(runs[1].Extra) != 1 {
		t.Errorf("undo is not kept (%v), or the second run's version is not: %v", ok, runs[1].Extra)
	}
}

// image is a program's own field, as a container's image before and after an
// update is one.
type image struct {
	Container string `json:"container"`
	Before    string `json:"before"`
	After     string `json:"after,omitempty"`
}

// A program's own field is any JSON value, a later Set replacing the earlier, written under extra
// and read back whole: one named like the record's own (kept) is the program's and leaves the
// record's Kept as it was. A value that does not encode is Err.
//
// What it costs when this fails: a program's undo finds nothing to put back,
// or its field takes the place of the record's own, so an undo copies back a
// file the run never kept.
func TestAProgramsOwnFieldIsUnderExtra(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	h := begin(t, l, []string{"app", "updates", "install", "notes"}, nil)
	h.Set("images", []image{{Container: "notes", Before: "sha256:aa"}})
	h.Set("images", []image{{Container: "notes", Before: "sha256:aa", After: "sha256:dd"}, {Container: "notes-db", Before: "sha256:cc"}})
	h.Set("kept", 1)
	h.Set("stack", "notes")
	if err := h.Finish(0, "done", "updated"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &top); err != nil || top["extra"] == nil || top["images"] != nil || top["kept"] != nil {
		t.Fatalf("the last line is %s (%v)", lines[len(lines)-1], err)
	}
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 1 {
		t.Fatalf("%v, %d runs", err, len(runs))
	}
	r := runs[0]
	var im []image
	if ok, err := r.Field("images", &im); !ok || err != nil || len(im) != 2 || im[0].After != "sha256:dd" || im[1].After != "" {
		t.Errorf("images read back %+v (%v)", im, err)
	}
	var kept int
	if ok, err := r.Field("kept", &kept); !ok || err != nil || kept != 1 {
		t.Errorf("the program's kept read back %d (%v)", kept, err)
	}
	if ok, _ := r.Field("none", &im); ok {
		t.Error("a field never set reads as there")
	}
	again, err := json.Marshal(r)
	if err != nil || string(again) != lines[len(lines)-1] {
		t.Errorf("written again:\n%s\nwas\n%s", again, lines[len(lines)-1])
	}
	h.Set("bad", func() {})
	if h.Err() == nil {
		t.Error("a field that does not encode was taken")
	}
	if _, err := l.Begin([]string{"app"}, map[string]any{"bad": make(chan int)}); err == nil {
		t.Error("Begin took a field that does not encode")
	}
}

// Two programs' runs in one process, at once, each keep only what was said to
// their own Handle: no copy, way back or field of one reaches the other.
//
// What it costs when this fails: one run's undo puts back the other run's
// files, or a program's tests that record runs must not run in parallel.
func TestTwoRunsAtOnceInOneProcessKeepOnlyTheirOwn(t *testing.T) {
	t.Parallel()
	const each = 50
	names := []string{"one", "two"}
	logs := make([]Log, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		logs[i] = logIn(t)
		h := begin(t, logs[i], []string{"app-" + name}, nil)
		wg.Go(func() {
			for n := range each {
				h.Change(fmt.Sprintf("%s.%d", name, n))
				h.Set(name, n)
			}
			_ = h.Finish(0, "done", "")
		})
	}
	wg.Wait()
	for i, name := range names {
		runs, err := logs[i].Read(t.Context())
		if err != nil || len(runs) != 1 {
			t.Fatalf("%s's log: %d runs, %v", name, len(runs), err)
		}
		r := runs[0]
		held := 0
		for _, c := range r.Changes {
			if !strings.HasPrefix(c, name+".") {
				t.Errorf("%s's run holds %q, another run's change", name, c)
			}
			held++
		}
		if held != each {
			t.Errorf("%s recorded %d changes, not its own %d", name, held, each)
		}
		if len(r.Extra) != 1 || r.Extra[name] == nil {
			t.Errorf("%s's fields are %v", name, slices.Collect(maps.Keys(r.Extra)))
		}
	}
}

// A line of this version reads back and is written again byte for byte.
//
// What it costs when this fails: a program that reads its log and writes a run again changes the
// run's line, and a record's ways back or fields drift.
func TestALineIsWrittenAgainByteForByte(t *testing.T) {
	t.Parallel()
	golden, err := os.ReadFile(filepath.Join("testdata", "a-record.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	runs, err := Log{Path: filepath.Join("testdata", "a-record.jsonl")}.Read(t.Context())
	if err != nil || len(runs) != 1 {
		t.Fatalf("%v, %d runs", err, len(runs))
	}
	r := runs[0]
	var version int
	if ok, err := r.Field("version", &version); !ok || err != nil || version != 3 || len(r.Extra) != 5 || r.Interrupted() {
		t.Errorf("read back %+v", r)
	}
	var im []image
	if ok, err := r.Field("images", &im); !ok || err != nil || len(im) != 2 || im[0].After != "sha256:dd" {
		t.Errorf("images %+v (%v)", im, err)
	}
	again, err := marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSuffix(string(golden), "\n"); string(again) != want {
		t.Errorf("written again:\n%s\nwas\n%s", again, want)
	}
}
