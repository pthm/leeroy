package pathfinding

import (
	"os"
	"path/filepath"
	"testing"
)

// The triangles' tops agree with the height query: shooting down onto the Lion's Pride Inn in Goldshire hits the
// highest triangle over that point.
func TestTrianglesMatchHeight(t *testing.T) {
	dir := filepath.Join("..", "..", "data", "vmaps")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no vmaps")
	}
	vm := NewVMapManager(dir)
	x, y := float32(-9462), float32(26)
	want, ok := vm.GetHeight(0, x, y, 200, 400)
	if !ok {
		t.Skip("no model at the inn")
	}
	top, n := float32(-1e9), 0
	vm.Triangles(0, x-1, x+1, y-1, y+1, func(a, b, c Vector3) {
		n++
		// barycentric: is (x, y) inside the triangle's top-down projection; if so its height there
		d := (b.Y-c.Y)*(a.X-c.X) + (c.X-b.X)*(a.Y-c.Y)
		if d == 0 {
			return
		}
		l1 := ((b.Y-c.Y)*(x-c.X) + (c.X-b.X)*(y-c.Y)) / d
		l2 := ((c.Y-a.Y)*(x-c.X) + (a.X-c.X)*(y-c.Y)) / d
		l3 := 1 - l1 - l2
		if l1 >= 0 && l2 >= 0 && l3 >= 0 {
			top = max(top, l1*a.Z+l2*b.Z+l3*c.Z)
		}
	})
	if n == 0 || top < want-0.05 || top > want+0.05 {
		t.Fatalf("%d triangles; highest over the point %.2f, height query %.2f", n, top, want)
	}
}
