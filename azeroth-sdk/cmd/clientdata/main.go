// clientdata reads a 3.3.5a game install (its MPQ archives) to build the data the tools use: minimap tiles for the
// viewer and road layers for pathing.
//
//	clientdata -client ~/Downloads/ChromieCraft_3.3.5a cat 'textures\Minimap\md5translate.trs'
package main

import (
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/pthm/wowbench/azeroth-sdk/clientdata"
)

func main() {
	dir := flag.String("client", os.Getenv("WOW_CLIENT"), "the game install (contains Data/)")
	flag.Parse()
	c, err := clientdata.OpenClient(*dir)
	if err != nil {
		log.Fatal(err)
	}
	switch flag.Arg(0) {
	case "cat":
		b, err := c.Read(flag.Arg(1))
		if err != nil {
			log.Fatal(err)
		}
		os.Stdout.Write(b)
	case "blp": // blp <file> <out.png>
		b, err := c.Read(flag.Arg(1))
		if err != nil {
			log.Fatal(err)
		}
		img, err := clientdata.DecodeBLP(b)
		if err != nil {
			log.Fatal(err)
		}
		f, _ := os.Create(flag.Arg(2))
		png.Encode(f, img)
		f.Close()
	case "textures": // textures <map dir>: ground textures used across a map, by how many chunks use them
		dir := flag.Arg(1)
		count := map[string]int{}
		for ty := 0; ty < 64; ty++ {
			for tx := 0; tx < 64; tx++ {
				b, err := c.Read(fmt.Sprintf(`World\Maps\%s\%s_%d_%d.adt`, dir, dir, tx, ty))
				if err != nil {
					continue
				}
				a, err := clientdata.ParseADT(b, false)
				if err != nil {
					continue
				}
				for _, ch := range a.Chunks {
					for _, l := range ch.Layers {
						if int(l) < len(a.Textures) {
							count[a.Textures[l]]++
						}
					}
				}
			}
		}
		names := make([]string, 0, len(count))
		for n := range count {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool { return count[names[i]] > count[names[j]] })
		for _, n := range names {
			fmt.Printf("%7d %s\n", count[n], n)
		}
	case "extract": // extract <data dir>: minimap tiles and road rasters for every continent
		out := flag.Arg(1)
		for id := range clientdata.Maps {
			n, err := c.Minimap(id, filepath.Join(out, "minimap"))
			if err != nil {
				log.Fatal(err)
			}
			r, err := c.Roads(id, filepath.Join(out, "roads"))
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("map %d: %d minimap tiles, %d squares with roads", id, n, r)
		}
	default:
		log.Fatal("usage: clientdata -client <dir> cat <file> | blp <file> <out.png> | textures <map dir> | extract <data dir>")
	}
}
