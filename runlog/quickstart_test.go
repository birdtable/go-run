package runlog_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/birdtable/go-run/runlog"
)

// The quick start of the root README, word for word: TestTheReadmeQuickStartIsThisExample holds the two
// the same, so a reader who copies it gets a program that builds and prints what the README says.
func Example_quickStart() {
	dir, err := os.MkdirTemp("", "run-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)

	log := runlog.Log{Path: filepath.Join(dir, "runs.jsonl")}
	h, err := log.Begin([]string{"app", "sync"}, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	h.Change("copy two files")
	if err := h.Finish(0, "done", "two files copied"); err != nil {
		fmt.Println(err)
		return
	}
	runs, err := log.Read(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(runs[0].Outcome, runs[0].Summary)
	// Output: done two files copied
}
