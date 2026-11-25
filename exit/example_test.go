package exit_test

import (
	"fmt"

	"github.com/birdtable/go-run/exit"
)

// A command gives its kinds once, builds the ending it reached from its table, prints its closing
// word, and exits with its code. The names, the words and the codes are the command's own.
func Example() {
	ends, err := exit.New(
		exit.Kind{Name: "done", Word: "done", Code: 0},
		exit.Kind{Name: "failed", Word: "failed", Code: 1},
		exit.Kind{Name: "not ready", Word: "not ready", Code: 4},
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	end := ends.End("not ready", "the database is still starting", "app wait")
	fmt.Println(end.Word(), end.Code())
	fmt.Println(end.What())
	fmt.Println(end.Next())
	// Output:
	// not ready 4
	// the database is still starting
	// app wait
}
