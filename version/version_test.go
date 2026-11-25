package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// rev is a full revision, as vcs.revision records one.
const rev = "712a01e5f0c0a3b1c2d3e4f5a6b7c8d9e0f1a2b3"

// info is a BuildInfo of the main module example.com/app at version main, built with go1.27.3, with
// settings as key and value pairs.
func info(main string, settings ...string) *debug.BuildInfo {
	i := &debug.BuildInfo{GoVersion: "go1.27.3", Main: debug.Module{Path: "example.com/app", Version: main}}
	for k := 0; k+1 < len(settings); k += 2 {
		i.Settings = append(i.Settings, debug.BuildSetting{Key: settings[k], Value: settings[k+1]})
	}
	return i
}

// From keeps the version Go recorded as it was given, and takes the full commit from vcs.revision
// or, with none, from a pseudo-version: a tagged release, past a tag, a dirty tree, a go install of a
// tag and of a branch (no vcs settings), (devel), and no build info at all.
//
// What it costs when this fails: a binary says a version other than the one Go recorded, loses the
// commit a go install named, or leaves out its toolchain, and a bug report names code nobody shipped.
func TestFromKeepsTheVersionAsGoRecordedIt(t *testing.T) {
	t.Parallel()
	ran := 0
	for _, c := range []struct {
		name, version, commit, str string
		info                       *debug.BuildInfo
	}{
		{"a tagged release", "v0.2.0", rev, "v0.2.0 (712a01e) go1.27.3", info("v0.2.0", "vcs.revision", rev)},
		{"a pseudo-version", "v0.2.1-0.20261014120000-712a01e5f0c0", rev, "v0.2.1-0.20261014120000-712a01e5f0c0 go1.27.3",
			info("v0.2.1-0.20261014120000-712a01e5f0c0", "vcs.revision", rev)},
		{"a dirty tree", "v0.2.0+dirty", rev, "v0.2.0+dirty (712a01e) go1.27.3", info("v0.2.0+dirty", "vcs.revision", rev, "vcs.modified", "true")},
		{"no vcs stamp", "v1.0.0", "", "v1.0.0 go1.27.3", info("v1.0.0")},
		{"a go install of a branch", "v0.0.0-20261113090000-712a01e5f0c0", "712a01e5f0c0", "v0.0.0-20261113090000-712a01e5f0c0 go1.27.3",
			info("v0.0.0-20261113090000-712a01e5f0c0")},
		{"a build of the checkout", Devel, rev, Devel + " (712a01e) go1.27.3", info(Devel, "vcs.revision", rev)},
		{"an empty version", Devel, "", Devel + " go1.27.3", info("")},
		{"no build info", Devel, "", Devel + " " + runtime.Version(), nil},
	} {
		ran++
		b := From(c.info)
		if b.Version != c.version || b.Commit != c.commit || b.Go != Go(c.info) || b.String() != c.str {
			t.Errorf("%s: %+v says %q, want version %q, commit %q, %q", c.name, b, b.String(), c.version, c.commit, c.str)
		}
	}
	if ran != 8 {
		t.Fatalf("checked %d builds, want 8", ran)
	}
}

// Go is the toolchain a build info names, the running one when it names none.
func Go(i *debug.BuildInfo) string {
	if i == nil {
		return runtime.Version()
	}
	return i.GoVersion
}

// A stamp lays its non-empty fields over Go's record: an empty stamp changes nothing, a version
// alone keeps Go's commit, and a stamped commit is printed short.
//
// What it costs when this fails: a stamped binary loses the commit Go recorded, or prints the
// version Go recorded over the one a build script stamped.
func TestAStampLaysItsFieldsOverGosRecord(t *testing.T) {
	t.Parallel()
	base := From(info("v0.2.1-0.20261014120000-712a01e5f0c0", "vcs.revision", rev))
	ran := 0
	for _, c := range []struct {
		name            string
		stamp           Stamp
		version, commit string
	}{
		{"an empty stamp", Stamp{}, base.Version, rev},
		{"a version alone", Stamp{Version: "v0.3.0"}, "v0.3.0", rev},
		{"a version and a commit", Stamp{Version: "1.2.3", Commit: "abcdef0123456789"}, "1.2.3", "abcdef0123456789"},
	} {
		ran++
		if b := base.With(c.stamp); b.Version != c.version || b.Commit != c.commit || b.Go != base.Go {
			t.Errorf("%s: %+v, want version %q and commit %q", c.name, b, c.version, c.commit)
		}
	}
	if ran != 3 {
		t.Fatalf("checked %d stamps, want 3", ran)
	}
	if s := (Build{Version: "v1.0.0", Commit: "abcdef0123456789", Go: "go1.27.3"}).String(); s != "v1.0.0 (abcdef0) go1.27.3" {
		t.Errorf("String of a stamped commit is %q", s)
	}
}

// String of a build with no tag says "devel" and the commit, never an empty version, always the
// toolchain, and names a commit once when the version holds it already.
//
// What it costs when this fails: a --version line is empty, says nothing of the toolchain, or names
// the commit twice.
func TestStringNeverPrintsAnEmptyVersionAndAlwaysNamesTheToolchain(t *testing.T) {
	t.Parallel()
	for _, b := range []Build{{Go: "go1.27.3"}, {Commit: rev, Go: "go1.27.3"}, {Version: Devel, Commit: rev, Go: "go1.27.3"}} {
		if s := b.String(); !strings.Contains(s, "devel") || !strings.HasSuffix(s, " go1.27.3") {
			t.Errorf("%+v prints %q", b, s)
		}
	}
	if s := (Build{Version: "v0.0.0-712a01e", Commit: rev, Go: "go1.27.3"}).String(); s != "v0.0.0-712a01e go1.27.3" {
		t.Errorf("a version that holds the commit says %q", s)
	}
}

// Read is this test binary's build: a toolchain that starts go, and a version that is never empty.
//
// What it costs when this fails: a program that stamps nothing prints an empty version.
func TestReadIsTheRunningBinarysBuild(t *testing.T) {
	t.Parallel()
	b := Read()
	if b.Version == "" || !strings.HasPrefix(b.Go, "go") || !strings.HasSuffix(b.String(), b.Go) {
		t.Errorf("Read: %+v", b)
	}
}

// Two answers of Read().With(Stamp{...}) are held here, a row each: a binary with no build info and
// no stamp is Devel, "(devel)"; and a stamped version with no stamped commit keeps the commit Go
// recorded. The commit is kept whole, the version is kept as Go recorded it, and the toolchain is the
// build's third field, which the tests above hold.
//
// What it costs when this fails: a script that parses the version line breaks on the first build
// with no tag, or a stamped build loses the commit Go recorded.
func TestDevelAndTheCommitGoRecorded(t *testing.T) {
	t.Parallel()
	ran := 0
	for _, c := range []struct {
		name            string
		info            *debug.BuildInfo
		stamp           Stamp
		version, commit string
	}{
		{"no build info and no stamp", nil, Stamp{}, Devel, ""},
		{"a stamped version and no stamped commit", info("v0.2.0", "vcs.revision", rev), Stamp{Version: "v0.3.0"}, "v0.3.0", rev},
	} {
		ran++
		b := From(c.info).With(c.stamp)
		if b.Version != c.version || b.Commit != c.commit {
			t.Errorf("%s: %+v, want version %q and commit %q", c.name, b, c.version, c.commit)
		}
	}
	if ran != 2 {
		t.Fatalf("checked %d differences, want 2", ran)
	}
}
