package pathfinding

import (
	"os"
	"path/filepath"
	"testing"
)

// Goldshire's square has navmesh: the ground and the inn's floors, all inside the square.
func TestSquareTriangles(t *testing.T) {
	dir := filepath.Join("..", "..", "data", "mmaps")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no mmaps")
	}
	tris, err := NewMMapManager(dir).SquareTriangles(0, 49, 31)
	if err != nil || len(tris) < 1000 {
		t.Fatalf("%v, %d triangles", err, len(tris))
	}
	inSquare := 0
	for _, tr := range tris {
		if x, y := tr[0].X, tr[0].Y; x <= -9066 && x >= -9600 && y <= 534 && y >= 0 {
			inSquare++
		}
	}
	if inSquare < len(tris)*9/10 {
		t.Fatalf("%d of %d triangles in the square", inSquare, len(tris))
	}
	t.Logf("%d triangles", len(tris))
}
