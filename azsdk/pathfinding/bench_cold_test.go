package pathfinding

import (
	"context"
	"os"
	"testing"
)

// BenchmarkColdRoute: long routes on a new service each time, every tile loaded from disk (a session's first route
// into new country): Orgrimmar to Thunder Bluff's lifts, Goldshire to Moonbrook (NAV_DATA: the data dir).
func BenchmarkColdRoute(b *testing.B) {
	data := os.Getenv("NAV_DATA")
	if data == "" {
		b.Skip("NAV_DATA not set")
	}
	routes := []struct {
		m        uint32
		from, to Point3D
	}{{1, Point3D{X: 1300, Y: -4400, Z: 26}, Point3D{X: -1311, Y: 187, Z: 69}}, {0, Point3D{X: -9460, Y: 64, Z: 56}, Point3D{X: -11208, Y: 1668, Z: 26}}}
	for i := 0; i < b.N; i++ {
		svc := NewPathFindingServiceFromDataDir(data)
		for _, r := range routes {
			if res, err := svc.FindPathContext(context.Background(), r.m, r.from, r.to); err != nil || res == nil || len(res.Points) == 0 {
				b.Fatalf("no path: %v", err)
			}
		}
	}
}
