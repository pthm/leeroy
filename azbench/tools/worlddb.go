package tools

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/pthm/leeroy/azworld/world"
)

const worlddbUsage = `worlddb "SELECT ..."   read-only SQL against the game's world database (acore_world): creature spawns,
NPCs, quests, loot tables, items, vendors. Output is tab-separated with a header row, capped at 300 rows.
`

// Worlddb runs read-only SQL on acore_world and prints it the way mysql -B does (tab-separated, a header row per
// result, NULL as NULL), capped at 301 lines.
func Worlddb(args []string) int {
	q := ""
	if len(args) > 0 {
		q = args[0]
	}
	if q == "--help" || q == "-h" {
		fmt.Print(worlddbUsage)
		return 0
	}
	if q == "" {
		b, _ := io.ReadAll(os.Stdin)
		q = strings.TrimRight(string(b), "\n")
	}
	w := &capped{w: bufio.NewWriter(os.Stdout), left: 301}
	defer w.w.Flush()
	rs, err := world.DB(world.Reader, "acore_world").Query(q)
	if err != nil {
		w.line(mysqlErr(err))
		return 0
	}
	defer rs.Close()
	for {
		cols, _ := rs.Columns()
		if rs.Next() {
			w.line(strings.Join(cols, "\t"))
			for ok := true; ok; ok = rs.Next() {
				raw := make([]sql.RawBytes, len(cols))
				ptr := make([]any, len(cols))
				for i := range raw {
					ptr[i] = &raw[i]
				}
				if err := rs.Scan(ptr...); err != nil {
					w.line(mysqlErr(err))
					return 0
				}
				cells := make([]string, len(cols))
				for i, v := range raw {
					if v == nil {
						cells[i] = "NULL"
					} else {
						cells[i] = batchEscape(string(v))
					}
				}
				w.line(strings.Join(cells, "\t"))
			}
		}
		if err := rs.Err(); err != nil {
			w.line(mysqlErr(err))
			return 0
		}
		if !rs.NextResultSet() {
			if err := rs.Err(); err != nil {
				w.line(mysqlErr(err))
			}
			return 0
		}
	}
}

// mysqlErr formats an error like the mysql client: ERROR 1064 (42000) at line 1: You have an error ...
func mysqlErr(err error) string {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return fmt.Sprintf("ERROR %d (%s) at line 1: %s", me.Number, string(me.SQLState[:]), me.Message)
	}
	return "ERROR: " + err.Error()
}

// batchEscape: mysql -B writes newline, tab, backslash and NUL as \n, \t, \\ and \0.
func batchEscape(s string) string {
	return strings.NewReplacer("\\", `\\`, "\n", `\n`, "\t", `\t`, "\x00", `\0`).Replace(s)
}

// capped writes lines until it has written left of them (head -n).
type capped struct {
	w    *bufio.Writer
	left int
}

func (c *capped) line(s string) {
	if c.left > 0 {
		c.w.WriteString(s + "\n")
		c.left--
	}
}
