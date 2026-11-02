package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/pthm/leeroy/azsdk/jqjson"
)

// wow reaches the daemon through its Unix socket when there is one (no TCP port, so no TIME_WAIT)
func TestRPCOverUnixSocket(t *testing.T) {
	port := fmt.Sprint(1 + os.Getpid()%60000) // the socket is tried before TCP
	sock := "/tmp/azsdk-" + port + ".sock"
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip(err)
	}
	defer os.Remove(sock)
	defer l.Close()
	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"via socket"}`)
	}))
	t.Setenv("WOW_PORT", port)
	if v, ok := rpc("sessions.list", jqjson.NewObj()); !ok || v != "via socket" {
		t.Fatalf("%v %v", v, ok)
	}
}
