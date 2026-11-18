package speed

import (
	"context"
	"errors"
	"fmt"
	"github.com/simonvetter/modbus"
	"sync"
	"time"
)

var ErrMismatch = errors.New("speed command/readback mismatch")

type State struct {
	CommandPct  uint16 `json:"command_pct"`
	FeedbackRPM uint16 `json:"feedback_rpm"`
	Mismatch    bool   `json:"mismatch"`
}
type Controller struct {
	URL         string
	Timeout     time.Duration
	mu          sync.Mutex
	c           *modbus.ModbusClient
	lastCommand uint16
	commandAt   time.Time
	mismatchAt  time.Time
}

func (s *Controller) closeLocked() {
	if s.c != nil {
		_ = s.c.Close()
		s.c = nil
	}
}
func (s *Controller) Close() error { s.mu.Lock(); defer s.mu.Unlock(); s.closeLocked(); return nil }
func (s *Controller) connect() error {
	if s.c != nil {
		return nil
	}
	c, err := modbus.NewClient(&modbus.ClientConfiguration{URL: s.URL, Timeout: s.Timeout})
	if err != nil {
		return err
	}
	if err = c.Open(); err != nil {
		return err
	}
	s.c = c
	return nil
}
func (s *Controller) read() (State, error) {
	regs, err := s.c.ReadRegisters(0, 2, modbus.HOLDING_REGISTER)
	if err != nil {
		s.closeLocked()
		return State{}, err
	}
	now := time.Now()
	if s.commandAt.IsZero() || s.lastCommand != regs[0] {
		s.lastCommand = regs[0]
		s.commandAt = now
		s.mismatchAt = time.Time{}
	}
	divergent := abs(int(regs[1])-int(regs[0])*30) > 300
	if !divergent {
		s.mismatchAt = time.Time{}
	} else if s.mismatchAt.IsZero() {
		s.mismatchAt = now
	}
	if regs[0] > 100 || regs[1] > 3000 {
		return State{}, errors.New("invalid PWM registers")
	}
	return State{CommandPct: regs[0], FeedbackRPM: regs[1], Mismatch: divergent && now.Sub(s.commandAt) >= 5*time.Second && now.Sub(s.mismatchAt) >= 5*time.Second}, nil
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
func (s *Controller) Status(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connect(); err != nil {
		return State{}, err
	}
	return s.read()
}
func (s *Controller) Set(ctx context.Context, pct int) (State, error) {
	if pct < 0 || pct > 100 {
		return State{}, errors.New("speed must be 0..100")
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connect(); err != nil {
		return State{}, err
	}
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	if err := s.c.WriteRegister(0, uint16(pct)); err != nil {
		s.closeLocked()
		return State{}, fmt.Errorf("speed write: %w", err)
	}
	st, err := s.read()
	if err != nil {
		return st, err
	}
	if st.CommandPct != uint16(pct) {
		return st, ErrMismatch
	}
	return st, nil
}
