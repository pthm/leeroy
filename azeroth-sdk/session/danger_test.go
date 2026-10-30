package session

import (
	"context"
	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/types"
	"testing"
)

func TestDangerRequiresLocalFloorSightAndConnection(t *testing.T) {
	mob := types.Creature{Unit: types.Unit{Alive: true, Reaction: "hostile", Place: types.Place{X: 10}}, Attackable: true}
	source := dangerSource{path: func(_ context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
		return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
	}, sight: func(context.Context, uint32, navigation.Point3D, navigation.Point3D) (bool, bool) { return true, true }}
	for _, tc := range []struct {
		name  string
		point types.Position
		edit  func(*dangerSource)
		want  string
	}{
		{"clear", types.Position{}, nil, "clear"},
		{"hostile", types.Position{}, nil, "hostile"},
		{"remote", types.Position{X: 100}, nil, "unknown"},
		{"no collision", types.Position{}, func(s *dangerSource) { s.sight = nil }, "unknown"},
		{"wall", types.Position{}, func(s *dangerSource) {
			s.sight = func(context.Context, uint32, navigation.Point3D, navigation.Point3D) (bool, bool) { return false, true }
		}, "clear"},
		{"different floor", types.Position{Z: 50}, func(s *dangerSource) {
			s.path = func(_ context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
				a.Z = 0
				b.Z = 0
				return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
			}
		}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := source
			if tc.edit != nil {
				tc.edit(&src)
			}
			mobs := []types.Creature{mob}
			if tc.name == "clear" {
				mobs = nil
			}
			d := checkDanger(context.Background(), types.Position{}, tc.point, 25, mobs, src)
			if d.State != tc.want {
				t.Fatalf("%+v", d)
			}
		})
	}
}
func TestTravelDangerPolicy(t *testing.T) {
	actWorld(t, actBase+`mobs={{alive=true,reaction="hostile",x=10,y=0,z=0}}`, `
  r=act.travel{points={{10,0,0}},stop_on_danger=true};assert(not r.ok and r.error.code=="danger_ahead" and #moves==0)
  r=act.travel{points={{10,0,0}}};assert(r.ok and #r.data.exposure>0 and #moves==1)
 `)
}

func TestTravelStopsWhileDangerQueryIsPending(t *testing.T) {
	actWorld(t, actBase+`
  checks=0;walking=false
  move.to=function() walking=true;return {id=9000,pending=true} end
  move.stop=function() walking=false;return {ok=true} end
  world.danger=function()
    checks=checks+1
    if checks>1 then assert(not walking,"walked while checking danger");wait(0.01);return {ok=true,data={state="unknown",hostiles={}}} end
    return {ok=true,data={state="clear",hostiles={}}}
  end
 `, `r=act.travel{points={{10,0,0}},stop_on_danger=true};assert(not r.ok and r.error.code=="danger_ahead" and not walking)`)
}
