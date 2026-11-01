package clientdata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testClient(t *testing.T) *Client {
	t.Helper()
	dir := os.Getenv("WOW_CLIENT")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), "Downloads", "ChromieCraft_3.3.5a")
	}
	c, err := OpenClient(dir)
	if err != nil {
		t.Skip("no game client")
	}
	return c
}

// Goldshire's square places the Lion's Pride Inn and a lot of trees.
func TestADTPlacements(t *testing.T) {
	c := testClient(t)
	b, err := c.Read(`World\Maps\Azeroth\Azeroth_31_49.adt`)
	if err != nil {
		t.Fatal(err)
	}
	a, err := ParseADT(b, false)
	if err != nil {
		t.Fatal(err)
	}
	inn, trees := false, 0
	for _, w := range a.WMOs {
		t.Logf("wmo %d %s pos %v rot %v", w.UniqueID, w.Name, w.Pos, w.Rot)
		inn = inn || strings.Contains(strings.ToLower(w.Name), "inn")
	}
	for _, m := range a.Models {
		if strings.Contains(strings.ToLower(m.Name), "tree") {
			trees++
		}
	}
	if !inn || trees < 20 || a.Models[0].Scale <= 0 {
		t.Fatalf("want the inn and trees: %d wmos, %d models, %d trees", len(a.WMOs), len(a.Models), trees)
	}
}

func TestLoadModels(t *testing.T) {
	c := testClient(t)
	inn, err := c.LoadWMO(`WORLD\WMO\AZEROTH\BUILDINGS\GOLDSHIREINN\GOLDSHIREINN.WMO`)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("inn: %d verts, %d tris, %d materials, %d doodads in %d sets; first texture %s", len(inn.Verts), len(inn.Tris), len(inn.Materials), len(inn.Doodads), len(inn.Sets), inn.Materials[0].Texture)
	b, _ := c.Read(`World\Maps\Azeroth\Azeroth_31_49.adt`)
	a, _ := ParseADT(b, false)
	for _, p := range a.Models {
		if !strings.Contains(strings.ToLower(p.Name), "tree") {
			continue
		}
		tree, err := c.LoadM2(p.Name)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d verts, %d tris, materials %+v", p.Name, len(tree.Verts), len(tree.Tris), tree.Materials)
		if len(tree.Tris) == 0 || !tree.Materials[0].AlphaTest && len(tree.Materials) < 2 {
			t.Fatal("a tree with triangles and leaves")
		}
		break
	}
	if len(inn.Tris) < 500 || len(inn.Materials) < 3 {
		t.Fatal("the inn has geometry and materials")
	}
}
