package poller

import (
	"context"
	"fmt"
	"github.com/simonvetter/modbus"
	"sync"
	"time"
)

// Thermocouple reads the simulator's four FC4 input registers (0..3).
// Faulted channels use 0x8000 and are omitted rather than persisted as zero.
type Thermocouple struct {
	URL     string
	Timeout time.Duration
	mu      sync.Mutex
	c       *modbus.ModbusClient
}

func (t *Thermocouple) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.c != nil {
		_ = t.c.Close()
		t.c = nil
	}
	return nil
}
func (t *Thermocouple) Read(ctx context.Context) ([]Measurement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.c == nil {
		c, err := modbus.NewClient(&modbus.ClientConfiguration{URL: t.URL, Timeout: t.Timeout})
		if err != nil {
			return nil, err
		}
		if err = c.Open(); err != nil {
			return nil, err
		}
		t.c = c
	}
	regs, err := t.c.ReadRegisters(0, 4, modbus.INPUT_REGISTER)
	if err != nil {
		_ = t.c.Close()
		t.c = nil
		return nil, fmt.Errorf("thermocouple FC4: %w", err)
	}
	now := time.Now()
	out := make([]Measurement, 0, 4)
	for i, v := range regs {
		if v == 0x8000 || v == 0xffff {
			continue
		}
		temp := float64(int16(v)) / 10
		if temp < -200 || temp > 1300 {
			continue
		}
		out = append(out, Measurement{Source: fmt.Sprintf("thermocouple_ch%d", i), Metric: "temperature_c", Value: temp, At: now})
	}
	return out, nil
}
