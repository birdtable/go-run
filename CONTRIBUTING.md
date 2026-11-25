# Contributing

Issues and pull requests are welcome. For a bug, open an issue with the go-run version, the Go version
(`go version`), the system and the shortest program that shows it. For a feature, say what you are trying
to do, not only the API you want.

## Running the checks

The tools are pinned in `tools/` and built by `go tool` on first use, so nothing is installed on your
machine but Go.

```sh
git clone https://github.com/birdtable/go-run && cd go-run
go tool -modfile=tools/task.mod task hooks      # once per clone: points git at hooks/
go tool -modfile=tools/task.mod task pre-push   # format, vet, lint, tests, security scans, API check
```

`task pre-commit` is the quick set a commit runs. `task --list` shows the rest.

## Commits and pull requests

- One change to a pull request, with the tests that hold it.
- Say in the commit message what changed and why, in the imperative: "Refuse a link at the log's folder".
- A change a user can see adds a file to `.changes/unreleased/` (`changie new` makes one).
- A change to the exported API says so in the pull request, and `task api` must pass.
- Run `task pre-push` before you push: it is the check a change has to pass.
