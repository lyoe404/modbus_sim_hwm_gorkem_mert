package remotereset

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommandsAndResetExtension(t *testing.T) {
	var mu sync.Mutex
	mode := "none"
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		mode = r.URL.Query().Get("mode")
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer admin.Close()
	s := NewServer("", map[string]string{"relay": admin.URL}, 80*time.Millisecond, nil)
	if s.dispatch("PING") != "PONG" {
		t.Fatal("ping")
	}
	if !strings.Contains(s.dispatch("STATUS"), "targets=1") {
		t.Fatal("status")
	}
	if !strings.HasPrefix(s.dispatch("RESET missing"), "ERR") {
		t.Fatal("unknown target")
	}
	if !strings.HasPrefix(s.dispatch("RESET relay"), "OK") {
		t.Fatal("reset")
	}
	time.Sleep(45 * time.Millisecond)
	if !strings.HasPrefix(s.dispatch("RESET relay"), "OK") {
		t.Fatal("second reset")
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	m := mode
	mu.Unlock()
	if m != "down" {
		t.Fatalf("early recovery: %s", m)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	m = mode
	mu.Unlock()
	if m != "none" {
		t.Fatalf("not recovered: %s", m)
	}
	if status, _, _ := s.DeviceStatus("relay"); status != "online" {
		t.Fatal(status)
	}
}
func TestRecoveryFailure(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mode") == "none" {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(200)
		}
	}))
	defer admin.Close()
	s := NewServer("", map[string]string{"relay": admin.URL}, time.Millisecond, nil)
	if err := s.triggerDeviceReset("relay", admin.URL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	status, detail, _ := s.DeviceStatus("relay")
	if status != "recovery_failed" || detail == "" {
		t.Fatalf("%s %s", status, detail)
	}

}

func TestTCPCommandsAndShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := NewServer(addr, map[string]string{}, time.Second, nil)
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx) }()
	var conn net.Conn
	for i := 0; i < 50; i++ {
		conn, err = net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err = fmt.Fprintln(conn, "PING"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "PONG" {
		t.Fatalf("response %q err %v", line, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown timeout")
	}
}
