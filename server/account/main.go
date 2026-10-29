// account <user> <pass> [gmLevel]: create or reset a game account in the auth DB.
// gmLevel 3 lets the bot use GM dot-commands (.levelup, .additem, .tele): handy for scenario setup, unfair in a benchmark.
package main

import (
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/pthm/leeroy/azworld/world"
)

var srpN, _ = new(big.Int).SetString("894B645E89E1535BBDAD5B8B290650530801B18EBFBF5E8FAB3C82872A3E9BB7", 16)

// verifier is AzerothCore's SRP6 verifier: g^H(salt, H(USER:PASS)) mod N, all little-endian, 32 bytes.
func verifier(salt []byte, user, pass string) []byte {
	inner := sha1.Sum([]byte(user + ":" + strings.ToUpper(pass)))
	x := sha1.Sum(append(slices.Clone(salt), inner[:]...))
	slices.Reverse(x[:])
	v := new(big.Int).Exp(big.NewInt(7), new(big.Int).SetBytes(x[:]), srpN).FillBytes(make([]byte, 32))
	slices.Reverse(v)
	return v
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: account <user> <pass> [gmLevel]")
		os.Exit(2)
	}
	user, pass, gm := strings.ToUpper(os.Args[1]), os.Args[2], "0"
	if len(os.Args) > 3 {
		gm = os.Args[3]
	}
	if !regexp.MustCompile(`^[A-Z0-9]{1,16}$`).MatchString(user) || !regexp.MustCompile(`^\d$`).MatchString(gm) {
		fmt.Fprintln(os.Stderr, "account must be alphanumeric <= 16 chars; gmLevel 0-9")
		os.Exit(2)
	}
	salt := make([]byte, 32)
	rand.Read(salt)
	db := world.DB(world.Admin, "acore_auth")
	q := `INSERT INTO account (username, salt, verifier, expansion) VALUES (?, ?, ?, 2)
  ON DUPLICATE KEY UPDATE salt = VALUES(salt), verifier = VALUES(verifier);
DELETE FROM account_access WHERE id = (SELECT id FROM account WHERE username = ?);`
	args := []any{user, salt, verifier(salt, user, pass), user}
	if gm != "0" {
		q += "\nINSERT INTO account_access (id, gmlevel, RealmID) SELECT id, ?, -1 FROM account WHERE username = ?;"
		args = append(args, gm, user)
	}
	if _, err := db.Exec(q, args...); err != nil {
		fmt.Fprintln(os.Stderr, "account:", err)
		os.Exit(1)
	}
	fmt.Printf("account %s ready (gm %s)\n", user, gm)
}
