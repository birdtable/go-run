package runlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A log with no path is ErrNoPath from Begin and from Read, and nothing is written anywhere.
//
// What it costs when this fails: a program that forgot its log's path changes the machine with no
// record, or writes one into whatever folder it was started in.
func TestALogWithNoPathIsErrNoPath(t *testing.T) {
	t.Parallel()
	h, err := Log{}.Begin([]string{"app"}, nil)
	if !errors.Is(err, ErrNoPath) || h != nil {
		t.Errorf("Begin with no path: %v, %v", h, err)
	}
	if runs, err := (Log{}).Read(t.Context()); !errors.Is(err, ErrNoPath) || runs != nil {
		t.Errorf("Read with no path: %v, %v", runs, err)
	}
}

// Begin on a log that cannot be written returns the error and no Handle, and writes nothing, so a
// caller can decide to change nothing; a later line that cannot be written is Err, and Finish
// returns it.
//
// What it costs when this fails: a program changes the machine believing its record was opened,
// and the person who looks later finds no record of what the run changed.
func TestALogThatCannotBeWrittenFailsBeginAndALaterLineIsErr(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := Log{Path: filepath.Join(file, "runs.jsonl")}.Begin([]string{"app"}, nil)
	if err == nil || h != nil {
		t.Errorf("a log under a file opened: %v, %v", h, err)
	}
	if h, err := (Log{Path: t.TempDir()}).Begin([]string{"app"}, nil); err == nil || h != nil {
		t.Errorf("a log whose path is a folder opened: %v, %v", h, err)
	}
	l := logIn(t)
	h = begin(t, l, []string{"app"}, nil)
	if err := os.Remove(l.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(l.Path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(l.Path), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.Change("x")
	if err := h.Finish(0, "done", ""); err == nil || h.Err() == nil {
		t.Errorf("a line under a file was written: %v", err)
	}
}

// A fixed clock and id make the same line every time, its times in UTC.
//
// What it costs when this fails: a program's tests cannot hold a record's line, and a record's
// times read in whatever zone the program ran in.
func TestAFixedClockAndIDMakeTheSameLine(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("east", 3*3600)
	lines := map[string]bool{}
	for range 2 {
		l := logIn(t)
		l.Now = func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, zone) }
		l.NewID = func() string { return "fixed" }
		h := begin(t, l, []string{"app"}, nil)
		if err := h.Finish(0, "done", "ok"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(l.Path)
		if err != nil {
			t.Fatal(err)
		}
		lines[string(b)] = true
	}
	want := `{"id":"fixed","argv":["app"],"started":"2026-11-13T09:00:00Z","code":0,"changes":[]}` + "\n" +
		`{"id":"fixed","argv":["app"],"started":"2026-11-13T09:00:00Z","finished":"2026-11-13T09:00:00Z","code":0,"outcome":"done","summary":"ok","changes":[]}` + "\n"
	if len(lines) != 1 || !lines[want] {
		t.Errorf("the lines are %v, want %q", lines, want)
	}
}

// An id is 16 hex characters when the log names no NewID, and two runs have two.
//
// What it costs when this fails: two runs of one log share an id, and Read merges them into one.
func TestAnIDIsSixteenHexCharacters(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	a, b := begin(t, l, []string{"app"}, nil).ID(), begin(t, l, []string{"app"}, nil).ID()
	for _, id := range []string{a, b} {
		if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
			t.Errorf("the id %q is not 16 hex characters", id)
		}
	}
	if a == b {
		t.Errorf("two runs have the id %s", a)
	}
}

// A line longer than the reader's MaxLine, from a writer with a larger one, is skipped, as a torn one is: every
// other run reads, and the run's line before it stands.
//
// What it costs when this fails: one long run makes every run in the log unreadable, and with them
// every way back.
func TestALineLongerThanMaxLineIsSkipped(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	long := begin(t, l, []string{"app"}, nil)
	long.Change("short")
	long.Change(strings.Repeat("x", 300))
	other := begin(t, l, []string{"other"}, nil)
	if err := errors.Join(long.Err(), other.Finish(0, "done", "")); err != nil {
		t.Fatal(err)
	}
	l.MaxLine = 200
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 2 {
		t.Fatalf("%v, %d runs", err, len(runs))
	}
	if runs[0].ID != long.ID() || len(runs[0].Changes) != 1 || runs[1].ID != other.ID() || runs[1].Interrupted() {
		t.Errorf("read back %+v", runs)
	}
	l.MaxLine = 0
	if runs, err := l.Read(t.Context()); err != nil || len(runs[0].Changes) != 2 {
		t.Errorf("with the default cap the long line reads: %v, %+v", err, runs)
	}
}

