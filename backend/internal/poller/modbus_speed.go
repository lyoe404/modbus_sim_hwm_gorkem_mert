package poller

import (
	"backend/internal/speed"
	"context"
	"fmt"
	"github.com/simonvetter/modbus"
	"sync"
	"time"
)

// SpeedReader reads the simulator's holding registers: duty 0..100%, tachometer 0..3000 RPM.
type SpeedReader struct {
	URL        string
	Timeout    time.Duration
	mu         sync.Mutex
	c          *modbus.ModbusClient
	feedbackMu sync.Mutex
	feedback   speed.FeedbackTracker
}

func (s *SpeedReader) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c != nil {
		_ = s.c.Close()
		s.c = nil
	}
	return nil
}
func (s *SpeedReader) Read(ctx context.Context) ([]Measurement, error) {
	commit, err := s.readStaged(ctx)
	if err != nil {
		s.Invalidate()
		return nil, err
	}
	return commit(), nil
}

func (s *SpeedReader) Invalidate() {
	s.feedbackMu.Lock()
	defer s.feedbackMu.Unlock()
	s.feedback = speed.FeedbackTracker{}
}

func (s *SpeedReader) readStaged(ctx context.Context) (func() []Measurement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c == nil {
		c, err := modbus.NewClient(&modbus.ClientConfiguration{URL: s.URL, Timeout: s.Timeout})
		if err != nil {
			return nil, err
		}
		if err = c.Open(); err != nil {
			return nil, err
		}
		s.c = c
	}
	regs, err := s.c.ReadRegisters(0, 2, modbus.HOLDING_REGISTER)
	if err != nil {
		_ = s.c.Close()
		s.c = nil
		return nil, fmt.Errorf("speed FC3: %w", err)
	}
	if regs[0] > 100 || regs[1] > 10000 {
		return nil, fmt.Errorf("%w: speed registers %v", ErrBadData, regs)
	}
	now := time.Now()
	return func() []Measurement {
		s.feedbackMu.Lock()
		mismatch := s.feedback.Observe(regs[0], regs[1], now)
		s.feedbackMu.Unlock()
		flag := 0.0
		if mismatch {
			flag = 1
		}
		return []Measurement{
			{Source: "fan_speed", Metric: "command_pct", Value: float64(regs[0]), At: now},
			{Source: "fan_speed", Metric: "feedback_rpm", Value: float64(regs[1]), At: now},
			{Source: "fan_speed", Metric: "mismatch", Value: flag, At: now},
		}
	}, nil
}
