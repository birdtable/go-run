package runlog

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec" //nolint:depguard // cli.process.1: the test starts this test binary again as a second process that holds the log; the module has no runner
	"testing"
)

// holdArg starts this test binary as the process that holds a log (TestMain).
const holdArg = "runlog-hold"

// holdMain is whether this process is the holder, and its exit code: it takes the hold Append takes on the log at
// os.Args[2], says "held" on its stdout, and keeps it until its stdin ends.
func holdMain() (code int, is bool) {
	if len(os.Args) != 3 || os.Args[1] != holdArg {
		return 0, false
	}
	release, err := takeHold(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err) //nolint:forbidigo // the helper process says why it failed on its stderr, which the test reads
		return 1, true
	}
	fmt.Fprintln(os.Stdout, "held") //nolint:forbidigo // the helper tells the test it holds the log on its stdout, the pipe the test reads
	_, _ = io.Copy(io.Discard, os.Stdin)
	_ = release()
	return 0, true
}

// holdLogInAnotherProcess starts a second process that holds the log at path as a writer's Append does, and is
// the func that lets it go. The process is ended with the test.
func holdLogInAnotherProcess(t *testing.T, path string) (letGo func()) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, holdArg, path) //nolint:gosec // this test binary again, with the test's own path
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var why bytes.Buffer
	cmd.Stderr = &why
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	let := func() { _ = in.Close(); _ = cmd.Wait() }
	t.Cleanup(let)
	if line, err := bufio.NewReader(out).ReadString('\n'); line != "held\n" {
		t.Fatalf("the holder said %q (%v) and %q, want held", line, err, why.String())
	}
	return let
}
