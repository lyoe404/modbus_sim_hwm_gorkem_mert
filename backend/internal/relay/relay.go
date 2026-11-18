// Package relay switches the relay card's channels over Modbus TCP and
// confirms every change with a read-back.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/simonvetter/modbus"
)

// Distinct failure kinds, so callers (and the UI) can tell them apart.
var (
	ErrInvalidChannel   = errors.New("invalid relay channel")
	ErrWriteFailed      = errors.New("relay write failed")       // coil write was rejected / no answer
	ErrReadBackFailed   = errors.New("relay read-back failed")   // write may have applied; state unknown
	ErrReadBackMismatch = errors.New("relay read-back mismatch") // device answered, but state != requested
)

// MaxChannels is the size of the simulated relay card (coils 0..7).
const MaxChannels = 8

// Channel is one relay with its configured label and current (read-back) state.
type Channel struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
	On    bool   `json:"on"`
}

// Result describes a Set call: what was asked and what the device reports.
type Result struct {
	Channel   int    `json:"channel"`
	Label     string `json:"label"`
	Requested bool   `json:"requested"`
	Actual    bool   `json:"actual"` // from read-back; meaningful only if the read-back succeeded
}

// Controller talks to the relay card. Safe for concurrent use.
type Controller struct {
	URL     string        // e.g. tcp://127.0.0.1:5020
	Timeout time.Duration // per-request timeout
	Labels  []string      // one per channel; len(Labels) = number of channels in use

	mu sync.Mutex
	c  *modbus.ModbusClient
}

func (r *Controller) connect() error {
	if r.c != nil {
		return nil
	}
	c, err := modbus.NewClient(&modbus.ClientConfiguration{URL: r.URL, Timeout: r.Timeout})
	if err != nil {
		return err
	}
	if err := c.Open(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	r.c = c
	return nil
}

func (r *Controller) drop() {
	if r.c != nil {
		_ = r.c.Close()
		r.c = nil
	}
}

// Close releases the connection.
func (r *Controller) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drop()
	return nil
}

// Channels reads the real state of every configured channel from the device.
func (r *Controller) Channels(ctx context.Context) ([]Channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := len(r.Labels)
	if n == 0 || n > MaxChannels {
		return nil, fmt.Errorf("%w: %d labels configured, want 1..%d", ErrInvalidChannel, n, MaxChannels)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.connect(); err != nil {
		return nil, err
	}
	states, err := r.c.ReadCoils(0, uint16(n))
	if err != nil {
		r.drop()
		return nil, fmt.Errorf("read coils: %w", err)
	}
	out := make([]Channel, n)
	for i := range out {
		out[i] = Channel{ID: i, Label: r.Labels[i], On: states[i]}
	}
	return out, nil
}

// Set switches one channel and confirms it by reading the coil back. The
// returned Result reflects the device state, never just the command.
//
// Errors: ErrInvalidChannel, ErrWriteFailed, ErrReadBackFailed (state unknown),
// ErrReadBackMismatch (device is not in the requested state; Result.Actual says what it is).
func (r *Controller) Set(ctx context.Context, id int, on bool) (Result, error) {
	res := Result{Channel: id, Requested: on}
	if id < 0 || id >= len(r.Labels) || id >= MaxChannels {
		return res, fmt.Errorf("%w: %d", ErrInvalidChannel, id)
	}
	res.Label = r.Labels[id]
	if err := ctx.Err(); err != nil {
		return res, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.connect(); err != nil {
		return res, fmt.Errorf("%w: %w", ErrWriteFailed, err)
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if err := r.c.WriteCoil(uint16(id), on); err != nil {
		if errors.Is(err, modbus.ErrIllegalFunction) {
			if err := ctx.Err(); err != nil {
				return res, err
			}
			err = r.c.WriteCoils(uint16(id), []bool{on})
		}
		if err != nil {
			r.drop()
			return res, fmt.Errorf("%w: %w", ErrWriteFailed, err)
		}
	}
	actual, err := r.c.ReadCoil(uint16(id))
	if err != nil {
		r.drop()
		return res, fmt.Errorf("%w: %w", ErrReadBackFailed, err)
	}
	res.Actual = actual
	if actual != on {
		return res, fmt.Errorf("%w: requested %v, device reports %v", ErrReadBackMismatch, on, actual)
	}
	return res, nil
}

// DefaultLabels returns n generic labels.
func DefaultLabels(n int) []string {
	base := []string{"Fan 1", "Heater", "Lighting", "Aux Power"}
	out := make([]string, n)
	for i := range out {
		if i < len(base) {
			out[i] = base[i]
		} else {
			out[i] = fmt.Sprintf("Channel %d", i+1)
		}
	}
	return out
}

// LoadLabels reads a JSON array of strings (e.g. ["Fan 1","Heater",...]).
// An empty path returns the defaults, so renaming a channel is a config edit, not a code change.
func LoadLabels(path string) ([]string, error) {
	if path == "" {
		return DefaultLabels(MaxChannels), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var labels []string
	if err := json.Unmarshal(b, &labels); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(labels) == 0 || len(labels) > MaxChannels {
		return nil, fmt.Errorf("%s: need 1..%d labels, got %d", path, MaxChannels, len(labels))
	}
	return labels, nil
}
