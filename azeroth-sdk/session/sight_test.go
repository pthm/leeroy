package session

import (
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/client"
	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type sightNav struct {
	navigation.Navigator
	clear bool
}

func (n sightNav) InLineOfSight(uint32, navigation.Point3D, navigation.Point3D) (bool, bool) {
	return n.clear, true
}

// a server line_of_sight refusal is recorded with both ends and the SDK's prediction; predicted is nil without
// collision evidence
func TestSightRefusedRecordsPrediction(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	var got []types.Diagnostic
	b.lua.SetDiagnosticSink("t", func(d types.Diagnostic) {
		if d.Kind == "sight_refused" {
			got = append(got, d)
		}
	})
	b.world.PutObjectForTest(&client.WorldObject{GUID: 42, PosX: 10, PosY: 2, PosZ: 3})
	b.nav = sightNav{clear: true}
	b.sightRefused(585, 42)
	b.nav = nil
	b.sightRefused(585, 42)
	if len(got) != 2 {
		t.Fatalf("%d sight_refused", len(got))
	}
	d := got[0].Details
	if d.SpellID != 585 || d.Target != "42" || d.Sight == nil || *d.Sight.Target != [3]float32{10, 2, 3} || d.Sight.Predicted == nil || !*d.Sight.Predicted {
		t.Fatalf("%+v %+v", d, d.Sight)
	}
	if got[1].Details.Sight.Predicted != nil {
		t.Fatal("a prediction without collision evidence")
	}
}
