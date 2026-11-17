package poller

import (
	"context"
	"fmt"
	"github.com/simonvetter/modbus"
	"sync"
	"time"
)

// SpeedReader reads the simulator's holding registers: duty 0..100%, tachometer 0..3000 RPM.
type SpeedReader struct {
	URL     string
	Timeout time.Duration
	mu      sync.Mutex
	c       *modbus.ModbusClient
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
		return nil, fmt.Errorf("invalid speed registers: %v", regs)
	}
	now := time.Now()
	return []Measurement{{Source: "fan_speed", Metric: "command_pct", Value: float64(regs[0]), At: now}, {Source: "fan_speed", Metric: "feedback_rpm", Value: float64(regs[1]), At: now}}, nil
}
