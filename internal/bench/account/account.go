// Package account creates or resets game accounts in the auth database (azbench account, azbench gm). GM level 3 lets
// a character use GM dot-commands (.levelup, .additem, .tele): handy for scenario setup, unfair in a benchmark.
package account

import (
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"

	"github.com/pthm/leeroy/world"
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

// Ensure creates the game account user with password pass, or resets its password, and sets its GM level (0: none).
func Ensure(user, pass string, gm int) error {
	user = strings.ToUpper(user)
	if !regexp.MustCompile(`^[A-Z0-9]{1,16}$`).MatchString(user) || gm < 0 || gm > 9 {
		return fmt.Errorf("account must be alphanumeric, at most 16 characters; GM level 0-9")
	}
	salt := make([]byte, 32)
	rand.Read(salt)
	db := world.DB(world.Admin, "acore_auth")
	q := `INSERT INTO account (username, salt, verifier, expansion) VALUES (?, ?, ?, 2)
  ON DUPLICATE KEY UPDATE salt = VALUES(salt), verifier = VALUES(verifier);
DELETE FROM account_access WHERE id = (SELECT id FROM account WHERE username = ?);`
	args := []any{user, salt, verifier(salt, user, pass), user}
	if gm != 0 {
		q += "\nINSERT INTO account_access (id, gmlevel, RealmID) SELECT id, ?, -1 FROM account WHERE username = ?;"
		args = append(args, gm, user)
	}
	_, err := db.Exec(q, args...)
	return err
}
