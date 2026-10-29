package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// GM starts the GM test character (Devgm, account devgm, gm level 3) on port 8112 with GM mode (mobs ignore you) and
// god mode, optionally teleported to x y z [map]. For testing SDK changes without walking or dying; never use a GM
// account in a scored run. Query it with WOW_PORT=8112 wow '...'.
func GM(repo string, at []string) error {
	if out, err := exec.Command(filepath.Join(repo, "bin/account"), "devgm", "devgm", "3").CombinedOutput(); err != nil {
		return fmt.Errorf("account: %v: %s", err, out)
	}
	exec.Command("pkill", "-f", "--", "-char Devgm").Run()
	time.Sleep(time.Second)
	log, err := os.Create("/tmp/devgm.log")
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(repo, "bin/azsdk"), "-char", "Devgm", "-account", "devgm", "-race", "1", "-class", "1",
		"-listen", "127.0.0.1:8112", "-data", filepath.Join(repo, "data"), "-events", "/tmp/devgm-events")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return err
	}
	cmd.Process.Release() // it outlives this command
	ctx := context.Background()
	for i := 0; ; i++ {
		if _, err := eval(ctx, 8112, "return 1"); err == nil {
			break
		}
		if i >= 60 {
			return fmt.Errorf("Devgm didn't come up, see /tmp/devgm.log")
		}
		time.Sleep(time.Second)
	}
	eval(ctx, 8112, `chat.command(".gm on") chat.command(".cheat god on") return 1`)
	if len(at) >= 3 {
		m := "0"
		if len(at) > 3 {
			m = at[3]
		}
		eval(ctx, 8112, fmt.Sprintf(`chat.command(".go xyz %s %s %s %s") return 1`, at[0], at[1], at[2], m))
	}
	fmt.Println("Devgm up on 8112 (log /tmp/devgm.log)")
	return nil
}

// APIDoc regenerates the API reference (azeroth-sdk/docs/api-reference.md) from a live session's function
// registry: it builds everything, restarts the GM character on the new build, and runs cmd/apigen against it.
func APIDoc(repo string) error {
	if err := Build(repo); err != nil {
		return err
	}
	if err := GM(repo, nil); err != nil {
		return err
	}
	cmd := exec.Command("go", "run", "./cmd/apigen", "-rpc", "http://127.0.0.1:8112/rpc")
	cmd.Dir = filepath.Join(repo, "azeroth-sdk")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
