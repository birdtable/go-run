package runlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// rootLog is a Log kept beneath a Root the test holds, over a folder of its own.
func rootLog(t *testing.T) (Log, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return Log{Root: root, Path: "runs.jsonl"}, dir
}

// pathLogAndFile is a Log by its Path, and the file.
func pathLogAndFile(t *testing.T) (Log, string) {
	t.Helper()
	l := logIn(t)
	return l, l.Path
}

// rootLogAndFile is a Log beneath a Root, and the file.
func rootLogAndFile(t *testing.T) (Log, string) {
	t.Helper()
	l, dir := rootLog(t)
	return l, filepath.Join(dir, l.Path)
}

// What it costs when this fails: a crash that cut a line short makes the next run's first line join
// it, and both records are lost to Read, the new one included.
func TestATornLastLineIsEndedAndTheNextRecordIsKept(t *testing.T) {
	t.Parallel()
	for name, mk := range map[string]func(t *testing.T) (Log, string){"path": pathLogAndFile, "root": rootLogAndFile} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, file := mk(t)
			first := begin(t, l, []string{"one"}, nil)
			if err := first.Finish(0, "done", "one"); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(`{"id":"torn","argv":["cut`); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			second := begin(t, l, []string{"two"}, nil)
			var skipped int
			l.Skipped = &skipped
			runs, err := l.Read(t.Context())
			if err != nil || len(runs) != 2 || skipped != 1 {
				t.Fatalf("%v, %d runs, %d skipped", err, len(runs), skipped)
			}
			if runs[0].ID != first.ID() || runs[1].ID != second.ID() || !slices.Equal(runs[1].Argv, []string{"two"}) {
				t.Errorf("read back %+v", runs)
			}
		})
	}
}

// The same for a line written by Set on a resumed run.
//
// What it costs when this fails: the line a guard writes to close another process's run joins a
// torn line and is lost with it.
func TestATornLastLineIsEndedBeforeASetOnAResumedRun(t *testing.T) {
	t.Parallel()
	l, dir := rootLog(t)
	h := begin(t, l, []string{"one"}, nil)
	f, err := os.OpenFile(filepath.Join(dir, l.Path), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"torn`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := l.Resume(t.Context(), h.ID())
	if err != nil {
		t.Fatal(err)
	}
	r.Set("how", "undo")
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	var skipped int
	l.Skipped = &skipped
	runs, err := l.Read(t.Context())
	var how string
	if err != nil || len(runs) != 1 || skipped != 1 {
		t.Fatalf("%v, %d runs, %d skipped", err, len(runs), skipped)
	}
	if ok, err := runs[0].Field("how", &how); !ok || err != nil || how != "undo" {
		t.Errorf("the field read back as %q (%v, %v)", how, ok, err)
	}
}

// A Root taken, then a folder beneath it replaced by a link to another folder: the write is
// refused, Err says so, and nothing lands in the folder the link leads to.
//
// What it costs when this fails: anyone who can make a link in the log's folder sends a program's
// record of what it changed to a folder of their own.
func TestAFolderTurnedToALinkAfterTheRootCarriesNoWrite(t *testing.T) {
	t.Parallel()
	dir, elsewhere := t.TempDir(), t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if err := root.Mkdir("state", 0o700); err != nil {
		t.Fatal(err)
	}
	l := Log{Root: root, Path: filepath.Join("state", "runs.jsonl")}
	h := begin(t, l, []string{"one"}, nil)
	if err := os.Remove(filepath.Join(dir, "state", "runs.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "state")); err != nil {
		t.Fatal(err)
	}
	plantLink(t, elsewhere, filepath.Join(dir, "state"))
	h.Change("write a")
	if h.Err() == nil {
		t.Error("a write beneath a folder turned to a link was not refused")
	}
	if _, err := l.Begin([]string{"two"}, nil); err == nil {
		t.Error("a Begin beneath a folder turned to a link was not refused")
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Errorf("the folder the link leads to holds %v (%v)", entries, err)
	}
}

// A name that climbs out of the Root is refused by Begin, and nothing is made outside it.
//
// What it costs when this fails: a Path of "../x" in a Root Log writes outside the folder the program held.
func TestANameThatClimbsOutOfTheRootIsRefused(t *testing.T) {
	t.Parallel()
	l, dir := rootLog(t)
	l.Path = filepath.Join("..", filepath.Base(dir)+"-out.jsonl")
	if _, err := l.Begin([]string{"one"}, nil); err == nil {
		t.Error("a name that climbs out of the Root was taken")
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-out.jsonl")); err == nil {
		t.Error("a log was made outside the Root")
	}
}

// A Root Log and a path Log on one file read the same runs.
//
// What it costs when this fails: a program that holds the folder reads a different journal from the
// one a person reads by its path.
func TestARootLogAndAPathLogOnOneFileReadTheSame(t *testing.T) {
	t.Parallel()
	l, dir := rootLog(t)
	h := begin(t, l, []string{"one"}, nil)
	h.Change("write a")
	byPath := Log{Path: filepath.Join(dir, l.Path)}
	other := begin(t, byPath, []string{"two"}, nil)
	if err := other.Finish(0, "done", "two"); err != nil {
		t.Fatal(err)
	}
	viaRoot, err := l.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	viaPath, err := byPath.Read(t.Context())
	if err != nil || len(viaRoot) != 2 || len(viaPath) != 2 {
		t.Fatalf("%v, %d and %d runs", err, len(viaRoot), len(viaPath))
	}
	for i := range viaRoot {
		if viaRoot[i].ID != viaPath[i].ID || !slices.Equal(viaRoot[i].Changes, viaPath[i].Changes) || viaRoot[i].Code != viaPath[i].Code {
			t.Errorf("run %d reads %+v by the Root and %+v by its path", i, viaRoot[i], viaPath[i])
		}
	}
	if r, err := l.Find(t.Context(), other.ID()[:4]); err != nil || r.ID != other.ID() {
		t.Errorf("Find through the Root: %v, %v", r, err)
	}
}

// A second process holding the log makes a write fail after the bound, not hang, with an error
// that matches context.DeadlineExceeded, kept in Err; and when it lets go the next write lands.
//
// What it costs when this fails: a program whose log another process holds hangs in its first Change,
// halfway through the change it is recording.
func TestAHeldLogFailsAWriteAfterTheBound(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	l.bound = 200 * time.Millisecond
	h := begin(t, l, []string{"one"}, nil)
	letGo := holdLogInAnotherProcess(t, l.Path)
	start := time.Now()
	h.Change("write a")
	took := time.Since(start)
	if err := h.Err(); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Err is %v, want an error matching context.DeadlineExceeded", err)
	}
	if took < l.bound || took > 5*time.Second {
		t.Errorf("the write took %v for a bound of %v", took, l.bound)
	}
	letGo()
	r, err := l.Resume(t.Context(), h.ID())
	if err != nil {
		t.Fatal(err)
	}
	r.Change("write b")
	if err := r.Err(); err != nil {
		t.Errorf("a write after the hold was let go failed: %v", err)
	}
}
