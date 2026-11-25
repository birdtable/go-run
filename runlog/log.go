package runlog

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/birdtable/go-files/confine"
	"github.com/birdtable/go-files/write"
)

// ErrNoPath is the error of a Log with no Path: there is no default place for a log.
var ErrNoPath = errors.New("run: the log has no path")

// ErrLineTooLong is the error of a write whose line is longer than the Log's MaxLine: nothing is written.
var ErrLineTooLong = errors.New("run: the line is longer than MaxLine")

// DefaultMaxLine is the longest line a Log writes and Read reads when Log.MaxLine is zero or less.
const DefaultMaxLine = 16 << 20

// Log is one journal file, and how it is written. Path is the one field it needs.
type Log struct {
	// Path is the log, one JSON line per write, or its name beneath Root. Begin, Read and Resume return ErrNoPath when it is "".
	Path string
	// Perm is the log's mode when made, 0600 when zero; DirPerm its folder's, 0700 when zero. Unix only: on
	// Windows a set Perm or DirPerm is refused, with an error matching errors.ErrUnsupported, and the log takes
	// its folder's ACL; Created is where a caller sets another.
	Perm, DirPerm fs.FileMode
	// Created is called once, with the file open, when a write makes the log: to give it a group, an
	// ACL or a label. When it fails the new file is removed and the write fails. Nil does nothing.
	Created func(*os.File) error
	// Sync flushes every line before the write returns, and the folder when the log is made.
	Sync bool
	// Now is the clock of a record's times, time.Now when nil; they are kept in UTC.
	Now func() time.Time
	// NewID makes a run's id, 16 hex characters from crypto/rand when nil.
	NewID func() string
	// MaxLine is the longest line a write writes and Read reads, DefaultMaxLine when zero or less. A longer line
	// is refused at the write with ErrLineTooLong, so a reader's MaxLine must be at least its writer's.
	MaxLine int
	// Skipped, when not nil, is set by Read to the number of lines it skipped.
	Skipped *int
	// Root, when set, is the folder the log is kept in, held open by the caller, and Path is the log's name
	// beneath it: a relative name, refused when it climbs out, as os.Root refuses it. No folder is made, and
	// every open of the log goes through the Root, so a folder swapped for a link after the Root was taken
	// carries no write. DirPerm is not used. With no Root a log is by its Path: the folder is made with DirPerm,
	// then opened as a root for each write.
	Root *os.Root
	// bound is a test's shorter defaultHold.
	bound time.Duration
}

// now is the log's clock, in UTC.
func (l Log) now() time.Time {
	if l.Now != nil {
		return l.Now().UTC()
	}
	return time.Now().UTC()
}

