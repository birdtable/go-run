package runlog

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// earlierKept and earlierUndo are a product's own types for what an earlier program wrote as kept and undo.
type earlierKept struct {
	File  string `json:"file"`
	Copy  string `json:"copy"`
	After string `json:"after"`
}

type earlierUndo struct {
	How   string   `json:"how"`
	Lines []string `json:"lines"`
	Why   string   `json:"why"`
}

// earlierFields is what a product reads of a run the earlier program wrote, each field by Record.Field.
type earlierFields struct {
	version      int
	build        string
	ending, what string
	kept         []earlierKept
	undo         earlierUndo
	images       []image
	has          string // each name Field found, in this order, space-separated
}

func earlierRead(t *testing.T, r Record) earlierFields {
	t.Helper()
	var f earlierFields
	var has []string
	for _, c := range []struct {
		name string
		into any
	}{{"version", &f.version}, {"build", &f.build}, {"ending", &f.ending}, {"what", &f.what}, {"kept", &f.kept}, {"undo", &f.undo}, {"images", &f.images}} {
		ok, err := r.Field(c.name, c.into)
		if err != nil {
			t.Errorf("run %s: Field(%q): %v", r.ID, c.name, err)
		}
		if ok {
			has = append(has, c.name)
		}
	}
	f.has = strings.Join(has, " ")
	return f
}

// earlierFinished fails t when a is not the fixture's finished run with each field the earlier program wrote.
func earlierFinished(t *testing.T, when string, a Record) {
	t.Helper()
	if a.ID != "14274cf4" || !slices.Equal(a.Argv, []string{"app", "configure", "--apply"}) || a.Started.IsZero() || a.Interrupted() ||
		a.Code != 0 || !slices.Equal(a.Changes, []string{"write /etc/app/app.conf"}) || a.Outcome != "" || a.Summary != "" {
		t.Errorf("%s: the finished run read as %+v", when, a)
	}
	f := earlierRead(t, a)
	wantKept := []earlierKept{{File: "/etc/app/app.conf", Copy: "/var/lib/app/kept/app.conf.1", After: Digest([]byte("new\n"))}}
	if f.has != "version build ending what kept undo images" || len(a.Extra) != 7 || f.version != 2 || f.build != "v0.3.1 (712a01e)" ||
		f.ending != "DONE" || f.what != "config written" || !slices.Equal(f.kept, wantKept) {
		t.Errorf("%s: the finished run's fields read as %+v, extra %v", when, f, a.Extra)
	}
	if f.undo.How != "copy_back" || !slices.Equal(f.undo.Lines, []string{"app undo 14274cf4"}) ||
		!slices.Equal(f.images, []image{{Container: "web", Before: "web:1"}}) {
		t.Errorf("%s: the finished run's way back and note read as %+v and %+v", when, f.undo, f.images)
	}
}

// earlierInterrupted fails t when b is not the fixture's interrupted run with each field the earlier program wrote.
func earlierInterrupted(t *testing.T, when string, b Record) {
	t.Helper()
	if b.ID != "0ebe60bd" || !slices.Equal(b.Argv, []string{"app", "serve", "--apply"}) || !b.Interrupted() ||
		!slices.Equal(b.Changes, []string{"restart web"}) || b.Outcome != "" || b.Summary != "" {
		t.Errorf("%s: the interrupted run read as %+v", when, b)
	}
	f := earlierRead(t, b)
	if f.has != "version build undo" || len(b.Extra) != 3 || f.version != 2 || f.build != "v0.3.1 (712a01e)" ||
		f.undo.How != "none" || f.undo.Why != "a restart has no way back" {
		t.Errorf("%s: the interrupted run's fields read as %+v, extra %v", when, f, b.Extra)
	}
}

