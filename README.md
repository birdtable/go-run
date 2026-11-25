<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/mark/go-run-banner-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/mark/go-run-banner-light.svg">
    <img src="docs/mark/go-run-banner-light.svg" width="267" alt="go-run">
  </picture>
</p>

# go-run

How a command ends, and what it leaves behind.

[![Latest release](https://img.shields.io/github/v/release/birdtable/go-run)](https://github.com/birdtable/go-run/releases)
[![Go reference](https://pkg.go.dev/badge/github.com/birdtable/go-run.svg)](https://pkg.go.dev/github.com/birdtable/go-run)
[![Licence](https://img.shields.io/github/license/birdtable/go-run)](LICENSE)

go-run is the small plumbing a command-line program shares at the two ends of a run. It gives your
program a checked table of how it can end, a journal of the runs that changed something, and one value
that says which build it is. It has no binary and no opinion about what your program does.

## Install

```sh
go get github.com/birdtable/go-run/exit
go get github.com/birdtable/go-run/runlog
go get github.com/birdtable/go-run/version
```

## Quick start

Record a run in a journal you can read with `tail`, then read it back:

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

## What's inside

- [`exit`](exit/README.md): gives a command a checked table of the ways it can end: a closing word, a sentence, the line to type next and an exit code.
- [`runlog`](runlog/README.md): an append-only journal of the runs of a program that change something: one JSON line per update, in a file a person can read.
- [`version`](version/README.md): says which build a program is, as `v0.1.0 (712a01e) go1.27.3`, from what Go records in the binary and what your build stamped over it.

What each package is built on, what it adds and what your program configures is in its README and its package doc.

Each package is a leaf: `exit` imports the standard library alone, and none imports another. A program
that only records runs or ends them takes `exit` and `runlog` and never links `version`. Full reference
is on [pkg.go.dev](https://pkg.go.dev/github.com/birdtable/go-run).

## Dependencies

The module requires four modules, each named here with what it is for:

- [`go-files`](https://pkg.go.dev/github.com/birdtable/go-files): `runlog` writes each line through its `write.Append`.
- [`golang.org/x/mod`](https://pkg.go.dev/golang.org/x/mod): `version` reads the commit a pseudo-version names with its `module` package.
- [`golang.org/x/sys`](https://pkg.go.dev/golang.org/x/sys): only `runlog`'s Windows tests and the repository's own tools use it. A program that imports go-run does not link it.
- [`go-testkit`](https://pkg.go.dev/github.com/birdtable/go-testkit): only the tests and the repository's tools use it. A program that imports go-run does not link it.

`exit` imports the standard library alone.

## Systems

Linux, macOS and Windows, on amd64 and arm64.

## Stability

v0: the API may change between minor versions.

## Contributing

Issues and pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) says how to run the checks.

## Reporting issues

A bug or a feature request goes in an [issue](https://github.com/birdtable/go-run/issues/new/choose). There is no promised
time for an answer. A security problem never goes in a public issue: report it privately, as [SECURITY.md](SECURITY.md) says.

## Security

Please report a vulnerability privately. [SECURITY.md](SECURITY.md) says how.

## License

Apache-2.0. See [LICENSE](LICENSE).
