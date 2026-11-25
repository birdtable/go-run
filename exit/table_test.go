package exit_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/birdtable/go-run/exit"
)

// ours is the kinds these tests run with: a caller's table, written here because the package holds
// none. The same checks run on a second table with other values in secondconsumer_test.go.
func ours() []exit.Kind {
	return []exit.Kind{
		{Name: "done", Word: "done", Code: 0},
		{Name: "needs you", Word: "needs you", Code: 0},
		{Name: "failed", Word: "failed", Code: 1},
		{Name: "findings", Word: "findings", Code: 1},
		{Name: "refused", Word: "refused", Code: 5},
	}
}

// holds runs every check of the package on the table made from kinds, and is the whole of what a
// second consumer must pass: End builds the word and the code of the kind it is given, with the
// sentence and the line given; the table gives its kinds back; a name it does not hold, and the zero
// Table, build an ending that is not a done.
func holds(t *testing.T, kinds []exit.Kind) {
	t.Helper()
	tbl, err := exit.New(kinds...)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tbl.Kinds(), kinds) {
		t.Errorf("the table gives back %+v, and was built from %+v", tbl.Kinds(), kinds)
	}
	for _, k := range kinds {
		e := tbl.End(k.Name, "what happened", "app next")
		if e.Word() != k.Word || e.Code() != k.Code || e.What() != "what happened" || e.Next() != "app next" {
			t.Errorf("End(%q) built %q, %q, %q with code %d; want %q, the sentence, the line, code %d", k.Name, e.Word(), e.What(), e.Next(), e.Code(), k.Word, k.Code)
		}
		if e := tbl.End(k.Name, "x", ""); e.Next() != "" {
			t.Errorf("End(%q) with no line built next %q", k.Name, e.Next())
		}
	}
	tbl.Kinds()[0].Code = 99
	if tbl.Kinds()[0] != kinds[0] {
		t.Errorf("a change to the slice Kinds gave changed the table")
	}
	var zero exit.Ending
	if zero.Code() != 1 || zero.Word() != "" || zero.Next() != "" {
		t.Errorf("the zero Ending: code %d, word %q, next %q", zero.Code(), zero.Word(), zero.Next())
	}
	for name, e := range map[string]exit.Ending{
		"a name the table does not hold": tbl.End("no such kind", "x", "y"),
		"a zero Table":                   exit.Table{}.End(kinds[0].Name, "x", "y"),
	} {
		if e.Code() == 0 {
			t.Errorf("%s built an ending with code %d", name, e.Code())
		}
	}
}

// End builds the word and the code of the kind it is given from the caller's table, keeps the
// sentence and the line given, and the zero Ending is not a done.
//
// What it costs when this fails: a run that closes on refused and exits 1, which a script retries;
// a run that said nothing and exits 0.
func TestEndBuildsTheKindsWordAndCodeFromTheTable(t *testing.T) {
	t.Parallel()
	holds(t, ours())
}

// New refuses a table that a script or a person could not read, and says which.
//
// What it costs when this fails: two kinds share a word and a column of words cannot be told apart,
// or a word or a name is blank, or a code is one no shell can exit with.
func TestNewRefusesATableThatCannotBeRead(t *testing.T) {
	t.Parallel()
	one := func(k exit.Kind) []exit.Kind { return append(ours(), k) }
	cases := map[string][]exit.Kind{
		"no kind at all":          nil,
		"a code past 255":         one(exit.Kind{Name: "x", Word: "x", Code: 256}),
		"a negative code":         one(exit.Kind{Name: "x", Word: "x", Code: -1}),
		"a blank name":            one(exit.Kind{Name: " ", Word: "x", Code: 1}),
		"an empty name":           one(exit.Kind{Name: "", Word: "x", Code: 1}),
		"a name with a space end": one(exit.Kind{Name: "x ", Word: "x", Code: 1}),
		"a blank word":            one(exit.Kind{Name: "x", Word: " ", Code: 1}),
		"an empty word":           one(exit.Kind{Name: "x", Word: "", Code: 1}),
		"a word with a space end": one(exit.Kind{Name: "x", Word: "x ", Code: 1}),
		"two words the same":      one(exit.Kind{Name: "x", Word: "failed", Code: 1}),
		"two names the same":      one(exit.Kind{Name: "failed", Word: "x", Code: 1}),
	}
	for name, kinds := range cases {
		if _, err := exit.New(kinds...); err == nil {
			t.Errorf("New accepted %s", name)
		}
	}
	if _, err := exit.New(exit.Kind{Name: "a", Word: "a", Code: 7}, exit.Kind{Name: "b", Word: "b", Code: 7}); err != nil {
		t.Errorf("New refused two kinds with the same code: %v", err)
	}
	if len(cases) != 11 {
		t.Fatalf("held %d cases", len(cases))
	}
}

