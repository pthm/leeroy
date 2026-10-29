// Package world is the shared Go side of the tools that read the game's world: the AzerothCore databases
// (world, characters, auth and the wowbench schema built by azworld setup), the server's terrain files, the client's
// minimap and road tiles, and map rendering. The bench (judge, party), the viewer (replay, live) and azworld use it.
package world

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"

	_ "github.com/go-sql-driver/mysql"
)

// Users: Reader is the read-only account agents' tools use; Admin can write (azworld setup, accounts) and read characters.
const (
	Reader = "worldreader:worldreader"
	Admin  = "root:password"
)

var (
	dbMu sync.Mutex
	dbs  = map[string]*sql.DB{}
)

// DB is a pooled connection to one database as one user, on $LEEROY_DB_ADDR (default 127.0.0.1:3306).
func DB(user, name string) *sql.DB {
	dbMu.Lock()
	defer dbMu.Unlock()
	k := user + "/" + name
	if d := dbs[k]; d != nil {
		return d
	}
	addr := os.Getenv("LEEROY_DB_ADDR")
	if addr == "" {
		addr = "127.0.0.1:3306"
	}
	d, err := sql.Open("mysql", fmt.Sprintf("%s@tcp(%s)/%s?multiStatements=true&interpolateParams=true", user, addr, name))
	if err != nil {
		panic(err) // only a malformed DSN
	}
	d.SetMaxOpenConns(4)
	dbs[k] = d
	return d
}

// Rows runs a query and returns every row as strings (NULL as ""), the way `mysql -N -B` prints them.
func Rows(d *sql.DB, q string, args ...any) ([][]string, error) {
	rs, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	var out [][]string
	for rs.Next() {
		raw := make([]sql.RawBytes, len(cols))
		ptr := make([]any, len(cols))
		for i := range raw {
			ptr[i] = &raw[i]
		}
		if err := rs.Scan(ptr...); err != nil {
			return nil, err
		}
		row := make([]string, len(cols))
		for i, v := range raw {
			row[i] = string(v)
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

// MustRows is Rows for tools that can't go on without the database: it exits with the error.
func MustRows(d *sql.DB, q string, args ...any) [][]string {
	r, err := Rows(d, q, args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query failed (is the server up?): %v\n%s\n", err, strings.TrimSpace(q))
		os.Exit(1)
	}
	return r
}

// World is the read-only world database, Bench the wowbench schema.
func World() *sql.DB { return DB(Reader, "acore_world") }
func Bench() *sql.DB { return DB(Reader, "wowbench") }
