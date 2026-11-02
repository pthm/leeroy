// clientdata reads a 3.3.5a game install (its MPQ archives) to build the data the tools use, in the data dir (gitignored):
// minimap tiles and road layers (the viewer, pathing), and the terrain and model files the viewer's detail tiles draw.
// Nothing reads the game install after extracting.
//
//	clientdata -client <game dir> extract data             every continent
//	clientdata -client <game dir> -maps 0 extract data      Eastern Kingdoms only
//	clientdata -client <game dir> cat 'textures\Minimap\md5translate.trs'
//	clientdata roads data                                   the road layers again, from data/client (no install)
//	clientdata -client <game dir> icons data                spell and item icons (data/icons) and their DBCs
package main

import (
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pthm/leeroy/azsdk/clientdata"
)

func main() {
	dir := flag.String("client", os.Getenv("WOW_CLIENT"), "the game install (contains Data/); default $WOW_CLIENT")
	mapsFlag := flag.String("maps", "", "extract: the continents' map ids, comma separated (0,1,530,571); default all")
	flag.Parse()
	if flag.Arg(0) == "roads" { // roads <data dir>: rebuild <data dir>/roads from the extracted files in <data dir>/client
		data := flag.Arg(1)
		src := clientdata.Dir(filepath.Join(data, "client"))
		if data == "" || !src.Has() {
			log.Fatal("clientdata roads <data dir>: needs <data dir>/client from an extract")
		}
		for _, id := range []int{0, 1, 530, 571} {
			n, err := clientdata.Roads(src, id, filepath.Join(data, "roads"))
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("map %d: %d squares with roads", id, n)
		}
		return
	}
	if *dir == "" {
		log.Fatal("clientdata: -client <game dir> (or $WOW_CLIENT): a World of Warcraft 3.3.5a install")
	}
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
	case "extract": // extract <data dir>: minimap tiles, road rasters, terrain and model files for the continents
		out := flag.Arg(1)
		if out == "" {
			log.Fatal("clientdata extract <data dir>")
		}
		ids := []int{}
		for id := range clientdata.Maps {
			ids = append(ids, id)
		}
		if *mapsFlag != "" {
			ids = ids[:0]
			for _, f := range strings.Split(*mapsFlag, ",") {
				id, err := strconv.Atoi(strings.TrimSpace(f))
				if _, ok := clientdata.Maps[id]; err != nil || !ok {
					log.Fatalf("-maps: %q is not a continent (0, 1, 530, 571)", f)
				}
				ids = append(ids, id)
			}
		}
		sort.Ints(ids)
		for _, id := range ids {
			n, err := c.Minimap(id, filepath.Join(out, "minimap"))
			if err != nil {
				log.Fatal(err)
			}
			r, err := c.Roads(id, filepath.Join(out, "roads"))
			if err != nil {
				log.Fatal(err)
			}
			f, size, err := c.ExtractTerrain(id, filepath.Join(out, "client"))
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("map %d: %d minimap tiles, %d squares with roads, %d terrain and model files (%d MB)", id, n, r, f, size>>20)
		}
	case "icons": // icons <data dir>: SpellIcon.dbc and ItemDisplayInfo.dbc into dbc/, every icon they name into icons/<name>.png
		out := flag.Arg(1)
		if out == "" {
			log.Fatal("clientdata icons <data dir>")
		}
		names := map[string]bool{}
		for _, d := range []struct {
			file  string
			field int
		}{{"SpellIcon.dbc", 1}, {"ItemDisplayInfo.dbc", 5}} {
			b, err := c.Read(`DBFilesClient\` + d.file)
			if err != nil {
				log.Fatal(err)
			}
			os.MkdirAll(filepath.Join(out, "dbc"), 0o755)
			os.WriteFile(filepath.Join(out, "dbc", d.file), b, 0o644)
			for _, n := range clientdata.DBCStrings(b, d.field) {
				if n == "" {
					continue
				}
				names[strings.ToLower(filepath.Base(strings.ReplaceAll(n, `\`, "/")))] = true
			}
		}
		os.MkdirAll(filepath.Join(out, "icons"), 0o755)
		n := 0
		for name := range names {
			b, err := c.Read(`Interface\Icons\` + name + ".blp")
			if err != nil {
				continue
			}
			img, err := clientdata.DecodeBLP(b)
			if err != nil {
				continue
			}
			f, err := os.Create(filepath.Join(out, "icons", name+".png"))
			if err != nil {
				log.Fatal(err)
			}
			png.Encode(f, img)
			f.Close()
			n++
		}
		log.Printf("%d icons of %d named", n, len(names))
	default:
		log.Fatal("usage: clientdata -client <dir> [-maps 0,1] cat <file> | blp <file> <out.png> | textures <map dir> | extract <data dir> | icons <data dir>")
	}
}
