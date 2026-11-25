// Package runlog keeps an append-only journal of the runs of a program that change something: what
// was invoked, what it changed and how it ended, with any fields of the caller's own. It is built on
// the standard library's encoding/json/v2 and os, and on go-files' write.Append, which appends each line beneath a
// held folder, ends a line another writer left torn and holds the log across processes for the write; it adds
// the journal the standard library lacks. The program configures the log's path, or its Root and name, and modes,
// the lock and its own fields: the copies a run kept, the way back and the version of its records go there. Each
// update writes the whole record as one JSON line, so the last line for a run's id is the run, a
// person can read the file with tail, and a torn line costs one update, never the history. An entry
// (Handle.Entry, Log.Entry) is a line of its own that names its run, so a run of many entries costs each entry
// once: {"entry":"<the run's id>","time":...,"value":...}.
//
// A run opens its record with Log.Begin before its first change. A run that is killed halfway
// then leaves a record that says it started and never finished, rather than nothing at all, which
// reads the same as a run that never happened. A run that changes nothing begins no record, and a
// nil Handle records nothing, so code that may run without a log needs no branch.
//
// The package writes and reads records. It never undoes anything, never prints, takes no lock and
// never decides what a change is.
package runlog

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	jsonv1 "encoding/json"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record is one run as it stands.
type Record struct {
	ID       string    `json:"id"`
	Argv     []string  `json:"argv"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	// Code is the exit status, meaningful once Finished is set.
	Code int `json:"code"`
	// Outcome is the caller's word for how the run ended, and Summary its sentence: free text, kept
	// as the caller said it.
	Outcome string   `json:"outcome,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Changes []string `json:"changes"`
	// Extra is each field of the caller's own, by name, under the line's extra. A program that keeps
	// copies of what it changed, a way back, or a version of its own records puts them here: what
	// they are, and what reads them, is its own. A field a line holds at its top level under a name
	// the record does not have is read into Extra too.
	Extra map[string]jsonv1.RawMessage `json:"extra,omitempty"`
	// Entries is the run's entry lines in file order, as Read folds them, never written into a record line.
	Entries []Entry `json:"-"`
}

// Entry is one entry of a run, a line of its own in the log: when it was written and the caller's value.
type Entry struct {
	Time  time.Time         `json:"time"`
	Value jsonv1.RawMessage `json:"value"`
}

// Decode reads the entry's value into v.
func (e Entry) Decode(v any) error { return json.Unmarshal(e.Value, v, opts()) }

// entryLine is an entry as its line holds it, its run's id under entry: with no id, an older reader skips it.
type entryLine struct {
	Run string `json:"entry"`
	Entry
}

// Entry writes one entry of the run id, any value encoding/json/v2 writes, as one line in one write, from a
// process with no Handle. It reads no entry of the log, so it costs the entry alone, and it does not look for the run: Read
// skips and counts an entry whose run the log does not hold. An id of "" matches ErrNotFound.
func (l Log) Entry(ctx context.Context, id string, v any) error {
	if l.Path == "" {
		return ErrNoPath
	}
	if id == "" {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	b, err := marshal(v)
	if err == nil {
		b, err = marshal(entryLine{id, Entry{l.now(), b}})
	}
	if err != nil {
		return err
	}
	return l.appendLine(ctx, b)
}

// Interrupted is a run with an opening line and no closing one.
func (r Record) Interrupted() bool { return r.Finished.IsZero() }

// Field reads the caller's own field name into v, and is whether the
// record has it.
func (r Record) Field(name string, v any) (bool, error) {
	raw, ok := r.Extra[name]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, v, opts())
}

// Digest is how a record names a file's contents: sha256, in hex.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// A Handle is the run being recorded. A nil Handle records nothing and returns nil errors, so a run
// that never changed anything needs no branch. Its state is its own: two runs in one process never
// share one.
type Handle struct {
	log Log
	mu  sync.Mutex
	run Record
	// failed is the first line that could not be written, so the caller can
	// say so once rather than on every change.
	failed error
}

