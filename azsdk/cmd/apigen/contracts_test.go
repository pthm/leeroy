package main

import "testing"

func TestTypedResultRegistry(t *testing.T) {
	for _, f := range []fnDoc{{Name: "spells.cast", Sig: "(id) → Request<CastDone>"}, {Name: "items.compare", Sig: "(item) → Request<Comparison>"}} {
		if err := validateTypedReturns([]fnDoc{f}, collect()); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []fnDoc{{Name: "spells.cast", Sig: "(id) → Request<Comparison>"}, {Name: "unknown", Sig: "() → Request<Spell>"}, {Name: "act.bad", Sig: "() → ActResult<Missing>"}} {
		if err := validateTypedReturns([]fnDoc{f}, collect()); err == nil {
			t.Fatal("invalid registry accepted", f)
		}
	}
}
