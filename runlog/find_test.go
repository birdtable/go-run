package runlog

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

// threeRuns is a log of three runs written over forty lines, the second left interrupted, and the
// runs as Read reads them.
func threeRuns(t *testing.T, l Log) []Record {
	t.Helper()
	lines := 0
	for i := range 3 {
		h := begin(t, l, []string{"app", strconv.Itoa(i)}, nil)
		lines++
		for n := range 12 - i%2 {
			h.Change("change " + strconv.Itoa(n))
			lines++
		}
		if i != 1 {
			if err := h.Finish(0, "done", ""); err != nil {
				t.Fatal(err)
			}
			lines++
		}
	}
	if lines != 40 {
		t.Fatalf("wrote %d lines, want 40", lines)
	}
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 3 {
		t.Fatalf("%v, %d runs", err, len(runs))
	}
	return runs
}

// Find is the run whose id is given, or the one whose id starts with what is given; an id no run has
// is ErrNotFound, and a prefix two runs share is ErrAmbiguous.
//
// What it costs when this fails: a person who types the start of an id gets another run, or a run
// that does not exist reads as an empty one.
func TestFindTakesAnIDOrAPrefixOfOne(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	ids := []string{"aaaa0001", "aaaa0002", "bbbb0001"}
	for _, id := range ids {
		l.NewID = func() string { return id }
		begin(t, l, []string{"app"}, nil)
	}
	l.NewID = nil
	ran := 0
	for prefix, want := range map[string]string{"aaaa0002": "aaaa0002", "bbbb": "bbbb0001", "b": "bbbb0001", "aaaa0001": "aaaa0001"} {
		ran++
		if r, err := l.Find(t.Context(), prefix); err != nil || r.ID != want {
			t.Errorf("Find %q: %q, %v, want %q", prefix, r.ID, err, want)
		}
	}
	for prefix, want := range map[string]error{"cccc": ErrNotFound, "": ErrNotFound, "aaaa": ErrAmbiguous, "a": ErrAmbiguous} {
		ran++
		if r, err := l.Find(t.Context(), prefix); !errors.Is(err, want) || r.ID != "" {
			t.Errorf("Find %q: %q, %v, want %v", prefix, r.ID, err, want)
		}
	}
	if ran != 8 {
		t.Fatalf("ran %d cases, want 8", ran)
	}
}

// Lines is the pure half of compaction. A log of three runs written over forty lines folds into three
// lines that read back equal, with a nil keep; a keep that rejects one returns it and leaves its line
// out. For any keep, kept plus dropped is the input, the order is kept, and every kept line parses back
// to its record.
//
// What it costs when this fails: a log that only grows is read whole at every run, or compaction loses
// a run, or a dropped run's kept copies are never known to the caller who removes them.
func TestLinesKeepsDropsAndKeepsTheOrderOfEveryRun(t *testing.T) {
	t.Parallel()
	runs := threeRuns(t, logIn(t))
	lines, dropped, err := Lines(runs, nil)
	back, skipped, rerr := readFrom(t.Context(), bytes.NewReader(lines), 0)
	if err != nil || dropped != nil || rerr != nil || skipped != 0 || !reflect.DeepEqual(back, runs) || bytes.Count(lines, []byte("\n")) != 3 {
		t.Fatalf("Lines keeping all: %v, dropped %+v, read back %d runs, %d skipped (%v), %d lines", err, dropped, len(back), skipped, rerr, bytes.Count(lines, []byte("\n")))
	}
	ran := 0
	for mask := range 1 << len(runs) {
		ran++
		keep := func(r Record) bool {
			return mask>>slices.IndexFunc(runs, func(o Record) bool { return o.ID == r.ID })&1 == 1
		}
		lines, dropped, err := Lines(runs, keep)
		back, _, rerr := readFrom(t.Context(), bytes.NewReader(lines), 0)
		var kept []Record
		for _, r := range runs {
			if keep(r) {
				kept = append(kept, r)
			}
		}
		if err != nil || rerr != nil || len(kept)+len(dropped) != len(runs) || (len(kept) > 0 && !reflect.DeepEqual(back, kept)) || len(back) != len(kept) {
			t.Errorf("keep %03b: %v, %v, kept %d, dropped %d, read back %d", mask, err, rerr, len(kept), len(dropped), len(back))
		}
	}
	if ran != 8 {
		t.Fatalf("tried %d keeps, want 8", ran)
	}
	if lines, dropped, err := Lines(nil, nil); lines != nil || dropped != nil || err != nil {
		t.Errorf("Lines of no runs: %q, %+v, %v", lines, dropped, err)
	}
}

// A MaxLine below zero is the default, as zero is: Read reads every run, and Lines of what it read keeps
// every run, where a negative cap took every line for too long and compacted the log to nothing.
//
// What it costs when this fails: a caller that sets MaxLine from a flag or a sum that comes out
// negative empties its log at the first compaction, and every way back with it.
func TestANegativeMaxLineIsTheDefaultAndLinesKeepsEveryRun(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	runs := threeRuns(t, l)
	ran := 0
	for _, maxLine := range []int{-1, -4096} {
		ran++
		l.MaxLine = maxLine
		read, err := l.Read(t.Context())
		if err != nil || !reflect.DeepEqual(read, runs) {
			t.Errorf("MaxLine %d: Read gives %d runs (%v), want the %d written", maxLine, len(read), err, len(runs))
		}
		if lines, dropped, err := Lines(read, nil); err != nil || dropped != nil || bytes.Count(lines, []byte("\n")) != len(runs) {
			t.Errorf("MaxLine %d: Lines: %d lines, dropped %+v, %v", maxLine, bytes.Count(lines, []byte("\n")), dropped, err)
		}
	}
	if ran != 2 {
		t.Fatalf("tried %d caps, want 2", ran)
	}
}
