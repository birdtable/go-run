package runlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// What it costs when this fails: a log read follows a link planted at its name to another file in the same
// folder, or waits on a pipe, where the write beside it refuses both.
func TestAReadOfAFolderAtTheLogsNameIsErrNotPlain(t *testing.T) {
	t.Parallel()
	for name, mk := range map[string]func(t *testing.T) (Log, string){"path": pathLogAndFile, "root": rootLogAndFile} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, file := mk(t)
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(file, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := l.Read(context.Background()); !errors.Is(err, ErrNotPlain) {
				t.Fatalf("Read of a folder: %v, want ErrNotPlain", err)
			}
		})
	}
}

// A missing log is still no runs, and a plain one still reads.
func TestAReadOfAPlainOrMissingLogIsAsBefore(t *testing.T) {
	t.Parallel()
	for name, mk := range map[string]func(t *testing.T) (Log, string){"path": pathLogAndFile, "root": rootLogAndFile} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, file := mk(t)
			if runs, err := l.Read(context.Background()); err != nil || len(runs) != 0 {
				t.Fatalf("Read of no log: %v, %v", runs, err)
			}
			h := begin(t, l, []string{"one"}, nil)
			if err := h.Finish(0, "done", "one"); err != nil {
				t.Fatal(err)
			}
			if runs, err := l.Read(context.Background()); err != nil || len(runs) != 1 {
				t.Fatalf("Read of %s: %v, %v", filepath.Base(file), runs, err)
			}
		})
	}
}
