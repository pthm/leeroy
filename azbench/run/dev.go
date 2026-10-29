package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pthm/leeroy/azbench/config"
)

// GM starts a GM test character (gm level 3) with GM mode (mobs ignore you) and god mode, optionally
// teleported to x y z [map]: Devgm (account devgm), a warrior, on port 8112, or for another class Dev<class> (account
// dev<class>) on port 8113 (a mage
// for spells and mana). For testing SDK changes without walking or dying (.gm off and .cheat god off to fight and
// die for real); never use a GM account in a scored run. Query it with WOW_PORT=8112 wow '...'.
func GM(repo, class string, at []string) error {
	id, err := config.ID(config.Classes, class)
	if err != nil {
		return fmt.Errorf("class: %v", err)
	}
	// its own account: an account has one character online, so a second GM character on devgm would log Devgm out
	char, account, port := "Devgm", "devgm", 8112
	if id != 1 {
		name := strings.ReplaceAll(strings.ToLower(class), " ", "")
		char, account, port = "Dev"+name, "dev"+name, 8113
	}
	if out, err := exec.Command(filepath.Join(repo, "bin/account"), account, account, "3").CombinedOutput(); err != nil {
		return fmt.Errorf("account: %v: %s", err, out)
	}
	exec.Command("pkill", "-f", "--", "-char "+char+" ").Run()
	time.Sleep(time.Second)
	logf := "/tmp/" + strings.ToLower(char) + ".log"
	log, err := os.Create(logf)
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(repo, "bin/azsdk"), "-char", char, "-account", account, "-race", "1", "-class", fmt.Sprint(id),
		"-listen", fmt.Sprintf("127.0.0.1:%d", port), "-data", filepath.Join(repo, "data"), "-events", "/tmp/"+strings.ToLower(char)+"-events")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return err
	}
	cmd.Process.Release() // it outlives this command
	ctx := context.Background()
	for i := 0; ; i++ {
		if _, err := eval(ctx, port, "return 1"); err == nil {
			break
		}
		if i >= 60 {
			return fmt.Errorf("%s didn't come up, see %s", char, logf)
		}
		time.Sleep(time.Second)
	}
	eval(ctx, port, `chat.command(".gm on") chat.command(".cheat god on") return 1`)
	if len(at) >= 3 {
		m := "0"
		if len(at) > 3 {
			m = at[3]
		}
		eval(ctx, port, fmt.Sprintf(`chat.command(".go xyz %s %s %s %s") return 1`, at[0], at[1], at[2], m))
	}
	fmt.Printf("%s up on %d (log %s)\n", char, port, logf)
	return nil
}

// APIDoc regenerates the API reference (azeroth-sdk/docs/api-reference.md) from a live session's function
// registry: it builds everything, restarts the GM character on the new build, and runs cmd/apigen against it.
func APIDoc(repo string) error {
	if err := Build(repo); err != nil {
		return err
	}
	if err := GM(repo, "warrior", nil); err != nil {
		return err
	}
	cmd := exec.Command("go", "run", "./cmd/apigen", "-rpc", "http://127.0.0.1:8112/rpc")
	cmd.Dir = filepath.Join(repo, "azeroth-sdk")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