// testdata/top-level-fields.jsonl is a journal an earlier program's own run package wrote: one finished run with
// a kept copy, a way back and a note of its own, and one interrupted run with a way back. It
// wrote version, build, ending, what, kept, undo and each note at the line's top level, where the
// record here has no name for them. Read keeps each one: Record.Field reaches it on the line as
// the earlier program left it, and after a Lines compaction, which writes it under extra, so an old line is moved
// once and loses nothing. Outcome and Summary stay empty: ending and what are the earlier program's words, and a
// product reads them with Field and decides what they mean.
//
// What it costs when this fails: a product that adopts this package cannot read the copies its old runs kept
// or their way back, and the first compaction of its log removes them from the disk for good.
func TestAJournalOfAnEarlierWriterKeepsItsTopLevelFieldsThroughFieldAndLines(t *testing.T) {
	t.Parallel()
	skipped := 0
	runs, err := Log{Path: filepath.Join("testdata", "top-level-fields.jsonl"), Skipped: &skipped}.Read(t.Context())
	if err != nil || len(runs) != 2 || skipped != 0 {
		t.Fatalf("%d runs, %d skipped, %v", len(runs), skipped, err)
	}
	compacted, dropped, err := Lines(runs, nil)
	if err != nil || len(dropped) != 0 {
		t.Fatalf("Lines: %d dropped, %v", len(dropped), err)
	}
	again, err := ReadFrom(t.Context(), bytes.NewReader(compacted), 0)
	if err != nil || len(again) != 2 {
		t.Fatalf("the compacted log: %d runs, %v", len(again), err)
	}
	checked := 0
	for _, c := range []struct {
		when string
		runs []Record
	}{{"as the earlier program left it", runs}, {"after Lines", again}} {
		earlierFinished(t, c.when, c.runs[0])
		earlierInterrupted(t, c.when, c.runs[1])
		checked++
	}
	if checked != 2 {
		t.Fatalf("checked %d readings, want 2", checked)
	}
	// The compacted log holds them under extra, in the library's own shape, and no longer at the top.
	lines := bytes.Split(bytes.TrimSuffix(compacted, []byte("\n")), []byte("\n"))
	if len(lines) != 2 || !bytes.Contains(lines[0], []byte(`"extra":{"build":"v0.3.1 (712a01e)","ending":"DONE","images":`)) ||
		!bytes.Contains(lines[1], []byte(`"extra":{"build":"v0.3.1 (712a01e)","undo":{"how":"none","why":"a restart has no way back"},"version":2}`)) {
		t.Errorf("the compacted log is not two lines with the earlier program's fields under extra:\n%s", compacted)
	}
	for _, l := range lines {
		if top, _, ok := bytes.Cut(l, []byte(`"extra":`)); !ok || bytes.Contains(top, []byte(`"build"`)) || bytes.Contains(top, []byte(`"version"`)) {
			t.Errorf("a compacted line still holds a field of the earlier program's at its top:\n%s", l)
		}
	}
}

// A line that holds a name at its top level and under extra reads as the one under extra: that one
// is the caller's by the record's own shape, and the top-level one is dropped. It is the one case in
// which a field of a line is not kept, and nothing reports it: the line is a record and is not
// counted in Skipped.
//
// What it costs when this fails: a line a program wrote here, with its own field under extra, reads
// as what another writer left at the top under the same name, and an undo acts on the wrong one.
func TestANameAtTheTopAndUnderExtraReadsAsTheOneUnderExtra(t *testing.T) {
	t.Parallel()
	in := `{"id":"a","argv":["app"],"started":"2026-11-13T09:00:00Z","code":0,"changes":[],"undo":{"how":"top"},"build":"b1","extra":{"undo":{"how":"own"}}}` + "\n"
	runs, skipped, err := readFrom(t.Context(), strings.NewReader(in), 0)
	if err != nil || skipped != 0 || len(runs) != 1 {
		t.Fatalf("%d runs, %d skipped, %v", len(runs), skipped, err)
	}
	var u earlierUndo
	var build string
	if ok, err := runs[0].Field("undo", &u); !ok || err != nil || u.How != "own" {
		t.Errorf("undo read as %+v (%v, %v), want the one under extra", u, ok, err)
	}
	if ok, err := runs[0].Field("build", &build); !ok || err != nil || build != "b1" || len(runs[0].Extra) != 2 {
		t.Errorf("build read as %q (%v, %v), extra %v: a name at the top alone is kept", build, ok, err, runs[0].Extra)
	}
	out, _, err := Lines(runs, nil)
	if err != nil || bytes.Contains(out, []byte("top")) || !bytes.Contains(out, []byte(`"extra":{"build":"b1","undo":{"how":"own"}}`)) {
		t.Errorf("the line written again is %s (%v)", out, err)
	}
}
