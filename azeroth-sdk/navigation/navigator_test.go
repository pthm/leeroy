package navigation

import (
	"github.com/o0olele/detour-go/detour"
	"testing"

	"github.com/pthm/leeroy/azeroth-sdk/pathfinding"
)

func TestConvertResultRejectsSyntheticStraightPaths(t *testing.T) {
	got := convertResult(&pathfinding.PathResult{
		Type: pathfinding.PathfindNormal | pathfinding.PathfindNotUsingPath,
		Points: []pathfinding.Point3D{
			{X: 0, Y: 0, Z: 0},
			{X: 20, Y: 0, Z: 5},
		},
	})
	if got.Found {
		t.Fatalf("synthetic path was marked found")
	}
}

func TestConvertResultAcceptsCappedNavPaths(t *testing.T) {
	got := convertResult(&pathfinding.PathResult{
		Type: pathfinding.PathfindNormal | pathfinding.PathfindShort,
		Points: []pathfinding.Point3D{
			{X: 0, Y: 0, Z: 0},
			{X: 3, Y: 0, Z: 0},
			{X: 6, Y: 0, Z: 0},
		},
	})
	if !got.Found {
		t.Fatalf("capped nav path was marked missing")
	}
}

func TestPartialPathAndSurfaceMetadataSurviveCopies(t *testing.T) {
	raw := &pathfinding.PathResult{Type: pathfinding.PathfindNormal | pathfinding.PathfindShort, Points: []pathfinding.Point3D{{X: 1}}, Corridor: []detour.DtPolyRef{7}, PointPolys: []detour.DtPolyRef{7}, Transitions: []bool{true}}
	got := convertResult(raw)
	copy := got.Clone()
	raw.Corridor[0] = 8
	got.Points[0].Z = 99
	got.surface.PointPolys[0] = 9
	got.surface.Transitions[0] = false
	if !copy.Found || !copy.Partial || copy.Points[0].Z != 0 || copy.surface.PointPolys[0] != 7 || copy.surface.Corridor[0] != 7 || !copy.surface.Transitions[0] {
		t.Fatalf("metadata lost or aliased: %+v", copy)
	}
}