// Begin opens a run's record, with argv as given and each field of extra as the caller's own, and
// writes its first line. Argv is written as it is: a caller with a secret on its command line passes
// a slice with it taken out. When Begin returns an error the run has no record, and a caller that
// keeps one before it changes anything changes nothing: ErrNoPath, a Perm or DirPerm set on Windows (an
// error matching errors.ErrUnsupported), a field of extra that is not
// JSON, a first line longer than MaxLine (ErrLineTooLong), or why the folder, the file or the line could not be made. A log Begin makes is removed
// again when its Created fails. The modes are unix only, as Log.Perm says.
func (l Log) Begin(argv []string, extra map[string]any) (*Handle, error) {
	if l.Path == "" {
		return nil, ErrNoPath
	}
	if err := l.modesRefused(); err != nil {
		return nil, err
	}
	r := Record{ID: l.newID(), Argv: argv, Started: l.now(), Changes: []string{}}
	for name, v := range extra {
		b, err := marshal(v)
		if err != nil {
			return nil, err
		}
		if r.Extra == nil {
			r.Extra = map[string]jsonv1.RawMessage{}
		}
		r.Extra[name] = b
	}
	if b, err := marshal(r); err != nil || l.tooLong(b) != nil {
		return nil, cmp.Or(err, l.tooLong(b)) // before the folder is made: a refusal for length leaves the disk as it was
	}
	if l.Root == nil {
		if err := os.MkdirAll(filepath.Dir(l.Path), cmp.Or(l.DirPerm, 0o700)); err != nil {
			return nil, err
		}
	}
	h := &Handle{log: l}
	h.update(func(run *Record) { *run = r })
	if err := h.Err(); err != nil {
		return nil, err
	}
	return h, nil
}

// Resume is the Handle of a run already in the log, found by its whole id as Handle.ID gave it: a
// prefix is not an id here, and an id the log does not hold, or "", matches ErrNotFound. ErrNoPath
// is the error of a Log with no Path, and a log that cannot be read is that error, with no Handle.
// The Handle starts from the run's last line, as Read gives it, and Change, Set and Finish write
// further lines of that run, each one line in one write, as Begin's Handle does. The log takes no
// lock: the caller holds its own across processes. A finished run may be resumed, and its Handle keeps
// the run's close as it is; a later Finish writes a new close and the last close wins, so a
// caller that wants the first close kept does not call Finish.
func (l Log) Resume(ctx context.Context, id string) (*Handle, error) {
	runs, err := l.Read(ctx)
	if err != nil {
		return nil, err
	}
	if id != "" {
		for _, r := range runs {
			if r.ID == id {
				return &Handle{log: l, run: r}, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
}

// Change records one change, as the program printed it ("write /etc/...").
func (h *Handle) Change(line string) {
	h.update(func(r *Record) { r.Changes = append(r.Changes, line) })
}

// Set records the caller's own field, any value encoding/json/v2 writes, in place of an earlier one of
// that name. A value that does not encode is Err, and the field is left as it was. Each Set writes the
// record whole, so a list set again for each addition costs the square of its length: Entry is for those.
func (h *Handle) Set(field string, v any) {
	if h == nil {
		return
	}
	b, err := marshal(v)
	if err != nil {
		h.mu.Lock()
		h.failed = cmp.Or(h.failed, err)
		h.mu.Unlock()
		return
	}
	h.update(func(r *Record) {
		r.Extra = maps.Clone(r.Extra)
		if r.Extra == nil {
			r.Extra = map[string]jsonv1.RawMessage{}
		}
		r.Extra[field] = b
	})
}

// Entry records one entry of the run as Log.Entry writes it, a line of its own. A failure is Err.
func (h *Handle) Entry(v any) {
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.log.hold()) //nolint:forbidigo // Entry takes no context, as Change and Set take none: the hold bounds the write
	defer cancel()
	err := h.log.Entry(ctx, h.ID(), v)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failed = cmp.Or(h.failed, err)
}

// Finish writes the closing line, with the caller's word for how the run ended and its sentence, and
// is Err: the first line of the run that could not be written, this one among them. A nil Handle
// returns nil.
func (h *Handle) Finish(code int, outcome, summary string) error {
	h.update(func(r *Record) {
		r.Finished, r.Code = h.log.now(), code
		r.Outcome, r.Summary = outcome, summary
	})
	return h.Err()
}

// Err is the first failure to write the record, if there was one.
func (h *Handle) Err() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failed
}

// ID is the run's id, "" for a nil Handle.
func (h *Handle) ID() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.run.ID
}

// update makes change to the record and appends the record as it then stands. A failure does not stop the
// run: the change already happened, and losing the note of it is no reason to report the change as failed. It
// is kept for the caller to say out loud, and the record keeps the change for its next line. A line longer
// than MaxLine is the one exception: the record is left as it was, so the run's later lines are still written.
func (h *Handle) update(change func(*Record)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	next := h.run
	change(&next)
	b, err := marshal(next)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), h.log.hold()) //nolint:forbidigo // Begin, Change, Set and Finish take no context: the hold bounds each write instead
		err = h.log.appendLine(ctx, b)
		cancel()
	}
	if !errors.Is(err, ErrLineTooLong) {
		h.run = next
	}
	h.failed = cmp.Or(h.failed, err)
}
