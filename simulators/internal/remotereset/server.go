// Package remotereset implements the Raw TCP line-oriented remote reset service.
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

type resetState struct {
	generation uint64
	deadline   time.Time
	status     string
	lastError  string
}

type Server struct {
	Addr          string
	Targets       map[string]string
	ResetDuration time.Duration
	Log           *slog.Logger
	mu            sync.Mutex
	resetCount    int
	lastReset     string
	states        map[string]*resetState
	httpClient    *http.Client
	ctx           context.Context
	connections   map[net.Conn]struct{}
}

func NewServer(addr string, targets map[string]string, resetDur time.Duration, log *slog.Logger) *Server {
	if resetDur <= 0 {
		resetDur = 5 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{Addr: addr, Targets: targets, ResetDuration: resetDur, Log: log,
		httpClient: &http.Client{Timeout: 3 * time.Second}, states: make(map[string]*resetState), connections: make(map[net.Conn]struct{}), ctx: context.Background()}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		s.mu.Lock()
		for c := range s.connections {
			_ = c.Close()
		}
		s.mu.Unlock()
	}()
	s.Log.Info("remote-reset Raw TCP listening", "addr", s.Addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.Log.Warn("accept failed", "err", err)
			continue
		}
		s.mu.Lock()
		s.connections[conn] = struct{}{}
		s.mu.Unlock()
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close(); s.mu.Lock(); delete(s.connections, conn); s.mu.Unlock() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		_, _ = fmt.Fprintf(conn, "%s\n", s.dispatch(line))
	}
}

func (s *Server) dispatch(line string) string {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return "ERR empty command"
	}
	cmd := strings.ToUpper(parts[0])
	switch cmd {
	case "PING":
		return "PONG"
	case "STATUS":
		if len(parts) == 2 {
			target := strings.ToLower(parts[1])
			status, detail, ok := s.DeviceStatus(target)
			if !ok {
				return fmt.Sprintf("ERR unknown device %q", target)
			}
			if detail != "" {
				return fmt.Sprintf("OK device=%s status=%s error=%q", target, status, detail)
			}
			return fmt.Sprintf("OK device=%s status=%s", target, status)
		}
		if len(parts) > 2 {
			return "ERR usage: STATUS [device]"
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		lr := s.lastReset
		if lr == "" {
			lr = "none"
		}
		// Preserve the original STATUS response format for existing clients.
		return fmt.Sprintf("OK resets=%d last=%s targets=%d", s.resetCount, lr, len(s.Targets))
	case "RESET":
		if len(parts) != 2 {
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
		return fmt.Sprintf("OK RESET %s initiated (offline for %s)", target, s.ResetDuration)
	default:
		return fmt.Sprintf("ERR unknown command %q (supported: PING, STATUS, RESET <device>)", cmd)
	}
}

func (s *Server) postFault(ctx context.Context, adminURL, mode string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(adminURL, "/")+"/fault?mode="+mode, nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("admin returned %d", resp.StatusCode)
	}
	return nil
}

func (s *Server) triggerDeviceReset(target, adminURL string) error {
	// Serialize RESET for each target and keep the generation token across recoveries.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.postFault(s.ctx, adminURL, "down"); err != nil {
		return err
	}
	state := s.states[target]
	if state == nil {
		state = &resetState{}
		s.states[target] = state
	}
	state.generation++
	state.deadline = time.Now().Add(s.ResetDuration)
	state.status = "offline"
	state.lastError = ""
	generation := state.generation
	s.resetCount++
	s.lastReset = fmt.Sprintf("%s@%s", target, time.Now().UTC().Format(time.RFC3339))
	s.Log.Info("device forced offline by remote-reset", "target", target, "duration", s.ResetDuration, "generation", generation)
	go s.recoverDevice(target, adminURL, generation)
	return nil
}

func (s *Server) recoverDevice(target, adminURL string, generation uint64) {
	for {
		s.mu.Lock()
		state := s.states[target]
		if state == nil || state.generation != generation {
			s.mu.Unlock()
			return
		}
		wait := time.Until(state.deadline)
		if wait <= 0 {
			state.status = "recovering"
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-s.ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
	// Keep the lock while issuing the recovery request so a concurrent RESET cannot
	// interleave a new DOWN between the generation check and this UP.
	for attempt := 1; attempt <= 3; attempt++ {
		s.mu.Lock()
		state := s.states[target]
		if state == nil || state.generation != generation {
			s.mu.Unlock()
			return
		}
		if time.Until(state.deadline) > 0 {
			s.mu.Unlock()
			return
		}
		err := s.postFault(s.ctx, adminURL, "none")
		if err == nil {
			state.status = "online"
			state.lastError = ""
			s.mu.Unlock()
			s.Log.Info("device recovered after remote-reset", "target", target, "attempt", attempt)
			return
		}
		state.status = "recovery_failed"
		state.lastError = err.Error()
		s.mu.Unlock()
		s.Log.Warn("device recovery failed", "target", target, "attempt", attempt, "err", err)
		timer := time.NewTimer(time.Duration(attempt) * time.Second)
		select {
		case <-timer.C:
		case <-s.ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

// DeviceStatus returns the latest remote-reset state for a configured device.
func (s *Server) DeviceStatus(target string) (status, lastError string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok = s.Targets[target]; !ok {
		return "", "", false
	}
	state := s.states[target]
	if state == nil {
		return "online", "", true
	}
	return state.status, state.lastError, true
}
