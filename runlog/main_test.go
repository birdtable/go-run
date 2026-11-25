package runlog

import (
	"fmt"
	"os"
	"testing"

	"github.com/birdtable/go-testkit"
)

// TestMain starts the tests from an environment of their own: only the variables testkit keeps,
// and each folder variable pointed at a new folder under one it makes and removes.
func TestMain(m *testing.M) {
	if code, is := holdMain(); is {
		os.Exit(code)
	}
	code, err := testkit.Main(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err) //nolint:forbidigo // TestMain has no t to report to
	}
	os.Exit(code)
}

// What it costs when this fails: a test reads or writes the person's own home, temp folder or
// settings, or runs with an agent's or git's variables, and passes or fails on what the machine holds.
func TestTheTestsRunInAnEnvironmentOfTheirOwn(t *testing.T) {
	t.Parallel()
	testkit.Isolated(t)
}
