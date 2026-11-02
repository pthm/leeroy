package pathfinding

import (
	"context"
	"errors"
	"testing"
	"time"
)

func preparationFixture(t *testing.T) (*PathFindingService, *PathResult) {
	pf := interiorFinder(t, floorCell{10, 10, 0})
	vm, _ := collisionFixture(t, []Vector3{{-30, -30, 0}, {0, -30, 0}, {0, 0, 0}, {-30, 0, 0}}, []MeshTriangle{{0, 1, 2}, {0, 2, 3}})
	s := NewPathFindingServiceWithMMapMgr(pf.mmapMgr, nil, vm)
	r, err := s.FindPathContext(context.Background(), 0, Point3D{12, 12, 0}, Point3D{18, 18, 0})
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}
func TestPreparedSupportAndMissingCoverage(t *testing.T) {
	s, r := preparationFixture(t)
	p, err := s.PrepareWalkingContext(context.Background(), 0, r, WalkingBody())
	if err != nil || p.Collision.State != "clear" || p.Points[0] != r.RequestedStart {
		t.Fatalf("%+v %v", p, err)
	}
	for _, point := range p.Points[1:] {
		if point.Z < 0 || point.Z > .03 {
			t.Fatal(point)
		}
	}
	s.vmapMgr = nil
	p, err = s.PrepareWalkingContext(context.Background(), 0, r, WalkingBody())
	var traversal *TraversalError
	if !errors.As(err, &traversal) || p.Collision.State != "unknown" {
		t.Fatalf("missing coverage: %+v %v", p, err)
	}
}
func TestPreparedRejectsIncompleteAndInvalidPaths(t *testing.T) {
	s, r := preparationFixture(t)
	for _, bad := range []*PathResult{nil, {}, {Type: PathfindIncomplete, Points: r.Points}} {
		if _, err := s.PrepareWalkingContext(context.Background(), 0, bad, WalkingBody()); err == nil {
			t.Fatal("accepted incomplete path")
		}
	}
	if _, err := s.PrepareWalkingContext(context.Background(), 0, r, BodyProfile{}); err == nil {
		t.Fatal("accepted invalid body")
	}
	r.PointPolys[0] = 0
	if _, err := s.PrepareWalkingContext(context.Background(), 0, r, WalkingBody()); err == nil {
		t.Fatal("accepted stale polygon")
	}
}
func TestPreparedCancellationDuringCollisionWait(t *testing.T) {
	s, r := preparationFixture(t)
	s.vmapMgr.mu.Lock()
	defer s.vmapMgr.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.PrepareWalkingContext(ctx, 0, r, WalkingBody()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
}
