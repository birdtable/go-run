//go:build windows

package runlog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A mode the program sets for the log or its folder is refused on Windows,
// and one it does not set runs.
//
// What it costs when this fails: a program that names a mode for its log is
// told nothing while Windows ignores it, and believes its log is owner-only
// when it takes its folder's access list.
func TestPermAndDirPermAreRefusedOnWindows(t *testing.T) {
	t.Parallel()
	for _, set := range []struct {
		name string
		fn   func(*Log)
	}{
		{"Perm", func(l *Log) { l.Perm = 0o640 }},
		{"DirPerm", func(l *Log) { l.DirPerm = 0o750 }},
	} {
		l := Log{Path: filepath.Join(t.TempDir(), "state", "runs.jsonl")}
		set.fn(&l)
		h, err := l.Begin([]string{"app"}, nil)
		if h != nil || !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s set: got %v, %v; want an error matching errors.ErrUnsupported", set.name, h, err)
		}
		if _, err := os.Stat(filepath.Dir(l.Path)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s set: the refused Begin made the folder (%v)", set.name, err)
		}
	}
	l := Log{Path: filepath.Join(t.TempDir(), "state", "runs.jsonl")}
	begin(t, l, []string{"app"}, nil).Change("x")
	if _, err := os.Stat(l.Path); err != nil {
		t.Errorf("no mode set: the log was not made: %v", err)
	}
}
