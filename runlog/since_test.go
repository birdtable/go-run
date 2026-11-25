package runlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// What it costs when this fails: a record or entry that Read hands back changes under the caller as the read
// goes on, because it is a slice of the buffer the next line is read into.
func TestARecordAndAnEntryHandedBackAreNotTheReadBuffer(t *testing.T) {
	t.Parallel()
	var log bytes.Buffer
	for i := range 400 {
		fmt.Fprintf(&log, `{"id":"r%03d","argv":["x"],"started":"2026-11-25T10:00:00Z","code":0,"changes":[],"mine":{"n":%d},"extra":{"k":"v%03d"}}`+"\n", i, i, i)
		fmt.Fprintf(&log, `{"entry":"r%03d","time":"2026-11-25T10:00:01Z","value":{"step":"s%03d"}}`+"\n", i, i)
	}
	runs, err := ReadFrom(context.Background(), &log, 0)
	if err != nil || len(runs) != 400 {
		t.Fatalf("%d runs, %v", len(runs), err)
	}
	for i, r := range runs {
		var mine struct {
			N int `json:"n"`
		}
		if ok, err := r.Field("mine", &mine); err != nil || !ok || mine.N != i {
			t.Fatalf("run %d: mine = %v, %v, %v", i, mine, ok, err)
		}
		var k string
		if ok, err := r.Field("k", &k); err != nil || !ok || k != fmt.Sprintf("v%03d", i) {
			t.Fatalf("run %d: k = %q, %v, %v", i, k, ok, err)
		}
		if len(r.Entries) != 1 || string(r.Entries[0].Value) != fmt.Sprintf(`{"step":"s%03d"}`, i) {
			t.Fatalf("run %d: entries %q", i, r.Entries)
		}
	}
}

// A line longer than the reader's buffer, and one longer than MaxLine around it, read as they did.
func TestALongLineAndALineBeyondMaxLineAreReadAsBefore(t *testing.T) {
	t.Parallel()
	long := `{"id":"big","argv":["` + strings.Repeat("a", 20000) + `"],"started":"2026-11-25T10:00:00Z","code":0,"changes":[]}`
	over := `{"id":"over","argv":["` + strings.Repeat("b", 30000) + `"],"started":"2026-11-25T10:00:00Z","code":0,"changes":[]}`
	in := strings.Join([]string{long, over, `{"id":"small","argv":["c"],"started":"2026-11-25T10:00:00Z","code":0,"changes":[]}`}, "\r\n") + "\r\n"
	runs, skipped, err := readFrom(context.Background(), strings.NewReader(in), 25000)
	if err != nil || skipped != 1 || len(runs) != 2 || runs[0].ID != "big" || runs[1].ID != "small" {
		t.Fatalf("%v, skipped %d, %v", runs, skipped, err)
	}
}

// ReadSince from the offset it handed back reads only what came after, for a Log by Path and beneath a Root.
func TestReadSinceReadsOnlyWhatCameAfterTheOffset(t *testing.T) {
	t.Parallel()
	for name, mk := range map[string]func(t *testing.T) (Log, string){"path": pathLogAndFile, "root": rootLogAndFile} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, file := mk(t)
			ctx := context.Background()
			if runs, next, err := l.ReadSince(ctx, 0); err != nil || len(runs) != 0 || next != 0 {
				t.Fatalf("no log: %v, %d, %v", runs, next, err)
			}
			first := begin(t, l, []string{"one"}, nil)
			if err := first.Finish(0, "done", "one"); err != nil {
				t.Fatal(err)
			}
			runs, next, err := l.ReadSince(ctx, 0)
			st, _ := os.Stat(file)
			if err != nil || len(runs) != 1 || next != st.Size() {
				t.Fatalf("whole: %v, next %d of %d, %v", runs, next, st.Size(), err)
			}
			if again, n2, err := l.ReadSince(ctx, next); err != nil || len(again) != 0 || n2 != next {
				t.Fatalf("nothing new: %v, %d, %v", again, n2, err)
			}
			second := begin(t, l, []string{"two"}, nil)
			if err := second.Finish(0, "done", "two"); err != nil {
				t.Fatal(err)
			}
			runs, next2, err := l.ReadSince(ctx, next)
			st, _ = os.Stat(file)
			if err != nil || len(runs) != 1 || runs[0].Argv[0] != "two" || next2 != st.Size() {
				t.Fatalf("since: %v, next %d of %d, %v", runs, next2, st.Size(), err)
			}
		})
	}
}

// An offset inside a line, or past the end, is ErrBadOffset and reads nothing; a last line with no newline is
// read but not counted, so the next call reads it whole.
func TestReadSinceRefusesAnOffsetThatIsNotALineStart(t *testing.T) {
	t.Parallel()
	l, file := pathLogAndFile(t)
	h := begin(t, l, []string{"one"}, nil)
	if err := h.Finish(0, "done", "one"); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(file)
	for _, off := range []int64{1, st.Size() - 1, st.Size() + 1, -1} {
		if runs, next, err := l.ReadSince(context.Background(), off); !errors.Is(err, ErrBadOffset) || runs != nil || next != off {
			t.Errorf("offset %d: %v, %d, %v", off, runs, next, err)
		}
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"tail","argv":["t"],"started":"2026-11-25T10:00:00Z","code":0,"changes":[]}`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	runs, next, err := l.ReadSince(context.Background(), st.Size())
	if err != nil || len(runs) != 1 || runs[0].ID != "tail" || next != st.Size() {
		t.Fatalf("a line with no newline: %v, %d, %v", runs, next, err)
	}
}

// The read of the offset of the last run reads less than the whole file.
func TestReadSinceTheLastRunReadsLessThanTheWholeFile(t *testing.T) {
	t.Parallel()
	l, last := bigLog(t, 2<<20)
	st, _ := os.Stat(l.Path)
	runs, next, err := l.ReadSince(context.Background(), last)
	if err != nil || len(runs) != 1 || next != st.Size() || st.Size()-last > 2048 {
		t.Fatalf("%d runs, next %d of %d from %d, %v", len(runs), next, st.Size(), last, err)
	}
}
