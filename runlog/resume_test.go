package runlog

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// What it costs when this fails: a guard that closes a run another process began writes a line the
// run's own reader never sees, or no line at all, and the run stays interrupted for ever.
func TestAnotherProcessResumesARunAndItsLinesLand(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	h := begin(t, l, []string{"app", "tune"}, nil)
	h.Change("write a")
	other := Log{Path: l.Path} // a second process's view
	r, err := other.Resume(t.Context(), h.ID())
	if err != nil {
		t.Fatal(err)
	}
	r.Change("write b")
	r.Set("how", []string{"undo b"})
	if err := r.Finish(3, "stopped", "the guard closed it"); err != nil {
		t.Fatal(err)
	}
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 1 {
		t.Fatalf("%v, %d runs", err, len(runs))
	}
	got := runs[0]
	var how []string
	if ok, err := got.Field("how", &how); !ok || err != nil || len(how) != 1 {
		t.Errorf("the field read back as %v (%v, %v)", how, ok, err)
	}
	if got.Interrupted() || got.Code != 3 || got.Outcome != "stopped" || strings.Join(got.Changes, ",") != "write a,write b" {
		t.Errorf("read back %+v", got)
	}
}

// What it costs when this fails: a program that resumes a finished run reads its first close as
// still standing, or loses what the run recorded before it.
func TestAFinishedRunResumedAndFinishedAgainReadsAsTheLastClose(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	n := 0
	l.Now = func() time.Time { n++; return time.Date(2026, 11, 22, 12, n, 0, 0, time.UTC) }
	h := begin(t, l, []string{"app"}, map[string]any{"build": "x"})
	h.Change("write a")
	if err := h.Finish(0, "done", "first"); err != nil {
		t.Fatal(err)
	}
	first, _ := l.Read(t.Context())
	r, err := l.Resume(t.Context(), h.ID())
	if err != nil {
		t.Fatal(err)
	}
	r.Change("write b")
	mid, _ := l.Read(t.Context())
	if mid[0].Finished != first[0].Finished || mid[0].Outcome != "done" || len(mid[0].Changes) != 2 {
		t.Errorf("a line written before the second Finish changed the close: %+v", mid[0])
	}
	if err := r.Finish(1, "failed", "second"); err != nil {
		t.Fatal(err)
	}
	runs, _ := l.Read(t.Context())
	if len(runs) != 1 || runs[0].Code != 1 || runs[0].Outcome != "failed" || runs[0].Summary != "second" || !runs[0].Finished.After(first[0].Finished) {
		t.Errorf("the last close did not stand: %+v", runs)
	}
	var build string
	if ok, _ := runs[0].Field("build", &build); !ok || build != "x" {
		t.Errorf("the field of Begin was lost: %q", build)
	}
}

// What it costs when this fails: a caller that holds a prefix, or a typo, resumes the wrong run.
func TestResumeTakesTheWholeIDAndNothingElse(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	h := begin(t, l, []string{"app"}, nil)
	for _, id := range []string{h.ID()[:6], h.ID() + "0", ""} {
		r, err := l.Resume(t.Context(), id)
		if !errors.Is(err, ErrNotFound) || r != nil {
			t.Errorf("%q: %v, %v", id, r, err)
		}
	}
	if _, err := (Log{}).Resume(t.Context(), h.ID()); !errors.Is(err, ErrNoPath) {
		t.Errorf("an unset path: %v", err)
	}
	if r, err := (Log{Path: l.Path + ".none"}).Resume(t.Context(), h.ID()); !errors.Is(err, ErrNotFound) || r != nil {
		t.Errorf("a log that is not there: %v, %v", r, err)
	}
}

// What it costs when this fails: two processes writing one run tear each other's lines, and Read
// skips the run's close.
func TestTwoHandlesOnOneRunWriteWholeLinesOnly(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	h := begin(t, l, []string{"app"}, nil)
	r, err := l.Resume(t.Context(), h.ID())
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 6000)
	var wg sync.WaitGroup
	for _, x := range []*Handle{h, r} {
		wg.Go(func() {
			for range 40 {
				x.Change(long)
			}
		})
	}
	wg.Wait()
	b, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
	if len(lines) != 1+80 {
		t.Fatalf("%d lines", len(lines))
	}
	var skipped int
	l.Skipped = &skipped
	runs, err := l.Read(t.Context())
	if err != nil || skipped != 0 || len(runs) != 1 {
		t.Errorf("%v, %d skipped, %d runs", err, skipped, len(runs))
	}
}
