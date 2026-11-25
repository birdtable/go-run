//go:build unix

package runlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A link at the log's name to another file in the same folder is refused, and that file's records are not read.
func TestAReadOfALinkAtTheLogsNameIsErrNotPlain(t *testing.T) {
	t.Parallel()
	for name, mk := range map[string]func(t *testing.T) (Log, string){"path": pathLogAndFile, "root": rootLogAndFile} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, file := mk(t)
			other := l
			other.Path = l.Path + ".other"
			if l.Root == nil {
				other.Path = file + ".other"
			}
			h := begin(t, other, []string{"theirs"}, nil)
			if err := h.Finish(0, "done", "theirs"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other.Path, file); err != nil {
				t.Fatal(err)
			}
			runs, err := l.Read(context.Background())
			if !errors.Is(err, ErrNotPlain) || len(runs) != 0 {
				t.Fatalf("Read through a link: %v, %v, want ErrNotPlain and no runs", runs, err)
			}
		})
	}
}

// A pipe at the log's name is refused at once, with no writer to wait for.
func TestAReadOfAPipeAtTheLogsNameReturnsAtOnceAsErrNotPlain(t *testing.T) {
	t.Parallel()
	for name, mk := range map[string]func(t *testing.T) (Log, string){"path": pathLogAndFile, "root": rootLogAndFile} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, file := mk(t)
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(file, 0o600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := l.Read(context.Background()); done <- err }()
			select {
			case err := <-done:
				if !errors.Is(err, ErrNotPlain) {
					t.Fatalf("Read of a pipe: %v, want ErrNotPlain", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Read of a pipe was still waiting for a writer after 5 s")
			}
		})
	}
}
