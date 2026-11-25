package main

import (
	"testing"

	"github.com/birdtable/go-testkit/taskfile"
)

// TestTheTaskfileCallsGo holds Taskfile.yml to Go: no shell utility, pipe, redirection, shell loop,
// /tmp path or path joined in shell, and no line in taskfile-exceptions.txt that the check does not need.
func TestTheTaskfileCallsGo(t *testing.T) {
	t.Parallel()
	taskfile.Check(t, "../..")
}
