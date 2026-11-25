package runlog_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/birdtable/go-run/runlog"
)

// A run that writes a file keeps a copy first, records the way back in fields of its own, and ends
// with its outcome. The record has no field for either: they are the program's, under Extra.
func Example() {
	dir, err := os.MkdirTemp("", "run-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)
	target, backup := filepath.Join(dir, "app.conf"), filepath.Join(dir, "app.conf.kept")
	data := []byte("port = 8080\n")

	log := runlog.Log{Path: filepath.Join(dir, "state", "runs.jsonl")}
	h, err := log.Begin([]string{"app", "configure"}, nil)
	if err != nil {
		fmt.Println(err) // no record, so change nothing
		return
	}
	h.Change("write " + target)
	h.Set("kept", []map[string]string{{"file": target, "copy": backup}})
	if err := os.WriteFile(target, data, 0o600); err != nil {
		fmt.Println(errors.Join(err, h.Finish(1, "failed", err.Error())))
		return
	}
	h.Set("kept", []map[string]string{{"file": target, "copy": backup, "after": runlog.Digest(data)}})
	h.Set("undo", "app configure --undo")
	if err := h.Finish(0, "done", "config written"); err != nil {
		fmt.Println(err)
		return
	}

	runs, err := log.Read(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	r := runs[0]
	var undo string
	_, _ = r.Field("undo", &undo)
	fmt.Println(r.Outcome, r.Summary, len(r.Changes), undo, r.Interrupted())
	// Output: done config written 1 app configure --undo false
}

// A run is recorded as one line, and read back with its outcome.
func ExampleLog_Begin() {
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
