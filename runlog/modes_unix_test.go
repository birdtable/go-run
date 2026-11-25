//go:build unix

package runlog

import (
	"os"
	"path/filepath"
	"testing"
)

// The log is its writer's alone unless the program says otherwise, and the
// mode it names is the mode it gets.
//
// What it costs when this fails: another account on the machine reads a
// run's log, with every file the run changed and kept, or a program that
// names a mode for its log gets another one.
func TestTheLogIsItsWritersUnlessTheProgramSays(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		perm, want os.FileMode
	}{{0, 0o600}, {0o640, 0o640}} {
		l := logIn(t)
		l.Perm = c.perm
		begin(t, l, []string{"app", "serve"}, nil).Change("x")
		st, err := os.Stat(l.Path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != c.want {
			t.Errorf("Perm %v made the log %v", c.perm, st.Mode().Perm())
		}
		dir, _ := os.Stat(filepath.Dir(l.Path))
		if dir.Mode().Perm() != 0o700 {
			t.Errorf("the folder is %v", dir.Mode().Perm())
		}
	}
}
