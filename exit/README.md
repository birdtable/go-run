# exit

A checked table of the ways a command can end.

[![Go reference](https://pkg.go.dev/badge/github.com/birdtable/go-run/exit.svg)](https://pkg.go.dev/github.com/birdtable/go-run/exit)

`exit` owns how a command ends, and none of what a command ends with. An `Ending` holds a closing word
together with its sentence, its line to type next and its exit code, and a `Table` builds one. The kinds of
ending, each with its name, its word and its code, are your program's own: you give them once, with no
default, and the package names no kind. There is no way to build an `Ending` outside a `Table`, so a
program that ends runs without giving its kinds does not build.

## Install

```sh
go get github.com/birdtable/go-run/exit
```

## Quick start

```go
package main

import (
	"fmt"
	"os"

	"github.com/birdtable/go-run/exit"
)

func main() {
	ends, err := exit.New(
		exit.Kind{Name: "done", Word: "done", Code: 0},
		exit.Kind{Name: "failed", Word: "failed", Code: 1},
		exit.Kind{Name: "not ready", Word: "not ready", Code: 4},
	)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	end := ends.End("not ready", "the database is still starting", "app wait")
	fmt.Println(end.Word(), end.Code())
	fmt.Println(end.What())
	fmt.Println(end.Next())
	os.Exit(end.Code())
}
```

It imports only the standard library, and no other package of this module imports it or is imported by
it. A daemon or a pure package that needs the codes takes this package alone.

## Giving the kinds

    ends, err := exit.New(exit.Kind{Name: "done", Word: "done", Code: 0}, exit.Kind{...})

`New` checks them and returns an error, which a program with fixed values reports at its start.
A `Kind` has three fields, all the caller's:

| field | meaning |
|---|---|
| Name | what the program calls the kind when it builds an ending |
| Word | the word a run of this kind closes on, word for word as it is printed |
| Code | the exit code of a run of this kind, from 0 to 255 |

A table is refused when:

- it holds no kind;
- a name or a word is empty or blank, or has a space at either end;
- two kinds have the same name, or the same word, so a column of words reads without counts or prose;
- a code is outside 0 to 255. Two codes may be the same: a program with only 0, 1 and 2 gives several
  kinds the same code, and the word tells them apart.

Which kinds, which names, words and codes is the program's choice, and so is whether a kind that
means "there is more" always has a line to type next. A table that passes `New` passes every check
in `table_test.go`: it runs on one table and on a second, unlike it, in
`secondconsumer_test.go`.

## Building an ending

    end := ends.End("not ready", "the database is still starting", "app wait")

`End` builds the ending of the kind with that name: the kind's word and code, the sentence, and the
line to type next, or `""` for none. A name the table does not hold builds the zero `Ending`, whose
code is 1, never a done. `Kinds` gives the kinds back, in the order they were given.

An `Ending` has no exported field, so a `Table` is the only way to build one. The zero `Ending`, which
none built, has code 1: it never claims a done that nobody promised, and 1 is the code every shell
reads as a failure. A zero `Table` builds the zero `Ending`.

A program that prints an ending can take any value with the four methods `Word`, `What`, `Next` and
`Code`, so the package that prints does not need to import this one.

## Built on, adds, configured by

**Built on:** the standard library alone.

**Adds:** what it lacks. `os.Exit` takes any int and names no code; this holds an `Ending` and a checked `Table`, so a closing word, its sentence, the line to type next and the exit code cannot be separated.

**Your program configures:** every kind of ending, by name, with its code and its closing word, given once to `New`. The package has no default and names no kind.

## Systems

Linux, macOS and Windows.
