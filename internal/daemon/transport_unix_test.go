//go:build !windows

package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStaleSocketAndProtocolMismatch(t *testing.T) {
	address, err := Endpoint(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	_ = stale.Close()
	listener, err := listen(address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Stat(address)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	info, err = os.Stat(filepath.Dir(address))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal(info, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		(&Server{}).serveConn(ctx, conn)
	}()
	conn, err := dial(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = encode(conn, request{Version: ProtocolVersion + 1, Method: "status"}); err != nil {
		t.Fatal(err)
	}
	var reply response
	if err = decode(conn, &reply); err != nil || !strings.Contains(reply.Error, "protocol mismatch") {
		t.Fatal(reply, err)
	}
	<-done
}

func TestListenerPreservesNonSocket(t *testing.T) {
	address, err := Endpoint(t.TempDir(), "file")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(address, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(address)
	if listener, err := listen(address); err == nil {
		_ = listener.Close()
		t.Fatal("overwrote non-socket")
	}
	data, err := os.ReadFile(address)
	if err != nil || string(data) != "keep" {
		t.Fatal(string(data), err)
	}
}
