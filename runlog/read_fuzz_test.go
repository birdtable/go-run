package runlog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fuzzMaxLine is the longest line FuzzRead reads, short so a seed can hold a line past it.
const fuzzMaxLine = 4096

// A log is read back whatever is in it, as a damaged or an older one may be: Read never panics, and
// says no error at all;
// skips a line longer than its MaxLine, a line with a name twice and a torn last one, counting each;
// and gives each run once, with an id, as a line that writes the same line again when read back.
// The seeds in testdata/fuzz/FuzzRead run as tests in every go test; a fuzz run is on demand
// (go test -fuzz FuzzRead).
//
// What it costs when this fails: a program's undo, or its list of runs, stops on a log a power cut
// left or one another release wrote.
func FuzzRead(f *testing.F) {
	f.Fuzz(func(t *testing.T, b []byte) {
		path := filepath.Join(t.TempDir(), "runs.jsonl")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		var skipped int
		runs, err := Log{Path: path, MaxLine: fuzzMaxLine, Skipped: &skipped}.Read(t.Context())
		if err != nil || skipped < 0 || skipped > bytes.Count(b, []byte("\n"))+1 {
			t.Fatalf("a log of %d bytes: %d skipped, %v", len(b), skipped, err)
		}
		seen := map[string]bool{}
		for _, r := range runs {
			if r.ID == "" || seen[r.ID] {
				t.Errorf("the run %q has no id or was read twice", r.ID)
			}
			seen[r.ID] = true
			once, err := marshal(r)
			if err != nil {
				t.Errorf("the run %q read back cannot be written: %v", r.ID, err)
				continue
			}
			again, skipped, err := readFrom(t.Context(), bytes.NewReader(append(once, '\n')), 0)
			if err != nil || skipped != 0 || len(again) != 1 {
				t.Errorf("the line written for the run %q cannot be read (%v, %d skipped, %d runs):\n%s", r.ID, err, skipped, len(again), once)
				continue
			}
			if twice, err := marshal(again[0]); err != nil || !bytes.Equal(once, twice) {
				t.Errorf("the run %q read back writes another line (%v):\n%s\n%s", r.ID, err, once, twice)
			}
		}
		lines, _, err := Lines(runs, nil)
		back, skipped, rerr := readFrom(t.Context(), bytes.NewReader(lines), 0)
		if err != nil || rerr != nil || skipped != 0 || len(back) != len(runs) {
			t.Fatalf("the runs written by Lines read back as %d of %d, %d skipped (%v, %v)", len(back), len(runs), skipped, err, rerr)
		}
		for i := range runs {
			if len(back[i].Entries) != len(runs[i].Entries) {
				t.Errorf("the run %q has %d entries and %d after Lines", runs[i].ID, len(runs[i].Entries), len(back[i].Entries))
			}
		}
	})
}

// Every seed is there, so a plain go test reads each shape: go test runs what
// is in the folder and says nothing of what is gone from it.
//
// What it costs when this fails: a seed is lost, and a parser that mishandles
// its shape is green.
func TestEverySeedOfTheReadersFuzzIsThere(t *testing.T) {
	t.Parallel()
	// theSeeds is every seed of FuzzRead, by its file's name: each a shape a
	// log is known to come in, damaged, older or another program's.
	theSeeds := []string{
		"a-duplicate-key", "a-duplicate-key-in-extra", "a-field-named-in-another-case", "a-field-of-another-type",
		"a-kelvin-sign-kept", "a-line-too-long", "a-line-with-no-id", "a-number-too-big", "a-torn-last-line",
		"a-torn-last-line-after-a-whole-one", "a-version-2-line", "a-whole-run", "an-empty-log", "an-entry-line", "an-older-version",
		"an-opening-then-a-closing", "an-unknown-top-level-member",
		"crlf-and-blank-lines", "html-in-an-own-field", "nested-too-deep", "not-an-object",
		"not-utf-8", "own-fields-beside",
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "fuzz", "FuzzRead"))
	if err != nil {
		t.Fatal(err)
	}
	there, listed := map[string]bool{}, 0
	for _, e := range entries {
		there[e.Name()] = true
		listed++
	}
	for _, name := range theSeeds {
		if !there[name] {
			t.Errorf("the seed %s is not in testdata/fuzz/FuzzRead", name)
		}
	}
	if len(theSeeds) == 0 || listed < len(theSeeds) {
		t.Fatalf("%d seeds are named and %d are there", len(theSeeds), listed)
	}
}

// A key named like one of the record's own in another case, as encoding/json
// would match it ("Kept", "UNDO", the Kelvin sign's "Kept", "Extra"), is
// never read as the record's, wherever it is in the line: it is a name the record does not have, so
// it is kept in Extra under the name as the line spells it, beside the caller's own under extra and
// never in their place. A name twice in a line is a line the reader skips, so each such key is there
// once.
//
// What it costs when this fails: a line's key decides what the run changed or what a program put in
// its own fields, so a program acts on a field the run never wrote, or the run's last lines are not
// read and it shows as cut off.
func TestAKeyLikeTheRecordsOwnInAnotherCaseIsNotRead(t *testing.T) {
	t.Parallel()
	l := logIn(t)
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"version":3,"id":"a1","argv":["app"],"started":"2026-11-10T09:00:00Z","finished":"2026-11-10T09:00:01Z","code":0,"changes":["write /etc/app.conf"],` +
		`"kept":[{"file":"/etc/app.conf","copy":"/var/lib/app/kept/a1/app.conf"}],"undo":[{"how":"copy_back"}],"extra":{"stack":"app"},` +
		`"Kept":[{"file":"/etc/other","copy":"/tmp/other"}],"Kept":[{"file":"/etc/third","copy":"/tmp/third"}],"UNDO":[{"how":"printed","lines":["rm -r /"]}],` +
		`"Changes":[],"ID":"b2","Extra":{"stack":"other"},"images":[]}` + "\n"
	if err := os.WriteFile(l.Path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, err := l.Read(t.Context())
	if err != nil || len(runs) != 1 {
		t.Fatalf("%v, %d runs", err, len(runs))
	}
	r := runs[0]
	if r.ID != "a1" || len(r.Changes) != 1 || r.Changes[0] != "write /etc/app.conf" {
		t.Errorf("the record's own fields were read from other keys: %+v", r)
	}
	var stack, id string
	if ok, err := r.Field("stack", &stack); !ok || err != nil || stack != "app" || len(r.Extra) != 11 {
		t.Errorf("the caller's fields are %v (stack %q, %v), want stack from under extra and 11 names", r.Extra, stack, err)
	}
	if ok, err := r.Field("ID", &id); !ok || err != nil || id != "b2" {
		t.Errorf("the key ID read as %q (%v, %v), want it kept under its own spelling", id, ok, err)
	}
	checked := 0
	for _, name := range []string{"Kept", "\u212aept", "UNDO", "Changes", "Extra", "images", "kept", "undo", "version"} {
		checked++
		if _, ok := r.Extra[name]; !ok {
			t.Errorf("the key %q is not kept", name)
		}
	}
	if checked != 9 {
		t.Fatalf("checked %d names, want 9", checked)
	}
}
