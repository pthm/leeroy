package world

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

// A detail tile of Goldshire's square: it decodes, it's 256x256, and it has more detail than the minimap pixel it
// replaces (neighbouring pixels differ).
func TestDetailTile(t *testing.T) {
	if !HasDetail(0) {
		t.Skip("no game client or minimap")
	}
	b := DetailTile(0, 49, 31, 3, 3, 1)
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatalf("want a 256x256 JPEG: %v %v", err, img)
	}
	diff := 0.0
	for x := 0; x < 255; x++ {
		r1, _, _, _ := img.At(x, 128).RGBA()
		r2, _, _, _ := img.At(x+1, 128).RGBA()
		diff += float64(max(r1, r2)-min(r1, r2)) / 257
	}
	if diff/255 < 2 {
		t.Fatalf("no detail: neighbouring pixels differ by %.1f on average", diff/255)
	}
	if DetailTile(0, 49, 31, 0, 0, 0) != nil || DetailTile(33, 49, 31, 2, 0, 0) != nil {
		t.Fatal("level 0 is the minimap, and instances have no detail tiles")
	}
}

func TestBoxBlur(t *testing.T) {
	a := make([][3]float64, 9)
	a[4] = [3]float64{9, 9, 9} // a 3x3 with the middle lit
	b := boxBlur(a, 3, 1)
	if b[4][0] != 1 || b[0][0] != 9.0/4 {
		t.Fatalf("middle: 9 over 9 cells, corner: 9 over 4: %v %v", b[4], b[0])
	}
}

// A long thin texture halves down to a pixel high, never to nothing (a 4x0 level made a negative index and crashed).
func TestMipsOfThin(t *testing.T) {
	for _, r := range []image.Rectangle{image.Rect(0, 0, 32, 2), image.Rect(0, 0, 2, 64), image.Rect(0, 0, 256, 256)} {
		for _, m := range mipsOf(image.NewRGBA(r)) {
			if m.Rect.Dx() == 0 || m.Rect.Dy() == 0 {
				t.Fatalf("%v: an empty mip level %v", r, m.Rect)
			}
		}
	}
}