// newID is a run's id from the log's NewID, or 16 random hex characters.
func (l Log) newID() string {
	if l.NewID != nil {
		return l.NewID()
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand's Read never returns an error
	return hex.EncodeToString(b)
}

// defaultHold is how long a write waits for another process's hold on the log: Begin, Change, Set and Finish
// take no context, so each waits at most this long, and then fails with an error matching
// context.DeadlineExceeded, kept in Handle.Err.
const defaultHold = 10 * time.Second

// hold is the bound of one write's wait, defaultHold unless a test in this package names a shorter one.
func (l Log) hold() time.Duration { return cmp.Or(l.bound, defaultHold) }

// held is the folder the log is kept in, as a root, and the log's name beneath it, and the func that lets the
// folder go: the caller's Root as it is, or the folder of Path opened for this one write.
func (l Log) held() (root *os.Root, name string, release func() error, err error) {
	if l.Root != nil {
		return l.Root, l.Path, func() error { return nil }, nil
	}
	root, err = os.OpenRoot(filepath.Dir(l.Path))
	if err != nil {
		return nil, "", nil, err
	}
	return root, filepath.Base(l.Path), root.Close, nil
}

// appendLine writes b as one line at the end of the log, through go-files' write.Append beneath the log's
// folder, making the log when it is not there. A last line another writer left torn is ended in the same write,
// and a link at the log or the folder, a second name and anything but a plain file are refused. A line longer
// than MaxLine is ErrLineTooLong, and nothing is opened or made.
func (l Log) appendLine(ctx context.Context, b []byte) (err error) {
	if err := l.tooLong(b); err != nil {
		return err
	}
	root, name, release, err := l.held()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	if err := l.make(root, name); err != nil {
		return err
	}
	opts := []write.LogOption{write.Aside(name + ".aside")} // Append names no default; the limit below never moves a log
	if l.Perm != 0 {
		opts = append(opts, write.LogMode(l.Perm))
	}
	if err := write.Append(ctx, root, name, b, math.MaxInt64, opts...); err != nil {
		return err
	}
	if !l.Sync {
		return nil
	}
	f, err := root.OpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// tooLong is ErrLineTooLong when the line b is longer than MaxLine, and nil when it is not.
func (l Log) tooLong(b []byte) error {
	if limit := cmp.Or(max(l.MaxLine, 0), DefaultMaxLine); len(b) > limit {
		return fmt.Errorf("%w: %d bytes, and MaxLine is %d", ErrLineTooLong, len(b), limit)
	}
	return nil
}

// make makes the log when the root has none at name, empty, with Perm or 0600, so Created sees it before its
// first line, and flushes the folder with Sync. A log whose Created or flush fails is removed again.
func (l Log) make(root *os.Root, name string) error {
	if _, err := root.Lstat(name); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, cmp.Or(l.Perm, 0o600))
	if errors.Is(err, fs.ErrExist) {
		return nil // another process made it since the look
	}
	if err != nil {
		return err
	}
	if l.Created != nil {
		err = l.Created(f)
	}
	if err == nil && l.Sync {
		err = write.SyncFolder(root, ".")
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return errors.Join(err, root.Remove(name))
	}
	return nil
}

// ErrNotPlain is the error of a read of a log that is a link, a pipe, a folder or anything but a plain file at
// its name, as a write refuses it: nothing is read, and a pipe is not waited on.
var ErrNotPlain = errors.New("run: the log is not a plain file")

// openRead opens the log to read, through go-files' confine.Open: beneath Root when there is one, beneath the
// folder of Path when not. No link is followed at any name and the open does not wait, so a link at the log's
// name or a pipe is refused at once, and so is a folder or any file that is not plain, all as ErrNotPlain.
func (l Log) openRead() (f *os.File, err error) {
	root, name := l.Root, l.Path
	if root == nil {
		if root, err = os.OpenRoot(filepath.Dir(l.Path)); err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, root.Close()) }() // a file opened beneath it stays open
		name = filepath.Base(l.Path)
	}
	f, err = confine.Open(root, name, os.O_RDONLY, 0)
	if errors.Is(err, confine.ErrOutside) {
		return nil, fmt.Errorf("%s: %w: %w", l.Path, ErrNotPlain, err)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s: %w: it is %s", l.Path, ErrNotPlain, info.Mode().Type())
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

// opts are the options of every line: the HTML and JavaScript escapes, sorted keys, a nil list as null
// and invalid UTF-8 replaced, so a line is byte-equal to one written before json/v2.
func opts() json.Options {
	return json.JoinOptions(jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true), json.Deterministic(true), json.FormatNilSliceAsNull(true), jsontext.AllowInvalidUTF8(true))
}

// marshal is v as one line, without its line ending.
func marshal(v any) ([]byte, error) { return json.Marshal(v, opts()) }

// Read is every run in the log, in the order they started, each as its last line says, with its entry lines
// in Entries in the order of the file, wherever they fall around the run's record lines. A missing log
// is no runs. A line that is not a record or an entry, has a name twice or is longer than MaxLine is
// skipped and counted in Skipped, as an entry line with no time or no value and a line torn by a power cut are, and the line before it for that
// run stands; an entry line whose run the log has no record line for is skipped and counted too. A field at a line's top level that the record has no name for is read into Extra, so
// Record.Field reaches it and Lines writes it under extra. The record has no version: a program that changes the shape of its own fields in
// Extra names the version in Extra, and reads it with Record.Field.
func (l Log) Read(ctx context.Context) ([]Record, error) {
	runs, _, err := l.readAt(ctx, 0)
	return runs, err
}

// readBuffer is the size of a read of the log: a line within it is parsed where it lies, with no copy.
const readBuffer = 64 << 10

// ErrBadOffset is the error of a ReadSince from an offset that is not the start of a line of the log: one past
// the log's end, or not just after a newline. Nothing is read.
var ErrBadOffset = errors.New("run: the offset is not at the start of a line of the log")