// kindNames are the ten kinds go-run's exit once named, as an exported name each. None may
// come back: the package names no kind, and the kinds are the caller's.
func kindNames() []string {
	return []string{"Done", "NeedsYou", "StoppedAt", "Refused", "Usage", "Failed", "Findings", "Busy", "NotReady", "Declined"}
}

// exportedNames is every exported name that the Go source src declares: a type, a function, a method,
// a constant, a variable or a field.
func exportedNames(t *testing.T, name string, src any) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	ast.Inspect(f, func(n ast.Node) bool {
		var id *ast.Ident
		switch n := n.(type) {
		case *ast.FuncDecl:
			id = n.Name
		case *ast.TypeSpec:
			id = n.Name
		case *ast.ValueSpec:
			for _, v := range n.Names {
				if v.IsExported() {
					names = append(names, v.Name)
				}
			}
		case *ast.Field:
			for _, v := range n.Names {
				if v.IsExported() {
					names = append(names, v.Name)
				}
			}
		}
		if id != nil && id.IsExported() {
			names = append(names, id.Name)
		}
		return true
	})
	return names
}

// The package names no kind of ending and declares no code and no word: no exported name is one of the
// ten once shipped, and no constant or variable is exported.
//
// What it costs when this fails: one product family's idea of how a command ends is shipped as
// platform, and a second consumer is handed our kinds by default.
func TestThePackageNamesNoKindAndDeclaresNoCodeAndNoWord(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		read++
		for _, n := range exportedNames(t, f, nil) {
			if slices.Contains(kindNames(), n) {
				t.Errorf("%s declares %s, which names a kind of ending, and the kinds are the caller's", f, n)
			}
		}
	}
	if read == 0 {
		t.Fatal("exit has no Go files, so this proves little")
	}
	// The scan goes red on each way a kind comes back, so it proves something.
	for _, src := range []string{
		"package exit\nfunc (t Table) Done(what string) Ending { return Ending{} }\n",
		"package exit\ntype Codes struct{ Busy int }\n",
		"package exit\nconst Declined = 6\n",
		"package exit\nvar Failed = \"failed\"\n",
	} {
		hit := false
		for _, n := range exportedNames(t, "planted.go", src) {
			hit = hit || slices.Contains(kindNames(), n)
		}
		if !hit {
			t.Errorf("the scan missed a kind named back in %q", src)
		}
	}
	for _, src := range []string{"package exit\nconst Max = 255\n", "package exit\nvar Zero = \"\"\n"} {
		if len(exportedNames(t, "planted.go", src)) == 0 {
			t.Errorf("the scan did not see an exported constant or variable in %q", src)
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		fl, _ := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		for _, d := range fl.Decls {
			if g, ok := d.(*ast.GenDecl); ok && (g.Tok == token.CONST || g.Tok == token.VAR) {
				for _, s := range g.Specs {
					for _, n := range s.(*ast.ValueSpec).Names {
						if n.IsExported() {
							t.Errorf("%s: %s is an exported constant or variable, and a value a second consumer may want otherwise comes from the caller", f, n.Name)
						}
					}
				}
			}
		}
	}
}
