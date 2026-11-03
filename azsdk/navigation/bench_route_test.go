package navigation

import (
	"os"
	"testing"
	"time"
)

// BenchmarkLongRoute: Orgrimmar to Thunder Bluff's lifts (7000 yd) and Goldshire to Moonbrook (2800 yd), the tiles
// loaded first (NAV_DATA: the data dir).
func BenchmarkLongRoute(b *testing.B) {
	data := os.Getenv("NAV_DATA")
	if data == "" {
		b.Skip("NAV_DATA not set")
	}
	n := NewEmbeddedNavigator(data)
	routes := []struct {
		m        uint32
		from, to Point3D
	}{{1, Point3D{X: 1300, Y: -4400, Z: 26}, Point3D{X: -1311, Y: 187, Z: 69}}, {0, Point3D{X: -9460, Y: 64, Z: 56}, Point3D{X: -11208, Y: 1668, Z: 26}}}
	for _, r := range routes {
		t0 := time.Now()
		n.FindPath(r.m, r.from, r.to)
		b.Logf("first (cold) %d: %s", r.m, time.Since(t0))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, r := range routes {
			if res, err := n.FindPath(r.m, r.from, r.to); err != nil || res == nil || !res.Found {
				b.Fatalf("no path: %v", err)
			}
		}
	}
}
