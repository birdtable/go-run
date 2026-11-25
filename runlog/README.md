# runlog

An append-only journal of the runs of a program that change something.

[![Go reference](https://pkg.go.dev/badge/github.com/birdtable/go-run/runlog.svg)](https://pkg.go.dev/github.com/birdtable/go-run/runlog)

A run opens a record before its first change and adds to it as it goes: what was invoked, what it
changed, how it ended, and any fields of your own. Each update is one whole JSON line in a file a person
can `tail`, and the last line for an id is the run. A run that adds many entries writes each as a short
line of its own. The package writes and reads records. It never undoes anything, never prints, and never
decides what a change is: the copies a run kept, the way back and the version of your records are
yours, and go in your own fields.

## Install

```sh
go get github.com/birdtable/go-run/runlog
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/birdtable/go-run/runlog"
)

func main() {
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
```

A run that keeps a copy of a file first and records the way back in fields of its own is
`Example` in the package's [reference](https://pkg.go.dev/github.com/birdtable/go-run/runlog#example-package).

It imports the standard library and [`go-files/write`](https://pkg.go.dev/github.com/birdtable/go-files/write), and no other package of this module.

## Where the log goes, and who can read it

`Log.Path` is the one field a log needs, and there is no default: `Begin`, `Read` and `Resume` return
`ErrNoPath` without it. On unix the file is made with mode `0600` and its folder with `0700`, so it
is its writer's alone; `Perm` and `DirPerm` say otherwise. That is unix only: on Windows a Go file mode
sets only the read-only bit, so a set `Perm` or `DirPerm` is refused, `Begin` returns an error matching
`errors.ErrUnsupported` and makes nothing, and an unset one runs. On Windows the log and its folder take
their parent folder's access list, and a caller who needs owner-only access sets one in `Created`. `Created` is called once, with the file open,
when a write makes it: a caller can give it a group, set an ACL or put a label on it there. There is no
group option: a caller that needs a group sets it on the file itself. A `Created` that fails removes the
new file and fails the write.

`Log.Root` is a folder the program holds open (an `*os.Root`), and then `Path` is the log's name beneath it: a
relative name, refused when it climbs out. No folder is made, and every open of the log, to write, read or
resume it, goes through the Root, so a folder replaced by a link after the Root was taken carries no write. With
no Root the folder is made by path with `DirPerm`, and opened as a root for each write.
A read (`Read`, `Resume`, `Find`) opens the log the same way, with no link followed at its name and no wait on the
open: a link, a pipe, a folder or any file that is not plain at the name is `ErrNotPlain`.

Every line is written by go-files' `write.Append`, the same in both ways. A log whose last line another
writer left torn, a crash in the middle of its write, has that line ended by a newline put before the new
line in the same write, so the new record is kept and `Read` skips the torn line alone (`Skipped` counts it).
A link at the log or at its folder, a log with a second name and anything there but a plain file are refused,
and `Handle.Err` says so. Append holds the log across processes for each write, and a write waits at most 10 s
for another process's hold: `Begin`, `Change`, `Set`, `Handle.Entry` and `Finish` take no context, and the one that waits
longer fails with an error matching `context.DeadlineExceeded`, kept in `Handle.Err`. `Log.Entry` takes a context and
waits until it ends. `Read` and `Resume` take no hold. The log is never rotated.

Argv is written as it is given. A program with a secret on its command line passes a slice with the
secret taken out.

## What an id is

A run's id is 16 hex characters from `crypto/rand`, so two runs of one log do not share one. `NewID`
gives a scheme of the caller's own, such as a trace id. `Find` takes an id or the start of one, so a
person types a few characters: `ErrNotFound` when no run's id starts with them, `ErrAmbiguous` when
more than one's does.

`Log.Resume(ctx, id)` is the Handle of a run already in the log, for a program that did not begin it: a
guard that closes a run another process left open. It takes the whole id `Handle.ID` gave, never a
prefix (`ErrNotFound` for an id the log does not hold), starts from the run's last line, and writes
further lines of it, each one line in one write. A finished run may be resumed, its close stands until
`Finish` is called again, and the last close wins; a caller that wants the first close kept does not
call `Finish`. The log takes no lock, so the caller holds its own across processes.

`Log.ReadSince(ctx, offset)` is `Read` of the lines from a byte offset on, and returns the offset after the last whole line it
read: a program that keeps its runs and that offset reads only what was written since, and merges the runs it gets by
`Record.ID`. Offset 0 is the whole log, and an offset that is not at a line start is `ErrBadOffset`. An entry line whose
run's record line lies before the offset is skipped and counted in `Skipped`.

## What one line holds

A line is the whole record as it stands: `id`, `argv`, `started`, `finished` once the run ends, `code`,
the caller's `outcome` word and `summary` sentence, `changes` and `extra`. `extra` holds the caller's own
fields, set by `Handle.Set` or given to `Begin`, so they never meet the record's names. A program that
keeps copies of what it changed, a way back or a version of its own records puts each in `extra`
under a name of its own, and reads it back with `Record.Field`: what they hold, and whether anything
acts on them, is its own. `Now` is the clock a record's times are read from, in UTC.

## An entry is a line of its own

A run that adds to a list as it goes, a line for each file it changed, does not set the list again each
time: `Set` writes the whole record, so n additions would write the list n times. `Handle.Entry(v)` writes
one line that names the run and holds the entry's time and its value, any value `encoding/json/v2` writes:

```
{"entry":"<the run's id>","time":"2026-11-14T09:08:07.123456789Z","value":{"file":"/etc/app.conf"}}
```

It is one `write.Append`, it reads no entry of the log and it writes no record line again, so an entry costs its own
bytes however long the run and the log are. `Log.Entry(ctx, id, v)` is the same line from a process that
holds no Handle, only the run's id; it does not look for the run. `Read`, `ReadFrom`, `Find` and `Resume` fold a
run's entry lines into `Record.Entries`, in the order of the file, wherever they fall around the run's
record lines, and `Entry.Decode` reads a value into the caller's type. `Entries` is never written into a
record line. An entry line has no `id`, so a reader from before entry lines skips it and counts it, and
reads the runs as it did. An entry line whose run the log does not hold is skipped and counted in
`Log.Skipped`, as is one with no `time` or no `value` (a `null` is a value), and a torn one costs that entry alone.

## How the file grows, and how to trim it

Every update writes the whole record again, so a run of n updates writes n lines, each a little
longer; an entry writes its own line and no more. `Read` holds every run in memory, with its entries. A write
whose line is longer than `MaxLine` (16 MiB by default) writes nothing, makes no file and no folder, and is `ErrLineTooLong`: returned by `Begin` and
`Log.Entry`, and kept in `Handle.Err` from `Set`, `Change`, `Handle.Entry` and `Finish`. A refused `Set` or `Change`
leaves the record as it was, so the run's later lines, its close among them, are still written. `Read` skips a line
longer than its own `MaxLine` as it skips a torn one, and counts it in `Log.Skipped`, so a reader's `MaxLine` must be
at least its writer's. `Lines` is the pure half of compaction: it gives the log as one
record line for each run a predicate keeps, followed by that run's entry lines, and the runs it dropped, so the caller can clean up after
them, whatever files its own fields name. A caller compacts in four steps, under its own lock, and the package takes none:

```go
b, err := os.ReadFile(path)                                // the log as it is
runs, err := runlog.ReadFrom(ctx, bytes.NewReader(b), 0)    // each run, as its last line says, with its entries
lines, dropped, err := runlog.Lines(runs, keep)            // the new log, and the runs left out
err = write.File(path, lines, write.KeepMode)              // replaced whole, with the mode it had
```

The package never trims on its own and never removes a caller's file.

## What a crash leaves

The opening line is written before the first change, so a run killed halfway reads back as one
that started and never finished (`Record.Interrupted`), with what it had changed so far. A line torn
by a power cut is skipped, and the line before it for that run stands, so a crash costs one
update or one entry, never the history. `Sync` flushes every line, and the folder when the log is made, for a
caller that would rather pay a flush per line than lose the last update. `Read` and `ReadFrom` read
whatever is in a log, a damaged one: a line that is not a record, has a name twice or is torn is skipped
and counted in `Log.Skipped`. A field at a line's top level that the record has no name for, as another program or an older release wrote it, is kept in `Extra` under its name and written under `extra` from then on; where the line also holds the name under `extra`, that one stands. The record has no version, so no line is refused for one: a program
that changes the shape of its own fields in `extra` names a version there and checks it when it reads.

## What it does not do

- It takes no lock. A program that may run twice at once holds its own, and holds it too while it
  compacts the log or acts on a run it read back.
- It keeps no copy and performs no undo. A way back, if a program has one, is a field of its own in
  `extra`, and taking it is the program's.
- It redacts nothing. What a caller gives it is written as given.

`go doc github.com/birdtable/go-run/runlog` has the rest; every exported name is documented.
`FuzzRead` is the reader's fuzz target, and its seeds in `testdata/fuzz/FuzzRead/` run in every
`go test`.

## Built on, adds, configured by

**Built on:** the standard library's `encoding/json/v2` and `os`, and [`go-files/write`](https://pkg.go.dev/github.com/birdtable/go-files/write)'s `Append`, which appends each line beneath a held folder, ends a line another writer left torn and holds the log across processes for the write.

**Adds:** the journal the standard library lacks: a run's record opened before its first change, its entries, and a torn line that costs one update and never the history.

**Your program configures:** the log's path, or its `Root` and name, the modes, the lock, and its own fields: the copies a run kept, the way back and the version of its records go there.

## Systems

Linux, macOS and Windows.
