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
	URL      string
	Timeout  time.Duration
	mu       sync.Mutex
	c        *modbus.ModbusClient
	channels [4]ThermocoupleChannel
	observed time.Time
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
	defer func() {
		if time.Since(t.observed) > t.Timeout {
			t.unknownLocked()
		}
	}()
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
	return t.decode(regs, time.Now()), nil
}

type ThermocoupleChannel struct {
	Source      string    `json:"source"`
	Status      string    `json:"status"`
	Temperature *float64  `json:"temperature_c"`
	LastValid   time.Time `json:"last_valid_at"`
}

func (t *Thermocouple) unknownLocked() {
	for i := range t.channels {
		t.channels[i].Status = "unknown"
		t.channels[i].Temperature = nil
	}
}
func (t *Thermocouple) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.unknownLocked()
	t.observed = time.Time{}
}
func (t *Thermocouple) Channels() []ThermocoupleChannel {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.observed) > 6*time.Second {
		t.unknownLocked()
	}
	out := make([]ThermocoupleChannel, 4)
	for i := range out {
		out[i] = t.channels[i]
		out[i].Source = fmt.Sprintf("thermocouple_ch%d", i)
		if out[i].Status == "" {
			out[i].Status = "unknown"
		}
		if out[i].Temperature != nil {
			v := *out[i].Temperature
			out[i].Temperature = &v
		}
	}
	return out
}

// decode runs under mu; an empty batch means connected with no valid channels.
func (t *Thermocouple) decode(regs []uint16, now time.Time) []Measurement {
	t.observed = now
	out := make([]Measurement, 0, 4)
	for i, v := range regs {
		ch := &t.channels[i]
		ch.Source = fmt.Sprintf("thermocouple_ch%d", i)
		temp := float64(int16(v)) / 10
		ch.Temperature = nil
		ch.Status = "fault"
		if v == 0x8000 || temp < -200 || temp > 1300 {
			continue
		}
		ch.Status = "live"
		ch.Temperature = &temp
		ch.LastValid = now
		out = append(out, Measurement{Source: ch.Source, Metric: "temperature_c", Value: temp, At: now})
	}
	return out
}
