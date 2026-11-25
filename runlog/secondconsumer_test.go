package runlog_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/birdtable/go-run/runlog"
)

// A second consumer: a program that runs a different business and journals its runs with fields
// nothing like ours. It has no copies and no way back, keeps a version and a
// ledger of its own through Extra, and sends the record through Begin, Change, Set and Finish
// without any value of ours. The package must build for it and pass what it passes for us.
//
// What it costs when this fails: the record holds a field of ours, a copy, a way back or a record
// version, so a consumer with other fields has to work around it or cannot use the package.
func TestSecondConsumer(t *testing.T) {
	t.Parallel()
	type order struct {
		SKU string `json:"sku"`
		Qty int    `json:"qty"`
	}
	log := runlog.Log{Path: filepath.Join(t.TempDir(), "shop", "ledger.jsonl")}
	h, err := log.Begin([]string{"shop", "ship"}, map[string]any{"schema": "ledger/9", "warehouse": "north"})
	if err != nil {
		t.Fatal(err)
	}
	h.Change("ship order 17")
	h.Set("orders", []order{{SKU: "tea", Qty: 4}})
	if err := h.Finish(70, "BROKE", "the carrier was down"); err != nil {
		t.Fatal(err)
	}
	runs, err := log.Read(t.Context())
	if err != nil || len(runs) != 1 {
		t.Fatalf("%d runs, %v", len(runs), err)
	}
	r := runs[0]
	var schema, warehouse string
	var orders []order
	okS, errS := r.Field("schema", &schema)
	okW, errW := r.Field("warehouse", &warehouse)
	okO, errO := r.Field("orders", &orders)
	if !okS || !okW || !okO || errS != nil || errW != nil || errO != nil || schema != "ledger/9" || warehouse != "north" || !slices.Equal(orders, []order{{SKU: "tea", Qty: 4}}) {
		t.Errorf("the consumer's fields read back as %q, %q, %+v (%v, %v, %v)", schema, warehouse, orders, errS, errW, errO)
	}
	if r.Code != 70 || r.Outcome != "BROKE" || r.Summary != "the carrier was down" || r.Interrupted() || !slices.Equal(r.Changes, []string{"ship order 17"}) {
		t.Errorf("the run read back as %+v", r)
	}
}

// The second consumer keeps a list of its own type and adds to it as the run goes: it holds the
// list, and sets the field again with the longer one. Set replaces the field whole, so the record
// reads back as the second list, through Record.Field into the consumer's own type, with no list
// type, no append and no merge of ours in between.
//
// What it costs when this fails: a consumer that keeps a list (the copies a run made, the orders it
// shipped) reads back the first list, both lists joined or a shape of ours, and has to keep its
// list somewhere else.
func TestSecondConsumerKeepsAListOfItsOwnBySettingItAgain(t *testing.T) {
	t.Parallel()
	type parcel struct {
		Order  int    `json:"order"`
		Label  string `json:"label"`
		Weight int    `json:"weight,omitempty"`
	}
	log := runlog.Log{Path: filepath.Join(t.TempDir(), "shop", "ledger.jsonl")}
	h, err := log.Begin([]string{"shop", "pack"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	parcels := []parcel{{Order: 17, Label: "north-1"}}
	h.Set("parcels", parcels)
	parcels[0].Weight = 900 // the same parcel, weighed: the entry is replaced, not added
	parcels = append(parcels, parcel{Order: 18, Label: "north-2"})
	h.Set("parcels", parcels)
	if err := h.Finish(0, "SHIPPED", "two parcels"); err != nil {
		t.Fatal(err)
	}
	r, err := log.Find(t.Context(), h.ID())
	if err != nil {
		t.Fatal(err)
	}
	var got []parcel
	want := []parcel{{Order: 17, Label: "north-1", Weight: 900}, {Order: 18, Label: "north-2"}}
	if ok, err := r.Field("parcels", &got); !ok || err != nil || !slices.Equal(got, want) || len(r.Extra) != 1 {
		t.Errorf("the list read back as %+v (%v, %v), extra %v, want %+v", got, ok, err, r.Extra, want)
	}
}

// A second consumer adds to a run one entry at a time, of a type of its own: from the process that began the
// run, through its Handle, and from a later process that holds only the run's id. Every entry
// reads back in the order written, with its time, as that type.
//
// What it costs when this fails: a consumer whose run has hundreds of entries writes its whole list again for
// each, or cannot add one from a process that did not begin the run.
func TestSecondConsumerAddsEntriesOfItsOwnTypeWithAndWithoutAHandle(t *testing.T) {
	t.Parallel()
	type scan struct {
		Parcel int    `json:"parcel"`
		Dock   string `json:"dock"`
	}
	log := runlog.Log{Path: filepath.Join(t.TempDir(), "shop", "ledger.jsonl")}
	h, err := log.Begin([]string{"shop", "load"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []scan{{17, "north"}, {18, "north"}, {19, "east"}}
	h.Entry(want[0])
	later := runlog.Log{Path: log.Path} // another process: it has the path and the id, and no Handle
	if err := later.Entry(t.Context(), h.ID(), want[1]); err != nil {
		t.Fatal(err)
	}
	h.Entry(want[2])
	if err := h.Finish(0, "LOADED", "three parcels"); err != nil {
		t.Fatal(err)
	}
	r, err := later.Find(t.Context(), h.ID())
	if err != nil || len(r.Entries) != len(want) || r.Outcome != "LOADED" || len(r.Extra) != 0 {
		t.Fatalf("found %+v, %v, want the run with three entries", r, err)
	}
	for i, e := range r.Entries {
		var got scan
		if err := e.Decode(&got); err != nil || got != want[i] || e.Time.Before(r.Started) || e.Time.After(r.Finished) {
			t.Errorf("entry %d reads back as %+v at %v (%v), want %+v within the run", i, got, e.Time, err, want[i])
		}
	}
}
