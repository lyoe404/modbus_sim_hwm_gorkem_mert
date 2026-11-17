// Package remotereset implements the Raw TCP line-oriented command service (Port 9001).
// Supported commands:
//   PING
//   STATUS
//   RESET <device>   (triggers fault?mode=down on the target simulator's admin API, then recovers)
package remotereset

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Addr          string
	Targets       map[string]string // deviceName -> adminBaseURL (e.g. "temp-humidity": "http://temp-humidity-sim:5121")
	ResetDuration time.Duration
	Log           *slog.Logger

	mu         sync.Mutex
	resetCount int
	lastReset  string
	httpClient *http.Client
}

func NewServer(addr string, targets map[string]string, resetDur time.Duration, log *slog.Logger) *Server {
	if resetDur <= 0 {
		resetDur = 5 * time.Second
	}
	return &Server{
		Addr:          addr,
		Targets:       targets,
		ResetDuration: resetDur,
		Log:           log,
		httpClient:    &http.Client{Timeout: 3 * time.Second},
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	s.Log.Info("remote-reset Raw TCP listening", "addr", s.Addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resp := s.dispatch(line)
		_, _ = fmt.Fprintf(conn, "%s\n", resp)
	}
}

func (s *Server) dispatch(line string) string {
	parts := strings.Fields(line)
	cmd := strings.ToUpper(parts[0])

	switch cmd {
	case "PING":
		return "PONG"
	case "STATUS":
		s.mu.Lock()
		rc, lr := s.resetCount, s.lastReset
		s.mu.Unlock()
		if lr == "" {
			lr = "none"
		}
		return fmt.Sprintf("OK resets=%d last=%s targets=%d", rc, lr, len(s.Targets))
	case "RESET":
		if len(parts) < 2 {
			return "ERR missing target device (usage: RESET <device>)"
		}
		target := strings.ToLower(parts[1])
		adminURL, ok := s.Targets[target]
		if !ok {
			return fmt.Sprintf("ERR unknown device %q", target)
		}
		if err := s.triggerDeviceReset(target, adminURL); err != nil {
			return fmt.Sprintf("ERR reset failed: %v", err)
		}
		s.mu.Lock()
		s.resetCount++
		s.lastReset = fmt.Sprintf("%s@%s", target, time.Now().UTC().Format(time.RFC3339))
		s.mu.Unlock()
		return fmt.Sprintf("OK RESET %s initiated (offline for %s)", target, s.ResetDuration)
	default:
		return fmt.Sprintf("ERR unknown command %q (supported: PING, STATUS, RESET <device>)", cmd)
	}
}

func (s *Server) triggerDeviceReset(target, adminURL string) error {
	reqDown, err := http.NewRequest(http.MethodPost, adminURL+"/fault?mode=down", nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(reqDown)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("admin returned %d", resp.StatusCode)
	}

	s.Log.Info("device forced offline by remote-reset", "target", target, "duration", s.ResetDuration)
	go func() {
		time.Sleep(s.ResetDuration)
		reqUp, err := http.NewRequest(http.MethodPost, adminURL+"/fault?mode=none", nil)
		if err != nil {
			return
		}
		r, err := s.httpClient.Do(reqUp)
		if err == nil {
			_ = r.Body.Close()
			s.Log.Info("device recovered after remote-reset", "target", target)
		}
	}()
	return nil
}
