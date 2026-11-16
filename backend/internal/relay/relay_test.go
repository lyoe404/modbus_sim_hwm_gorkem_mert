package relay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/simonvetter/modbus"
)

// coilHandler is an in-test Modbus relay card with switchable misbehaviour.
type coilHandler struct {
	mu           sync.Mutex
	coils        [MaxChannels]bool
	failWrites   bool // device rejects writes
	ignoreWrites bool // device accepts writes but does not change state
}

func (h *coilHandler) set(fail, ignore bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failWrites, h.ignoreWrites = fail, ignore
}

func (h *coilHandler) HandleCoils(req *modbus.CoilsRequest) ([]bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if req.IsWrite {
		if int(req.Addr)+len(req.Args) > MaxChannels {
			return nil, modbus.ErrIllegalDataAddress
		}
		if h.failWrites {
			return nil, modbus.ErrServerDeviceFailure
		}
		if !h.ignoreWrites {
			copy(h.coils[req.Addr:], req.Args)
		}
		return nil, nil
	}
	if int(req.Addr)+int(req.Quantity) > MaxChannels {
		return nil, modbus.ErrIllegalDataAddress
	}
	return append([]bool(nil), h.coils[req.Addr:req.Addr+req.Quantity]...), nil
}
func (h *coilHandler) HandleDiscreteInputs(*modbus.DiscreteInputsRequest) ([]bool, error) {
	return nil, modbus.ErrIllegalFunction
}
func (h *coilHandler) HandleHoldingRegisters(*modbus.HoldingRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}
func (h *coilHandler) HandleInputRegisters(*modbus.InputRegistersRequest) ([]uint16, error) {
	return nil, modbus.ErrIllegalFunction
}

func newController(t *testing.T, port int) (*Controller, *coilHandler) {
	t.Helper()
	h := &coilHandler{}
	url := fmt.Sprintf("tcp://127.0.0.1:%d", port)
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: url, Timeout: 30 * time.Second, MaxClients: 5}, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	c := &Controller{URL: url, Timeout: 500 * time.Millisecond, Labels: DefaultLabels(MaxChannels)}
	t.Cleanup(func() { _ = c.Close() })
	return c, h
}

func TestSetConfirmsWithReadBack(t *testing.T) {
	c, _ := newController(t, 25030)
	ctx := context.Background()

	res, err := c.Set(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Actual || res.Label != "Heater" {
		t.Fatalf("result = %+v", res)
	}

	chs, err := c.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range chs {
		if want := ch.ID == 1; ch.On != want {
			t.Fatalf("channel %d on=%v, want %v", ch.ID, ch.On, want)
		}
	}
}

func TestInvalidChannel(t *testing.T) {
	c, _ := newController(t, 25031)
	for _, id := range []int{-1, 8, 99} {
		if _, err := c.Set(context.Background(), id, true); !errors.Is(err, ErrInvalidChannel) {
			t.Fatalf("id %d: err = %v, want ErrInvalidChannel", id, err)
		}
	}
}

func TestWriteFailureIsDistinct(t *testing.T) {
	c, h := newController(t, 25032)
	h.set(true, false)
	_, err := c.Set(context.Background(), 0, true)
	if !errors.Is(err, ErrWriteFailed) {
		t.Fatalf("err = %v, want ErrWriteFailed", err)
	}
	if errors.Is(err, ErrReadBackMismatch) {
		t.Fatal("write failure must not look like a mismatch")
	}
}

func TestReadBackMismatchIsDetected(t *testing.T) {
	c, h := newController(t, 25033)
	h.set(false, true) // device says OK but does not actuate
	res, err := c.Set(context.Background(), 2, true)
	if !errors.Is(err, ErrReadBackMismatch) {
		t.Fatalf("err = %v, want ErrReadBackMismatch", err)
	}
	if res.Requested != true || res.Actual != false {
		t.Fatalf("result must show the TRUE state, got %+v", res)
	}
}

func TestLoadLabels(t *testing.T) {
	if got, err := LoadLabels(""); err != nil || len(got) != MaxChannels {
		t.Fatalf("defaults: %v %v", got, err)
	}
	p := filepath.Join(t.TempDir(), "labels.json")
	if err := os.WriteFile(p, []byte(`["Pump","Valve"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLabels(p)
	if err != nil || len(got) != 2 || got[0] != "Pump" {
		t.Fatalf("custom: %v %v", got, err)
	}
	if err := os.WriteFile(p, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLabels(p); err == nil {
		t.Fatal("empty label list should be rejected")
	}
}
