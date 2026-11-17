package speed

import (
	"context"
	"errors"
	"github.com/simonvetter/modbus"
	"sync"
	"testing"
	"time"
)

type pwmDevice struct {
	mu           sync.Mutex
	command, rpm uint16
	ignore       bool
}

func (h *pwmDevice) HandleHoldingRegisters(r *modbus.HoldingRegistersRequest) ([]uint16, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.IsWrite {
		if !h.ignore {
			h.command = r.Args[0]
		}
		return nil, nil
	}
	return []uint16{h.command, h.rpm}, nil
}
func (h *pwmDevice) HandleInputRegisters(*modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}
func (h *pwmDevice) HandleCoils(*modbus.CoilsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}
func (h *pwmDevice) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}
func TestPWMReadbackRampMismatchAndRecovery(t *testing.T) {
	h := &pwmDevice{}
	url := "tcp://127.0.0.1:25184"
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: url, Timeout: time.Second, MaxClients: 3}, h)
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	c := &Controller{URL: url, Timeout: time.Second}
	defer c.Close()
	ctx := context.Background()
	st, err := c.Set(ctx, 100)
	if err != nil || st.CommandPct != 100 || st.Mismatch {
		t.Fatal(st, err)
	}
	c.mu.Lock()
	c.commandAt = time.Now().Add(-6 * time.Second)
	c.mismatchAt = c.commandAt
	c.mu.Unlock()
	st, err = c.Status(ctx)
	if err != nil || !st.Mismatch {
		t.Fatal("sustained fault not visible", st, err)
	}
	h.mu.Lock()
	h.rpm = 3000
	h.mu.Unlock()
	st, err = c.Status(ctx)
	if err != nil || st.Mismatch {
		t.Fatal("recovery", st, err)
	}
	h.mu.Lock()
	h.ignore = true
	h.mu.Unlock()
	if _, err = c.Set(ctx, 40); !errors.Is(err, ErrMismatch) {
		t.Fatal("unverified write accepted", err)
	}
}
