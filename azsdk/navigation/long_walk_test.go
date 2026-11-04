package navigation

import (
	"os"
	"testing"
)

// TestLongWalkCollision: a long walk prepares a stretch at a time (NAV_DATA). Flash Lite's walk from Northshire to
// Menethil Harbor was refused whole at an Elwynn fence its first stretch gets round.
func TestLongWalkCollision(t *testing.T) {
	data := os.Getenv("NAV_DATA")
	if data == "" {
		t.Skip("NAV_DATA not set")
	}
	n := NewEmbeddedNavigator(data)
	res, err := n.FindPath(0, Point3D{X: -8950, Y: -132.5, Z: 83.5}, Point3D{X: -3800, Y: -850, Z: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found || res.Collision == nil || res.Collision.State != "clear" {
		c := res.Collision
		t.Fatalf("refused: %+v", c)
	}
	t.Logf("%d points, replanned %v", len(res.Points), res.Replanned)
}
