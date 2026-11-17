package reset

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestTCPResetStatusAndFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				if strings.HasPrefix(line, "STATUS") {
					fmt.Fprintln(conn, "OK device=relay status=recovering")
				} else {
					fmt.Fprintln(conn, "OK RESET relay initiated")
				}
			}()
		}
	}()
	c := &Client{Addr: ln.Addr().String(), Timeout: time.Second}
	if _, err := c.Command(context.Background(), "RESET", "relay"); err != nil {
		t.Fatal(err)
	}
	if state, err := c.Status(context.Background(), "relay"); err != nil || state != "recovering" {
		t.Fatal(state, err)
	}
	if _, err := c.Command(context.Background(), "RESET", "relay\nPING"); err == nil {
		t.Fatal("invalid target accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Command(ctx, "RESET", "relay"); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestTCPTimeoutClosesConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		bufio.NewReader(conn).ReadString('\n')
		var b [1]byte
		conn.Read(b[:])
		close(done)
	}()
	c := &Client{Addr: ln.Addr().String(), Timeout: 30 * time.Millisecond}
	if _, err := c.Command(context.Background(), "RESET", "relay"); err == nil {
		t.Fatal("timeout ignored")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connection leaked")
	}
}
