// Package exit owns how a command ends: the Ending that holds a closing word with its sentence, its
// line to type next and its exit code, built only by a Table, so the four cannot be separated. It is
// built on the standard library alone and adds what it lacks: os.Exit takes any int and names no
// code, and this holds an Ending and a checked Table. The program configures every kind of ending,
// by name, with its code and its closing word, given once to New with no default: the package names no
// kind, so a program that ends runs without giving its kinds does not build. A daemon or a pure
// package takes it without the terminal look. README.md says what a table must hold, and the tests
// hold a table to it.
package exit

import (
	"fmt"
	"strings"
)

// A Kind is one way a command of the caller ends, all of it the caller's own: Name is what the
// caller calls it when it builds an Ending, Word is the word a run of that kind closes on, and Code
// is its exit code, from 0 to 255. Codes may repeat: a program with only the codes 0, 1 and 2 gives
// several kinds the same code, and the word tells them apart.
type Kind struct {
	Name string
	Word string
	Code int
}

// A Table is a program's kinds, checked once by New. Its End method is the only way to build an
// Ending. The zero Table builds the zero Ending.
type Table struct {
	kinds []Kind
}

// New checks the kinds and returns the table that builds endings from them. It is an error to give
// none, a name or a word that is blank or has a space at either end, two kinds with the same name or
// the same word, or a code outside 0 to 255. Two codes may be the same.
func New(kinds ...Kind) (Table, error) {
	if len(kinds) == 0 {
		return Table{}, fmt.Errorf("exit: no kind of ending was given, and a table with none builds nothing")
	}
	names, words := map[string]bool{}, map[string]string{}
	for _, k := range kinds {
		if k.Name == "" || strings.TrimSpace(k.Name) != k.Name {
			return Table{}, fmt.Errorf("exit: the name of a kind is %q, and a name is not blank and has no space at either end", k.Name)
		}
		if names[k.Name] {
			return Table{}, fmt.Errorf("exit: two kinds are both named %q", k.Name)
		}
		names[k.Name] = true
		if k.Word == "" || strings.TrimSpace(k.Word) != k.Word {
			return Table{}, fmt.Errorf("exit: the word for %s is %q, and a word is not blank and has no space at either end", k.Name, k.Word)
		}
		if other, twice := words[k.Word]; twice {
			return Table{}, fmt.Errorf("exit: %s and %s are both the word %q", other, k.Name, k.Word)
		}
		words[k.Word] = k.Name
		if k.Code < 0 || k.Code > 255 {
			return Table{}, fmt.Errorf("exit: %s is code %d, and a code is from 0 to 255", k.Name, k.Code)
		}
	}
	return Table{kinds: append([]Kind(nil), kinds...)}, nil
}

// Kinds is the kinds the table was built from, in the order they were given. The slice is a copy.
func (t Table) Kinds() []Kind { return append([]Kind(nil), t.kinds...) }

// End builds the ending of the kind with that name: its word and its code, the sentence what, which
// is in the run's own terms with no full stop, and next, the line to type next or "" for none. A
// name the table does not hold builds the zero Ending, which is not a done.
func (t Table) End(kind, what, next string) Ending {
	for _, k := range t.kinds {
		if k.Name == kind {
			return Ending{word: k.Word, what: what, next: next, code: k.Code}
		}
	}
	return Ending{}
}

// An Ending is the last thing a run says: its word, the sentence beside it,
// the line to type next when there is one, and its exit code.
//
// Its fields are unexported, so it is built only by a Table. The zero Ending was built by none: its
// code is 1, never a done that nobody promised.
type Ending struct {
	word, what, next string
	code             int
}

// Word is the closing word.
func (e Ending) Word() string { return e.word }

// What is the sentence beside the word, in the run's own terms, with no full
// stop.
func (e Ending) What() string { return e.what }

// Next is the line to type next, or "" for an ending that has none.
func (e Ending) Next() string { return e.next }

// Code is the exit code the ending stands for. The zero Ending, built by no
// table, is 1: any code but 0 reads as not done, and 1 is the one every shell
// and script already reads as a failure.
func (e Ending) Code() int {
	if e.word == "" {
		return 1
	}
	return e.code
}