// A context that is done stops Read and ReadFrom with its error.
//
// What it costs when this fails: a program that reads a large log on a deadline cannot stop it.
func TestADoneContextStopsRead(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	begin(t, l, []string{"app"}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if runs, err := l.Read(ctx); !errors.Is(err, context.Canceled) || runs != nil {
		t.Errorf("Read on a done context: %v, %v", runs, err)
	}
	if runs, err := ReadFrom(ctx, strings.NewReader("{}\n"), 0); !errors.Is(err, context.Canceled) || runs != nil {
		t.Errorf("ReadFrom on a done context: %v, %v", runs, err)
	}
}

// ReadFrom reads a log from any reader as Read reads its file, a CRLF line and a last line with no
// newline among it.
//
// What it costs when this fails: a log read from a pipe or an archive reads otherwise than the same
// log on disk.
func TestReadFromReadsAnyReader(t *testing.T) {
	t.Parallel()
	in := `{"version":3,"id":"a","argv":["app"],"started":"2026-11-13T09:00:00Z","code":0,"changes":[]}` + "\r\n" +
		"not a record\n" +
		`{"version":3,"id":"b","argv":["app"],"started":"2026-11-13T09:00:01Z","code":0,"changes":["x"]}`
	runs, skipped, err := readFrom(t.Context(), strings.NewReader(in), 0)
	if err != nil || skipped != 1 || len(runs) != 2 || runs[0].ID != "a" || runs[1].ID != "b" || len(runs[1].Changes) != 1 {
		t.Errorf("read %+v, %v", runs, err)
	}
}

// A Created that fails leaves no log, and Begin returns its error; one that works is called once, at
// the line that makes the log.
//
// What it costs when this fails: a log a program could not give to its group is left for its writer
// alone, unsaid, or a log an administrator changed is changed back at every line.
func TestCreatedIsCalledOnceAndAFailureLeavesNoLog(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	refused := errors.New("refused")
	l.Created = func(*os.File) error { return refused }
	if h, err := l.Begin([]string{"app"}, nil); !errors.Is(err, refused) || h != nil {
		t.Errorf("a failing Created: %v, %v", h, err)
	}
	if _, err := os.Stat(l.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failing Created left the log: %v", err)
	}
	calls := 0
	l.Created = func(*os.File) error { calls++; return nil }
	h := begin(t, l, []string{"app"}, nil)
	h.Change("x")
	if err := h.Finish(0, "done", ""); err != nil || calls != 1 {
		t.Errorf("Created was called %d times (%v)", calls, err)
	}
}

// With Sync every line is written and read back as without it.
//
// What it costs when this fails: a program that asks for a durable log loses its record.
func TestASyncedLogReadsBack(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	l.Sync = true
	h := begin(t, l, []string{"app"}, nil)
	h.Change("x")
	if err := h.Finish(0, "done", ""); err != nil {
		t.Fatal(err)
	}
	if runs, err := l.Read(t.Context()); err != nil || len(runs) != 1 || len(runs[0].Changes) != 1 {
		t.Errorf("read back %+v, %v", runs, err)
	}
}

// A line of exactly MaxLine bytes reads, with or without a CRLF ending, and one a byte longer is
// skipped, wherever ReadFrom's reads split it.
//
// What it costs when this fails: the cap a caller set reads a line it said to skip, or skips one it
// said to read.
func TestALineOfMaxLineReadsAndOneByteMoreIsSkipped(t *testing.T) {
	t.Parallel()
	line := func(id string, n int) string {
		head := `{"version":3,"id":"` + id + `","changes":["`
		tail := `"]}`
		return head + strings.Repeat("x", n-len(head)-len(tail)) + tail
	}
	const maxLine = 5000 // past bufio's 4096, so a line takes two reads
	in := line("at", maxLine) + "\n" + line("crlf", maxLine) + "\r\n" + line("over", maxLine+1) + "\n" + line("last", maxLine)
	runs, skipped, err := readFrom(t.Context(), strings.NewReader(in), maxLine)
	var ids []string
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	if err != nil || skipped != 1 || strings.Join(ids, " ") != "at crlf last" {
		t.Errorf("read %q, %d skipped, %v, want at, crlf and last and one skipped: the smallest line past the cap", ids, skipped, err)
	}
}

// A line with a name twice is skipped and counted, at the top of the line and inside a caller's own
// field, and the run's line before it stands: the reader does not take the last of two, or the first.
//
// What it costs when this fails: a line that says two things for one name is read as one of them, and
// an undo acts on whichever a parser happened to keep.
func TestALineWithANameTwiceIsSkippedAndCounted(t *testing.T) {
	t.Parallel()
	const head = `{"version":3,"id":"a","argv":["app"],"started":"2026-11-14T09:00:00Z","code":0,"changes":[]`
	in := head + "}\n" + head + `,"code":1}` + "\n" + head + `,"extra":{"k":1,"k":2}}` + "\n"
	runs, skipped, err := readFrom(t.Context(), strings.NewReader(in), 0)
	if err != nil || skipped != 2 || len(runs) != 1 || runs[0].Code != 0 || len(runs[0].Extra) != 1 || string(runs[0].Extra["version"]) != "3" {
		t.Errorf("read %+v, %d skipped, %v: want the first line only and two skipped", runs, skipped, err)
	}
}
