package exit_test

import (
	"testing"

	"github.com/birdtable/go-run/exit"
)

// A second consumer: a program that runs a different business and ends its runs with kinds, codes and
// words nothing like ours, in another language, with three kinds and the codes 0,
// 1 and 2. It gives no value of ours, and the package must build for it and pass every check
// it passes for us.
//
// What it costs when this fails: the package holds a kind, a code or a word of ours, or a rule about
// them, so a consumer with other ones has to work around it or cannot use it.
func TestSecondConsumer(t *testing.T) {
	t.Parallel()
	kinds := []exit.Kind{
		{Name: "fait", Word: "FAIT", Code: 0},
		{Name: "echec", Word: "ECHEC", Code: 1},
		{Name: "refus", Word: "REFUSE", Code: 2},
	}
	holds(t, kinds)
	tbl, err := exit.New(kinds...)
	if err != nil {
		t.Fatal(err)
	}
	if e := tbl.End("refus", "option inconnue", "boutique --aide"); e.Word() != "REFUSE" || e.Code() != 2 || e.Next() != "boutique --aide" {
		t.Errorf("refus built %q with code %d and next %q", e.Word(), e.Code(), e.Next())
	}
}
