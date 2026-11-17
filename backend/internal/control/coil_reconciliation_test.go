package control

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type coilDevice struct {
	mu      sync.Mutex
	actual  bool
	writes  []bool
	readErr error
}

func (d *coilDevice) ReadFan(_ context.Context, _ int) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.actual, d.readErr
}
func (d *coilDevice) SetChannel(_ context.Context, _ int, on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.actual = on
	d.writes = append(d.writes, on)
	return nil
}
func (d *coilDevice) external(on bool) { d.mu.Lock(); d.actual = on; d.mu.Unlock() }
func (d *coilDevice) writeCount() int  { d.mu.Lock(); defer d.mu.Unlock(); return len(d.writes) }

func TestCoilFeedbackReconciliationAndHysteresisIntegration(t *testing.T) {
	dev := &coilDevice{}
	logs := &fakeRecorder{}
	now := time.Now()
	e := New(Options{Switch: dev, FanReader: dev, Recorder: logs,
		Store: &fakeDB{}, Channels: 8, MaxAge: time.Minute,
		Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ctx := context.Background()
	samples := []struct {
		temp   float64
		actual bool
	}{
		{25, false}, {28, false}, {31, true}, {29, true}, {26, false},
	}
	for _, sample := range samples {
		e.step(ctx, Reading{TempC: sample.temp, At: now})
		dev.mu.Lock()
		actual := dev.actual
		dev.mu.Unlock()
		if actual != sample.actual {
			t.Fatalf("temp %v: actual %v, want %v", sample.temp, actual, sample.actual)
		}
	}
	if got := dev.writeCount(); got != 2 {
		t.Fatalf("writes=%d, want 2", got)
	}
	if got := logs.count("fan_transition"); got != 2 {
		t.Fatalf("transitions=%d, want 2", got)
	}
	// A second Modbus client changes the real coil inside the band.
	dev.external(true)
	e.step(ctx, Reading{TempC: 28, At: now})
	if e.Snapshot().FanOn == nil || !*e.Snapshot().FanOn {
		t.Fatal("external coil state not reconciled")
	}
	if got := dev.writeCount(); got != 2 {
		t.Fatalf("in-band external change should not cause write, got %d", got)
	}
	// After reading 26°C, the controller correctly returns to OFF.
	e.step(ctx, Reading{TempC: 26, At: now})
	if got := dev.writeCount(); got != 3 {
		t.Fatalf("required correction missing, writes=%d", got)
	}
}
