package runlog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// noRefs matches what a reader of the package's docs has no use for: an issue or pull request number,
// a rule id such as G3 or cli.config.3, or a design document's number.
var noRefs = regexp.MustCompile(`#[0-9]+|\bG[0-9]+\b|\b[a-z]+\.[a-z]+\.[0-9]+\b|\bdesign [0-9]{3}\b`)

// The README and the package's Go files name no issue, rule or design number, and the planted lines
// with one are each found.
//
// What it costs when this fails: a stranger reading the package's docs meets a reference to a tracker
// or a rulebook they cannot open, in place of what the package does.
func TestTheDocsNameNoIssueRuleOrDesignNumber(t *testing.T) {
	t.Parallel()
	read := 0
	for _, name := range []string{"README.md", "run.go", "log.go", "find.go", "os_other.go", "os_windows.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		read++
		if m := noRefs.FindAll(b, -1); len(m) != 0 {
			t.Errorf("%s names %q", name, m)
		}
	}
	planted := 0
	for _, line := range []string{"as proj#107 says", "the rule (G3)", "read in the config package (cli.config.3)", "design 001 has it"} {
		planted++
		if !noRefs.MatchString(line) {
			t.Errorf("the planted line %q is not found", line)
		}
	}
	if read != 6 || planted != 4 || noRefs.MatchString("a record of version 3, 16 hex characters, mode 0600") {
		t.Fatalf("read %d files and %d planted lines, or a plain line was found", read, planted)
	}
}

// The first Go block of the root README, with its package clause and its main function renamed for an
// example, is the file quickstart_test.go byte for byte.
//
// What it costs when this fails: the README's quick start is a program nobody builds, and a reader who
// copies it meets an error before the first run.
func TestTheReadmeQuickStartIsThisExample(t *testing.T) {
	t.Parallel()
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(readme), "## Quick start")
	if !ok {
		t.Fatal("the README has no Quick start")
	}
	_, block, ok := strings.Cut(after, "```go\n")
	if !ok {
		t.Fatal("the Quick start has no Go block")
	}
	block, _, ok = strings.Cut(block, "```")
	if !ok {
		t.Fatal("the Quick start's Go block does not end")
	}
	got := strings.Replace(block, "package main", "package runlog_test", 1)
	got = strings.Replace(got, "func main() {", "func Example_quickStart() {", 1)
	file, err := os.ReadFile("quickstart_test.go")
	if err != nil {
		t.Fatal(err)
	}
	// The example file says what it is above its function; the README block starts at the package clause.
	want := strings.Replace(string(file), "// The quick start of the root README, word for word: TestTheReadmeQuickStartIsThisExample holds the two\n// the same, so a reader who copies it gets a program that builds and prints what the README says.\n", "", 1)
	if got != want {
		t.Errorf("the README's quick start and runlog/quickstart_test.go differ:\n--- README\n%s\n--- file\n%s", got, want)
	}
}
