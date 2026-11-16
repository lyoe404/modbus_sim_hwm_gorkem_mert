package poller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/simonvetter/modbus"
)

// ErrBadData means the device answered but the values are implausible.
var ErrBadData = errors.New("implausible device data")

// ModbusSensor reads the temperature/humidity module over Modbus TCP.
// Register map: input reg 0 = temp °C x10, input reg 1 = humidity % x10.
type ModbusSensor struct {
	Source  string // measurement source tag
	URL     string // e.g. tcp://modbus-sim:5020
	Timeout time.Duration

	mu sync.Mutex // Read and Close may come from different goroutines
	c  *modbus.ModbusClient
}

func (m *ModbusSensor) connect() error {
	if m.c != nil {
		return nil
	}
	c, err := modbus.NewClient(&modbus.ClientConfiguration{URL: m.URL, Timeout: m.Timeout})
	if err != nil {
		return err
	}
	if err := c.Open(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	m.c = c
	return nil
}

func (m *ModbusSensor) drop() {
	if m.c != nil {
		_ = m.c.Close()
		m.c = nil
	}
}

// Read implements Reader. Any transport error drops the connection so the
// next call reconnects from scratch.
func (m *ModbusSensor) Read(ctx context.Context) ([]Measurement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.connect(); err != nil {
		return nil, err
	}
	regs, err := m.c.ReadRegisters(0, 2, modbus.INPUT_REGISTER)
	if err != nil {
		m.drop()
		return nil, fmt.Errorf("read input registers: %w", err)
	}

	temp, hum := float64(regs[0])/10, float64(regs[1])/10
	if regs[0] == 0xFFFF || regs[1] == 0xFFFF || temp < -40 || temp > 125 || hum < 0 || hum > 100 {
		return nil, fmt.Errorf("%w: temp=%v humidity=%v", ErrBadData, temp, hum)
	}
	now := time.Now()
	return []Measurement{
		{Source: m.Source, Metric: "temperature_c", Value: temp, At: now},
		{Source: m.Source, Metric: "humidity_pct", Value: hum, At: now},
	}, nil
}

// Close implements Reader.
func (m *ModbusSensor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.drop()
	return nil
}