// ReadSince is Read of the lines from offset on, and the offset after the last whole line it read, to hand to
// the next call: a caller that keeps its runs and that offset reads only what was written since. An offset of 0
// is the whole log, as Read gives it. The runs are those of the lines after offset, folded among themselves, so
// the caller merges them into what it holds by Record.ID; an entry line there whose run's record line lies
// before offset has no run in the result and is skipped and counted in Skipped, so a caller that follows entries
// resumes from the offset of the run's first line. A last line with no newline yet is read but not counted in
// the offset, and is read again by the next call, when its run is the same as it was. An offset that is not at a
// line's start, or lies past the log's end, is ErrBadOffset, and a log that is not there is no runs and the
// same offset. A Log with no Path is ErrNoPath.
func (l Log) ReadSince(ctx context.Context, offset int64) (runs []Record, next int64, err error) {
	if offset < 0 {
		return nil, offset, fmt.Errorf("%w: %d", ErrBadOffset, offset)
	}
	return l.readAt(ctx, offset)
}

// readAt reads the log from the line start at offset, and is the runs and the offset after the last whole line.
func (l Log) readAt(ctx context.Context, offset int64) (runs []Record, next int64, err error) {
	if l.Path == "" {
		return nil, offset, ErrNoPath
	}
	f, err := l.openRead()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, offset, nil
	}
	if err != nil {
		return nil, offset, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if offset > 0 {
		if err := atLineStart(f, offset); err != nil {
			return nil, offset, fmt.Errorf("%s: %w: %d", l.Path, ErrBadOffset, offset)
		}
	}
	runs, skipped, n, err := scan(ctx, f, l.MaxLine)
	if l.Skipped != nil {
		*l.Skipped = skipped
	}
	if err != nil {
		return nil, offset, fmt.Errorf("%s: %w", l.Path, err)
	}
	return runs, offset + n, nil
}

// atLineStart seeks f to offset when the byte before it is a newline and offset is within the file, and is an
// error when not.
func atLineStart(f *os.File, offset int64) error {
	var prev [1]byte
	if _, err := f.ReadAt(prev[:], offset-1); err != nil || prev[0] != '\n' {
		return errors.New("not at a line start")
	}
	_, err := f.Seek(offset, io.SeekStart)
	return err
}

// ReadFrom is Log.Read of any reader. A maxLine of zero or less is DefaultMaxLine.
func ReadFrom(ctx context.Context, r io.Reader, maxLine int) ([]Record, error) {
	runs, _, err := readFrom(ctx, r, maxLine)
	return runs, err
}

// readFrom is ReadFrom and the number of lines it skipped.
func readFrom(ctx context.Context, r io.Reader, maxLine int) (runs []Record, skipped int, err error) {
	runs, skipped, _, err = scan(ctx, r, maxLine)
	return runs, skipped, err
}

// scan is readFrom and the number of bytes through the last line that has its newline.
func scan(ctx context.Context, r io.Reader, maxLine int) (runs []Record, skipped int, whole int64, err error) {
	maxLine = cmp.Or(max(maxLine, 0), DefaultMaxLine)
	f := fold{opts: opts(), runs: []Record{}, index: map[string]int{}, entries: map[string][]Entry{}}
	br := bufio.NewReaderSize(r, readBuffer)
	var buf []byte // the line, when it is longer than br's buffer
	var read int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, 0, err
		}
		line, long, n, err := nextLine(br, &buf, maxLine)
		read += int64(n)
		if err == nil {
			whole = read
		}
		f.add(line, long)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, 0, err
		}
	}
	runs, skipped = f.done()
	return runs, skipped, whole, nil
}

// fold is the runs of the lines of a log, as Read folds them: a run is its last record line, with its entry lines.
type fold struct {
	opts    json.Options // built once for the read
	runs    []Record
	index   map[string]int // a run's place in runs, by id
	entries map[string][]Entry
	skipped int
}

// add folds one line in, or counts it skipped: a line past MaxLine, or one that is neither record nor entry.
func (f *fold) add(line []byte, long bool) {
	if !long && len(line) == 0 {
		return
	}
	if long {
		f.skipped++
		return
	}
	rec, in, err := parse(line, f.opts)
	switch {
	case err != nil:
		f.skipped++
	case in.Run != "":
		f.entries[in.Run] = append(f.entries[in.Run], in.Entry)
	default:
		if i, seen := f.index[rec.ID]; seen {
			f.runs[i] = rec
		} else {
			f.index[rec.ID] = len(f.runs)
			f.runs = append(f.runs, rec)
		}
	}
}

// done is the runs with their entries, and the lines skipped, an entry line whose run has no record line among them.
func (f *fold) done() (runs []Record, skipped int) {
	if len(f.entries) > 0 {
		for i := range f.runs {
			f.runs[i].Entries = f.entries[f.runs[i].ID]
			delete(f.entries, f.runs[i].ID)
		}
	}
	for _, orphans := range f.entries {
		f.skipped += len(orphans)
	}
	return f.runs, f.skipped
}

