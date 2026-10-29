// navprobe: path length and how straight it is between points on map 0, straight from the navmesh.
// go run ./azeroth-sdk/cmd/navprobe <data dir> x1 y1 z1 x2 y2 z2 [...more legs]
package main

import (
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
)

func main() {
	nav := navigation.NewEmbeddedNavigator(os.Args[1])
	a := os.Args[2:]
	f := func(i int) float32 { v, _ := strconv.ParseFloat(a[i], 32); return float32(v) }
	for i := 0; i+5 < len(a); i += 6 {
		s, d := navigation.Point3D{X: f(i), Y: f(i + 1), Z: f(i + 2)}, navigation.Point3D{X: f(i + 3), Y: f(i + 4), Z: f(i + 5)}
		res, err := nav.FindPath(0, s, d)
		if err != nil || res == nil || len(res.Points) == 0 {
			fmt.Println("no path", err)
			continue
		}
		length, prev, maxz := 0.0, s, float32(-1e9)
		for _, p := range res.Points {
			length += math.Sqrt(float64((p.X-prev.X)*(p.X-prev.X) + (p.Y-prev.Y)*(p.Y-prev.Y) + (p.Z-prev.Z)*(p.Z-prev.Z)))
			prev = p
			maxz = float32(math.Max(float64(maxz), float64(p.Z)))
		}
		straight := math.Hypot(float64(d.X-s.X), float64(d.Y-s.Y))
		end := res.Points[len(res.Points)-1]
		fmt.Printf("%.0f,%.0f -> %.0f,%.0f: path %.0f yd (straight %.0f, x%.1f), %d points, max z %.0f, ends %.1f yd from target, found=%v\n",
			s.X, s.Y, d.X, d.Y, length, straight, length/straight, len(res.Points), maxz, math.Hypot(float64(end.X-d.X), float64(end.Y-d.Y)), res.Found)
	}
}
