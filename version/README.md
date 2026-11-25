# version

Which build a program is, as `v0.1.0 (712a01e) go1.27.3`.

[![Go reference](https://pkg.go.dev/badge/github.com/birdtable/go-run/version.svg)](https://pkg.go.dev/github.com/birdtable/go-run/version)

`Read()` takes what Go records in the binary, kept as it was given, and `With(Stamp{...})` lays over it what
a build script stamped with `-ldflags -X`. A `Build` has three fields, `Version`, `Commit` and `Go` (the
toolchain), and marshals with the keys `version`, `commit` and `go`. A build with no tag says `(devel)`, and
its version is never empty.

## Install

```sh
go get github.com/birdtable/go-run/version
```

## Quick start

```go
package main

import (
	"fmt"

	"github.com/birdtable/go-run/version"
)

// Set by the build script with -ldflags -X.
var stamped, commit string

func main() {
	build := version.Read().With(version.Stamp{Version: stamped, Commit: commit})
	fmt.Println(build)
}
```

The stamp wins where it is set and Go's record stands otherwise. `Commit` is the whole revision, and
`String` prints its first seven characters. A build no tag reaches keeps the version Go recorded, a
pseudo-version or one ending in `+dirty`. A script that reads the line takes the first word as the version.

It is for a program's own build line. A program that only records runs or ends them takes `runlog` and
`exit` and never this one. It imports the standard library, and `golang.org/x/mod/module` to read the
commit a pseudo-version names.

## Built on, adds, configured by

**Built on:** the standard library's `runtime/debug.ReadBuildInfo` and `golang.org/x/mod/module`.

**Adds:** one `Build` value that lays a build script's stamp over what the toolchain recorded, field by field.

**Your program configures:** the stamp, set with `-ldflags -X`. A program that stamps nothing calls `Read` and is done.

## Systems

Linux, macOS and Windows.
