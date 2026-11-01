package world

import (
	"os"
	"path/filepath"
	"sync"
)

var dataDir = sync.OnceValue(func() string {
	if d := os.Getenv("LEEROY_DATA"); d != "" {
		return d
	}
	// the repo's data/ (maps, dbc, minimap...): above the working directory or the executable (bin/)
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, d := range starts {
		for ; ; d = filepath.Dir(d) {
			if st, err := os.Stat(filepath.Join(d, "data", "maps")); err == nil && st.IsDir() {
				return filepath.Join(d, "data")
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	return "data"
})

// DataDir is the extracted game data (maps/, dbc/, minimap/, roads/): $LEEROY_DATA, else the nearest data/ with maps.
func DataDir() string { return dataDir() }

// DataFlag takes "--data <dir>" out of args, if there, and makes it the data directory (as $LEEROY_DATA would): the
// tools call it first thing, before anything reads the data.
func DataFlag(args []string) []string {
	for i, a := range args {
		if a == "--data" && i+1 < len(args) {
			os.Setenv("LEEROY_DATA", args[i+1])
			return append(append([]string{}, args[:i]...), args[i+2:]...)
		}
	}
	return args
}
