package runlog

import (
	"bytes"
	"context"
	jsonv1 "encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fixedLog is a log with a clock that stands still and the id "e5", so a line's bytes are known.
func fixedLog(t *testing.T) Log {
	t.Helper()
	l := logIn(t)
	at := time.Date(2026, 11, 14, 9, 8, 7, 123456789, time.UTC)
	l.NewID, l.Now = func() string { return "e5" }, func() time.Time { return at }
	return l
}

// fileOf is the log's bytes, "" when it is not there.
func fileOf(t *testing.T, l Log) string {
	t.Helper()
	b, err := os.ReadFile(l.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(b)
}

// values is each entry's value as a string, for entries whose values are strings.
func values(t *testing.T, entries []Entry) []string {
	t.Helper()
	var out []string
	for _, e := range entries {
		var s string
		if err := e.Decode(&s); err != nil {
			t.Fatalf("the entry %s: %v", e.Value, err)
		}
		out = append(out, s)
	}
	return out
}

// An entry is one line that names its run under entry, with its time and its value and no id, from a Handle
// and from a Log with no Handle alike, and Read, Find and Resume give the run's entries in the order written.
//
// What it costs when this fails: a reader of the release before reads an entry line as a run, or a program
// that adds entries from two processes reads them back in another order or not at all.
func TestAnEntryIsALineOfItsOwnThatNamesItsRunAndHasNoID(t *testing.T) {
	t.Parallel()
	l := fixedLog(t)
	h := begin(t, l, []string{"app"}, nil)
	before := fileOf(t, l)
	h.Entry(map[string]string{"file": "/etc/x.conf"})
	if err := errors.Join(h.Err(), l.Entry(t.Context(), h.ID(), "second")); err != nil {
		t.Fatal(err)
	}
	const want = `{"entry":"e5","time":"2026-11-14T09:08:07.123456789Z","value":{"file":"/etc/x.conf"}}` + "\n" +
		`{"entry":"e5","time":"2026-11-14T09:08:07.123456789Z","value":"second"}` + "\n"
	if got := strings.TrimPrefix(fileOf(t, l), before); got != want {
		t.Fatalf("the entries are written as\n%swant\n%s", got, want)
	}
	if err := h.Finish(0, "done", ""); err != nil {
		t.Fatal(err)
	}
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 1 || len(runs[0].Entries) != 2 || runs[0].Interrupted() {
		t.Fatalf("read %+v, %v, want one finished run of two entries", runs, err)
	}
	var first map[string]string
	if err := runs[0].Entries[0].Decode(&first); err != nil || first["file"] != "/etc/x.conf" || !runs[0].Entries[0].Time.Equal(l.Now()) {
		t.Errorf("the first entry is %+v at %v (%v)", first, runs[0].Entries[0].Time, err)
	}
	if string(runs[0].Entries[1].Value) != `"second"` {
		t.Errorf("the second entry's value is %s", runs[0].Entries[1].Value)
	}
	found, err := l.Find(t.Context(), "e")
	if err != nil || !reflect.DeepEqual(found, runs[0]) {
		t.Errorf("Find gives %+v, %v, want the run with its entries", found, err)
	}
	r, err := l.Resume(t.Context(), "e5")
	if err != nil {
		t.Fatal(err)
	}
	r.Entry("third")
	r.Change("write /etc/x")
	runs, err = l.Read(t.Context())
	if got := values(t, runs[0].Entries[1:]); err != nil || r.Err() != nil || !reflect.DeepEqual(got, []string{"second", "third"}) || len(runs[0].Changes) != 1 {
		t.Errorf("after a resumed Handle's entry and change: %q, %v, %v, %+v", got, err, r.Err(), runs[0].Changes)
	}
	if strings.Count(fileOf(t, l), `"value"`) != 3 {
		t.Errorf("a record line holds an entry:\n%s", fileOf(t, l))
	}
}

// Twenty entries of 1 MiB on one run, half from the run's Handle and half from a Log with no Handle, all read
// back, in order: no line passes MaxLine however many entries the run has.
//
// What it costs when this fails: a run's later entries are written, reported as kept, and never read back.
func TestTwentyEntriesOfAMebibyteOnOneRunAllReadBack(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	h := begin(t, l, []string{"app"}, nil)
	var want []string
	for i := range 20 {
		v := strings.Repeat(string(rune('a'+i)), 1<<20)
		want = append(want, v)
		if i%2 == 0 {
			h.Entry(v)
		} else if err := (Log{Path: l.Path}).Entry(t.Context(), h.ID(), v); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Finish(0, "done", ""); err != nil {
		t.Fatal(err)
	}
	var skipped int
	l.Skipped = &skipped
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 1 || skipped != 0 {
		t.Fatalf("read %d runs, %d skipped, %v", len(runs), skipped, err)
	}
	if got := values(t, runs[0].Entries); len(got) != 20 || !reflect.DeepEqual(got, want) {
		t.Errorf("read back %d entries, want the twenty in the order written", len(got))
	}
}

// A write whose line is longer than MaxLine writes nothing and is ErrLineTooLong: an entry from a Handle or a
// Log, a Set, a Change and Begin's first line. The file's bytes are as they were, a line of exactly MaxLine is
// written and read, and after a refused Set and Change the run's close is still written, without them.
//
// What it costs when this fails: a write returns nil for a line Read then skips, so a program is told a
// record was kept that it never reads back; or one long field stops every later line of its run.
func TestALineLongerThanMaxLineIsRefusedAtTheWriteAndTheFileIsAsItWas(t *testing.T) {
	t.Parallel()
	l := fixedLog(t)
	l.MaxLine = 400
	h := begin(t, l, []string{"app"}, nil)
	h.Entry("")
	overhead := len(fileOf(t, l)) - strings.Index(fileOf(t, l), `{"entry"`) - 1
	h.Entry(strings.Repeat("x", l.MaxLine-overhead))
	if err := h.Err(); err != nil {
		t.Fatalf("a line of exactly MaxLine: %v", err)
	}
	before := fileOf(t, l)
	long := strings.Repeat("x", l.MaxLine-overhead+1)
	refused := 0
	for name, write := range map[string]func() error{
		"Handle.Entry": func() error { o, err := l.Resume(t.Context(), "e5"); o.Entry(long); return errors.Join(err, o.Err()) },
		"Log.Entry":    func() error { return l.Entry(t.Context(), "e5", long) },
		"Handle.Set":   func() error { h.Set("field", long); return h.Err() },
		"Begin":        func() error { _, err := l.Begin([]string{long}, nil); return err },
		"the default MaxLine": func() error {
			return Log{Path: l.Path}.Entry(t.Context(), "e5", strings.Repeat("x", DefaultMaxLine))
		},
	} {
		refused++
		if err := write(); !errors.Is(err, ErrLineTooLong) {
			t.Errorf("%s: %v, want ErrLineTooLong", name, err)
		}
		if got := fileOf(t, l); got != before {
			t.Errorf("%s changed the file: %d bytes, was %d", name, len(got), len(before))
		}
	}
	h.Change(long)
	h.Change("write /etc/x")
	if err := h.Finish(0, "done", ""); !errors.Is(err, ErrLineTooLong) || refused != 5 {
		t.Errorf("Finish is %v after %d refusals, want the first refusal kept", err, refused)
	}
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 1 || runs[0].Interrupted() || runs[0].Extra != nil || !reflect.DeepEqual(runs[0].Changes, []string{"write /etc/x"}) {
		t.Fatalf("read %+v, %v, want the run closed with its one short change", runs, err)
	}
	if got := values(t, runs[0].Entries); len(got) != 2 || len(got[1]) != l.MaxLine-overhead {
		t.Errorf("read back %d entries, want the empty one and the one of exactly MaxLine", len(got))
	}
	fresh := logIn(t)
	fresh.MaxLine = 50
	if _, err := fresh.Begin([]string{long}, nil); !errors.Is(err, ErrLineTooLong) || fileOf(t, fresh) != "" {
		t.Errorf("Begin on no log: %v, and the log holds %q", err, fileOf(t, fresh))
	}
}

// 602 entries of 415 bytes on one run, from the Handle and from a Log with no Handle, cost their own bytes:
// the file is under twice the entries' bytes, it holds one line for each entry and the run's two record lines,
// and no entry reads the log, which Skipped shows, as Read sets it whenever it runs.
//
// What it costs when this fails: a run's bytes grow with the square of its entries, 73 MB for these 602, and
// each entry takes longer as the log grows.
func Test602EntriesCostTheirOwnBytesAndNoEntryReadsTheLog(t *testing.T) {
	t.Parallel()
	const entries, size = 602, 415
	l := logIn(t)
	unread := -1
	l.Skipped = &unread
	h := begin(t, l, []string{"app"}, nil)
	for i := range entries {
		v := fmt.Sprintf("%0*d", size-2, i) // 415 bytes as JSON, with its quotes
		if i%2 == 0 {
			h.Entry(v)
		} else if err := l.Entry(t.Context(), h.ID(), v); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Finish(0, "done", ""); err != nil {
		t.Fatal(err)
	}
	file := fileOf(t, l)
	if len(file) >= 2*entries*size || strings.Count(file, "\n") != entries+2 || strings.Count(file, `"id"`) != 2 {
		t.Errorf("the log is %d bytes in %d lines, %d of them record lines, want under %d bytes in %d lines, 2 of them record lines",
			len(file), strings.Count(file, "\n"), strings.Count(file, `"id"`), 2*entries*size, entries+2)
	}
	if unread != -1 {
		t.Errorf("an entry read the log: Skipped was set to %d", unread)
	}
	runs, err := l.Read(t.Context())
	if err != nil || unread != 0 || len(runs) != 1 || len(runs[0].Entries) != entries {
		t.Fatalf("read %d runs, Skipped %d, %v", len(runs), unread, err)
	}
	for i, v := range values(t, runs[0].Entries) {
		if v != fmt.Sprintf("%0*d", size-2, i) {
			t.Fatalf("entry %d reads back as %q", i, v)
		}
	}
}

// A log with no entry lines, as the release before wrote it, reads as it did: each run as its last line,
// Entries nil, a torn line and a line with no id skipped and counted, and a record line of another program
// that holds entry, time and value at its top level beside its id is still a record, with them in Extra.
//
// What it costs when this fails: a log written before entry lines reads differently after the upgrade, or
// another program's record with a field named entry is taken for an entry and its run is lost.
func TestALogWithNoEntryLinesReadsAsItDidBefore(t *testing.T) {
	t.Parallel()
	const in = `{"id":"a1","argv":["app"],"started":"2026-11-10T09:00:00Z","code":0,"changes":[]}` + "\n" +
		`{"id":"b2","argv":["other"],"started":"2026-11-10T09:00:01Z","code":0,"changes":[],"entry":"a1","time":5,"value":[1]}` + "\n" +
		`{"argv":["no id"]}` + "\n" +
		`{"id":"a1","argv":["app"],"started":"2026-11-10T09:00:00Z","finished":"2026-11-10T09:00:02Z","code":3,"outcome":"failed","changes":["write /etc/x"],"extra":{"kept":[1]}}` + "\n" +
		`{"id":"a1","argv":["app"],"started":"2026-11-1`
	runs, skipped, err := readFrom(t.Context(), strings.NewReader(in), 0)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	want := []Record{
		{ID: "a1", Argv: []string{"app"}, Started: at, Finished: at.Add(2 * time.Second), Code: 3, Outcome: "failed", Changes: []string{"write /etc/x"},
			Extra: map[string]jsonv1.RawMessage{"kept": jsonv1.RawMessage(`[1]`)}},
		{ID: "b2", Argv: []string{"other"}, Started: at.Add(time.Second), Changes: []string{},
			Extra: map[string]jsonv1.RawMessage{"entry": jsonv1.RawMessage(`"a1"`), "time": jsonv1.RawMessage(`5`), "value": jsonv1.RawMessage(`[1]`)}},
	}
	if err != nil || skipped != 2 || !reflect.DeepEqual(runs, want) {
		t.Fatalf("read %+v, %d skipped, %v, want %+v and 2 skipped", runs, skipped, err, want)
	}
	if runs[0].Entries != nil || runs[1].Entries != nil {
		t.Errorf("a log with no entry lines has entries: %+v", runs)
	}
}

// Read folds a run's entry lines in the order of the file, wherever they fall around its record lines, before
// the first among them. An entry line whose run the log does not hold, one that is torn, one with a name twice
// and one whose time is not a time are each skipped and counted, and each costs that entry alone.
//
// What it costs when this fails: a crash in the middle of one entry's write loses the entries around it, or an
// entry written for a run that was never begun is given to another run.
func TestEntryLinesFoldInFileOrderAndADamagedOneCostsThatEntryAlone(t *testing.T) {
	t.Parallel()
	const in = `{"entry":"a1","time":"2026-11-10T08:59:59Z","value":"early"}` + "\n" +
		`{"id":"a1","argv":["app"],"started":"2026-11-10T09:00:00Z","code":0,"changes":[]}` + "\n" +
		`{"entry":"a1","time":"2026-11-10T09:00:00.5Z","value":"between"}` + "\n" +
		`{"entry":"nobody","time":"2026-11-10T09:00:00.6Z","value":"orphan"}` + "\n" +
		`{"entry":"nobody","time":"2026-11-10T09:00:00.6Z","value":"orphan again"}` + "\n" +
		`{"entry":"a1","time":"not a time","value":"bad time"}` + "\n" +
		`{"entry":"a1","entry":"a1","time":"2026-11-10T09:00:00.7Z","value":"twice"}` + "\n" +
		`{"entry":"a1","time":"2026-11-10T09:00:00.8Z","value":"to` + "\n" + // torn, and ended by the next write
		`{"entry":"","time":"2026-11-10T09:00:00.9Z","value":"no run named"}` + "\n" +
		`{"id":"b2","argv":["other"],"started":"2026-11-10T09:00:01Z","code":0,"changes":[]}` + "\n" +
		`{"id":"a1","argv":["app"],"started":"2026-11-10T09:00:00Z","finished":"2026-11-10T09:00:01Z","code":0,"changes":[]}` + "\n" +
		`{"entry":"a1","time":"2026-11-10T09:00:02Z","value":"late"}` + "\n" +
		`{"entry":"b2","time":"2026-11-10T09:00:03Z","value":"to`
	runs, skipped, err := readFrom(t.Context(), strings.NewReader(in), 0)
	if err != nil || len(runs) != 2 || skipped != 7 {
		t.Fatalf("read %d runs, %d skipped, %v, want 2 runs and 7 skipped", len(runs), skipped, err)
	}
	if got := values(t, runs[0].Entries); !reflect.DeepEqual(got, []string{"early", "between", "late"}) || runs[0].Interrupted() {
		t.Errorf("the first run's entries are %q, want early, between and late on a finished run", got)
	}
	if runs[1].ID != "b2" || runs[1].Entries != nil {
		t.Errorf("the second run is %+v, want b2 with no entry", runs[1])
	}
}

// Lines writes each kept run's record line and then its entry lines, so a compacted log reads back as the
// same runs with the same entries, writes the same bytes again, and holds no entry of a run it dropped.
//
// What it costs when this fails: the first compaction loses every entry, or writes them into the record line
// and brings back the cost the entry line removed.
func TestLinesRoundTripsARunWithItsEntries(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	a := begin(t, l, []string{"a"}, map[string]any{"schema": 1})
	b := begin(t, l, []string{"b"}, nil)
	c := begin(t, l, []string{"c"}, nil)
	a.Entry("a one")
	c.Entry(map[string]int{"n": 3})
	a.Change("write /etc/a")
	a.Entry("a two")
	if err := errors.Join(a.Finish(0, "done", ""), b.Finish(0, "done", ""), c.Err(), l.Entry(t.Context(), "nobody", "orphan")); err != nil {
		t.Fatal(err)
	}
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 3 || len(runs[0].Entries) != 2 || len(runs[1].Entries) != 0 || len(runs[2].Entries) != 1 {
		t.Fatalf("read %+v, %v", runs, err)
	}
	lines, dropped, err := Lines(runs, nil)
	if err != nil || dropped != nil {
		t.Fatalf("Lines: %v, dropped %+v", err, dropped)
	}
	var shape []string
	for line := range strings.Lines(string(lines)) {
		shape = append(shape, line[:strings.Index(line, ",")])
	}
	if want := []string{`{"id":"` + a.ID() + `"`, `{"entry":"` + a.ID() + `"`, `{"entry":"` + a.ID() + `"`, `{"id":"` + b.ID() + `"`, `{"id":"` + c.ID() + `"`, `{"entry":"` + c.ID() + `"`}; !reflect.DeepEqual(shape, want) {
		t.Errorf("the lines begin %q, want %q", shape, want)
	}
	back, skipped, err := readFrom(t.Context(), bytes.NewReader(lines), 0)
	if err != nil || skipped != 0 || !reflect.DeepEqual(back, runs) {
		t.Errorf("the lines read back as %+v, %d skipped, %v, want %+v", back, skipped, err, runs)
	}
	if again, _, err := Lines(back, nil); err != nil || !bytes.Equal(again, lines) {
		t.Errorf("the lines written again differ (%v):\n%s\n%s", err, again, lines)
	}
	kept, dropped, err := Lines(runs, func(r Record) bool { return r.ID != a.ID() })
	if err != nil || len(dropped) != 1 || len(dropped[0].Entries) != 2 || bytes.Contains(kept, []byte(a.ID())) || bytes.Count(kept, []byte("\n")) != 3 {
		t.Errorf("Lines without the first run: %v, dropped %+v, kept\n%s", err, dropped, kept)
	}
}

// An entry that cannot be written says why and writes nothing: a Log with no Path, an id of "", a value that
// does not encode, and from a Handle the failure is Err. A nil Handle's Entry does nothing.
//
// What it costs when this fails: an entry is written for no run, where no reader finds it, or a nil Handle,
// which a run that records nothing holds, stops the program.
func TestAnEntryThatCannotBeWrittenSaysWhyAndWritesNothing(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	h := begin(t, l, []string{"app"}, nil)
	before := fileOf(t, l)
	if err := (Log{}).Entry(t.Context(), h.ID(), 1); !errors.Is(err, ErrNoPath) {
		t.Errorf("a Log with no Path: %v, want ErrNoPath", err)
	}
	if err := l.Entry(t.Context(), "", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("an id of \"\": %v, want ErrNotFound", err)
	}
	if err := l.Entry(t.Context(), h.ID(), make(chan int)); err == nil {
		t.Error("a value that does not encode was written")
	}
	h.Entry(make(chan int))
	if h.Err() == nil || fileOf(t, l) != before {
		t.Errorf("Err is %v after an entry that does not encode, and the file is\n%s", h.Err(), fileOf(t, l))
	}
	var none *Handle
	none.Entry("nothing")
	if none.Err() != nil || fileOf(t, l) != before {
		t.Errorf("a nil Handle's Entry: %v, and the file is\n%s", none.Err(), fileOf(t, l))
	}
}

// A second process holding the log makes an entry fail after the bound, not hang, with an error matching
// context.DeadlineExceeded: in Err from a Handle, returned from a Log by its ctx. When it lets go the next lands.
//
// What it costs when this fails: a program whose log another process holds hangs in an entry, halfway
// through the change it is recording.
func TestAHeldLogFailsAnEntryAfterTheBound(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	l.bound = 200 * time.Millisecond
	h := begin(t, l, []string{"one"}, nil)
	letGo := holdLogInAnotherProcess(t, l.Path)
	h.Entry("held")
	if err := h.Err(); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Err is %v, want an error matching context.DeadlineExceeded", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), l.bound)
	defer cancel()
	if err := l.Entry(ctx, h.ID(), "held"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Log.Entry is %v, want an error matching context.DeadlineExceeded", err)
	}
	letGo()
	if err := l.Entry(t.Context(), h.ID(), "free"); err != nil {
		t.Fatalf("an entry after the hold was let go failed: %v", err)
	}
	runs, err := l.Read(t.Context())
	if got := values(t, runs[0].Entries); err != nil || !reflect.DeepEqual(got, []string{"free"}) {
		t.Errorf("read back %q, %v, want the one entry written after the hold", got, err)
	}
}

// An entry line that names a run and has no time key, or no value key, is not an entry: it is skipped and
// counted, as a record line with no id is. A value of null is a value, so Entry(nil) folds.
//
// What it costs when this fails: a line another program wrote with an entry member reads back as an entry at
// the year 1 whose value cannot be decoded, and the next compaction writes it as one.
func TestAnEntryLineWithNoTimeOrNoValueIsSkippedAndANullValueFolds(t *testing.T) {
	t.Parallel()
	const in = `{"id":"a1","argv":["app"],"started":"2026-11-10T09:00:00Z","code":0,"changes":[]}` + "\n" +
		`{"entry":"a1"}` + "\n" +
		`{"entry":"a1","value":"no time"}` + "\n" +
		`{"entry":"a1","time":"2026-11-10T09:00:01Z"}` + "\n" +
		`{"entry":"a1","time":null,"value":"a null time"}` + "\n" +
		`{"entry":"a1","time":"2026-11-10T09:00:02Z","value":null}` + "\n"
	runs, skipped, err := readFrom(t.Context(), strings.NewReader(in), 0)
	if err != nil || len(runs) != 1 || skipped != 4 {
		t.Fatalf("read %d runs, %d skipped, %v, want 1 run and 4 skipped", len(runs), skipped, err)
	}
	if e := runs[0].Entries; len(e) != 1 || string(e[0].Value) != "null" || e[0].Time.Second() != 2 {
		t.Errorf("the entries are %+v, want the one whose value is null", e)
	}
	l := fixedLog(t)
	h := begin(t, l, []string{"app"}, nil)
	h.Entry(nil)
	r, err := l.Find(t.Context(), h.ID())
	if err := errors.Join(err, h.Err()); err != nil || len(r.Entries) != 1 || string(r.Entries[0].Value) != "null" {
		t.Errorf("an entry of nil reads back as %+v (%v), want one entry whose value is null", r.Entries, err)
	}
}

// A Begin whose first line is longer than MaxLine, on a Log with no Root whose folder is not there, is
// ErrLineTooLong and makes no folder: a refusal for length leaves the disk as it was.
//
// What it costs when this fails: a refused run leaves an empty folder behind, with the mode of a log's folder,
// in a place the program never wrote to.
func TestABeginRefusedForLengthMakesNoFolder(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	l := Log{Path: filepath.Join(dir, "runs.jsonl"), MaxLine: 64}
	if _, err := l.Begin([]string{strings.Repeat("a", 64)}, nil); !errors.Is(err, ErrLineTooLong) {
		t.Errorf("Begin: %v, want ErrLineTooLong", err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the folder after a Begin refused for length: %v, want none", err)
	}
	l.MaxLine = 0
	if _, err := l.Begin([]string{strings.Repeat("a", 64)}, nil); err != nil {
		t.Errorf("the same Begin under the default MaxLine: %v", err)
	}
}
