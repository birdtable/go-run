// Package version says which build a program is: the version, the commit and the Go toolchain, as
// one plain value that prints for people. It is built on runtime/debug.ReadBuildInfo and
// golang.org/x/mod/module, and adds the one Build value that lays a build script's stamp over what
// the toolchain recorded; the program configures that stamp, set with -ldflags -X.
//
// Read takes what the toolchain records in every binary, and With lays the stamp over it, field by
// field. A program that stamps nothing calls Read and is done. Versions are kept as they were given,
// never rewritten: a pseudo-version, a pre-release or a "+dirty" says more than any word could.
package version

import (
	"runtime"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
)

// Build is one build of a program. It is plain data.
type Build struct {
	// Version is the stamped version, else the one Go recorded, as it was given; Devel when neither
	// names one. It is never empty.
	Version string `json:"version"`
	// Commit is the full revision, "" when nothing recorded one.
	Commit string `json:"commit"`
	// Go is the toolchain the binary was built with, as go1.27.3.
	Go string `json:"go"`
}

// Stamp is what a build script set with -ldflags -X. Its zero value is nothing stamped.
type Stamp struct {
	Version, Commit string
}

// Devel is Version when neither a stamp nor the toolchain names one, the mark of a build with no
// tag. It is Go's own word.
const Devel = "(devel)"

// Read is the running binary's build, from what the toolchain recorded in it.
func Read() Build {
	info, _ := debug.ReadBuildInfo()
	return From(info)
}

// From is the build info describes. A nil info is a binary with no build info: Devel, no commit,
// and the running toolchain. From reads nothing else, so a test drives it with a BuildInfo of its
// own.
func From(info *debug.BuildInfo) Build {
	b := Build{Version: Devel, Go: runtime.Version()}
	if info == nil {
		return b
	}
	if info.GoVersion != "" {
		b.Go = info.GoVersion
	}
	if v := info.Main.Version; v != "" {
		b.Version = v
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			b.Commit = s.Value
		}
	}
	if b.Commit == "" && module.IsPseudoVersion(b.Version) {
		// A go install of a branch records no vcs settings; its version is the one place its
		// commit is written.
		if rev, err := module.PseudoVersionRev(b.Version); err == nil {
			b.Commit = rev
		}
	}
	return b
}

// With is b with each non-empty field of s in place of b's: a stamped version over Go's, and Go's
// commit kept when the stamp names none.
func (b Build) With(s Stamp) Build {
	if s.Version != "" {
		b.Version = s.Version
	}
	if s.Commit != "" {
		b.Commit = s.Commit
	}
	return b
}

// String is the build as people read it, "v1.2.3 (712a01e) go1.27.3": the version, the first seven
// characters of the commit, and the toolchain. A build with no tag says "(devel)" and its commit;
// the commit is left out when there is none, or when the version already holds it.
func (b Build) String() string {
	version := b.Version
	if version == "" {
		version = Devel
	}
	short := b.Commit[:min(len(b.Commit), 7)]
	if short != "" && !strings.Contains(version, short) {
		version += " (" + short + ")"
	}
	return version + " " + b.Go
}