// parse is the record on a line, the entry line of the run it names when it has no id, or why it is neither: an
// entry line names its run and has a time and a value, a null among them, as a record line has an id. A
// member at the line's top level that the record has no name for is kept in Extra under its name,
// as a line another program wrote holds its own fields; one the line also names under extra gives
// way to that one.
func parse(line []byte, o json.Options) (Record, entryLine, error) {
	var in struct {
		Record
		// An entry line's members, kept raw: a record line that has them as fields of its own holds them in Extra.
		Run  jsontext.Value            `json:"entry"`
		At   jsontext.Value            `json:"time"`
		Val  jsontext.Value            `json:"value"`
		Rest map[string]jsontext.Value `json:",embed"`
	}
	// One pass reads the line as a record, and what the record has no name for is held in Rest.
	err := json.Unmarshal(line, &in, o)
	if err != nil {
		return parseEntry(line, o, err)
	}
	if in.ID == "" {
		return entryOf(in.Run, in.At, in.Val, line, o)
	}
	if in.Run != nil || in.At != nil || in.Val != nil || len(in.Rest) > 0 {
		if in.Rest == nil {
			in.Rest = map[string]jsontext.Value{}
		}
		for name, raw := range map[string]jsontext.Value{"entry": in.Run, "time": in.At, "value": in.Val} {
			if raw != nil {
				in.Rest[name] = raw
			}
		}
		for name, raw := range in.Rest {
			if _, own := in.Extra[name]; !own {
				if in.Extra == nil {
					in.Extra = map[string]jsonv1.RawMessage{}
				}
				in.Extra[name] = jsonv1.RawMessage(raw)
			}
		}
	}
	return in.Record, entryLine{}, nil
}

// entryOf is the entry line whose members the first pass read raw, or why the line is no line of the log.
func entryOf(runRaw, atRaw, value jsontext.Value, line []byte, o json.Options) (Record, entryLine, error) {
	var run string
	var at time.Time
	if runRaw == nil || value == nil || atRaw == nil || string(atRaw) == "null" ||
		json.Unmarshal(runRaw, &run, o) != nil || run == "" || json.Unmarshal(atRaw, &at, o) != nil {
		return parseEntry(line, o, nil)
	}
	return Record{}, entryLine{run, Entry{at, jsonv1.RawMessage(value)}}, nil
}

// parseEntry is the entry line a line is, or why it is no line of the log: recErr is why it is not a record.
func parseEntry(line []byte, o json.Options, recErr error) (Record, entryLine, error) {
	var head struct {
		ID string `json:"id"`
	}
	err := json.Unmarshal(line, &head, o)
	if err == nil && head.ID == "" {
		var in struct {
			Run   string            `json:"entry"`
			Time  *time.Time        `json:"time"`
			Value jsonv1.RawMessage `json:"value"`
		}
		if json.Unmarshal(line, &in, o) == nil && in.Run != "" && in.Time != nil && in.Value != nil {
			return Record{}, entryLine{in.Run, Entry{*in.Time, in.Value}}, nil
		}
	}
	return Record{}, entryLine{}, errors.Join(err, errors.New("run: not a record"))
}

// nextLine is the next line of br without its line ending, or long when it is past maxLine, its rest
// read over, and the bytes it took; io.EOF comes with the last line, which may have no newline. A line within br's
// buffer is a slice of it, good until the next call; a longer one is gathered in *buf, which is kept for the next.
func nextLine(br *bufio.Reader, buf *[]byte, maxLine int) (line []byte, long bool, n int, err error) {
	chunk, err := br.ReadSlice('\n')
	n = len(chunk)
	if !errors.Is(err, bufio.ErrBufferFull) {
		line = bytes.TrimRight(chunk, "\r\n")
		return line, len(line) > maxLine, n, err
	}
	*buf = append((*buf)[:0], chunk...)
	for errors.Is(err, bufio.ErrBufferFull) {
		chunk, err = br.ReadSlice('\n')
		n += len(chunk)
		if !long {
			if len(*buf)+len(chunk) > maxLine+2 { // room for "\r\n"
				long = true
			} else {
				*buf = append(*buf, chunk...)
			}
		}
	}
	if long {
		return nil, true, n, err
	}
	line = bytes.TrimRight(*buf, "\r\n")
	return line, len(line) > maxLine, n, err
}
