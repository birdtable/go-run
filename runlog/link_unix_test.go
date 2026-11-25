//go:build unix

package runlog

import (
	"os"
	"testing"
)

// plantLink makes link a link to the folder target.
func plantLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
