// Package poller collects measurements from field devices, one goroutine per
// device, with retry/backoff and an honest connection status.
package poller

import (
	"context"
	"time"
)

// Measurement is one timestamped value from a device.
type Measurement struct {
	Source string // e.g. "sensor"
	Metric string // e.g. "temperature_c"
	Value  float64
	At     time.Time
}

// Reader reads one batch of measurements from a device. Implementations own
// their connection and must reconnect on their own after a failure.
type Reader interface {
	Read(ctx context.Context) ([]Measurement, error)
	Close() error
}

// Sink persists measurements (database, in-memory, log...).
type Sink interface {
	Save(ctx context.Context, ms []Measurement) error
}

// State is the connection state shown to operators.
type State string

const (
	StateUnknown  State = "unknown"  // nothing polled yet
	StateOnline   State = "online"   // last poll succeeded
	StateDegraded State = "degraded" // recent failures, still retrying
	StateOffline  State = "offline"  // repeated failures
)

// DeviceStatus is a point-in-time view of a device's connectivity.
type DeviceStatus struct {
	Name                string    `json:"name"`
	State               State     `json:"state"`
	LastSeen            time.Time `json:"last_seen"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}
