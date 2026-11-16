package poller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBackoffDelay(t *testing.T) {
	b := Backoff{Base: time.Second, Max: 8 * time.Second}
	want := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for failures, w := range want {
		if got := b.Delay(failures); got != w {
			t.Errorf("Delay(%d) = %v, want %v", failures, got, w)
		}
	}
}

type fakeReader struct {
	mu    sync.Mutex
	fails int
}

func (f *fakeReader) Read(context.Context) ([]Measurement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails > 0 {
		f.fails--
		return nil, errors.New("boom")
	}
	return []Measurement{{Source: "s", Metric: "temperature_c", Value: 25, At: time.Now()}}, nil
}
func (f *fakeReader) Close() error { return nil }

type memSink struct {
	mu sync.Mutex
	n  int
}

func (s *memSink) Save(_ context.Context, ms []Measurement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n += len(ms)
	return nil
}

func TestPollerStatusTransitionsAndShutdown(t *testing.T) {
	var mu sync.Mutex
	var seen []State
	cfg := Config{
		Name: "fake", Interval: 5 * time.Millisecond, OfflineAfter: 3,
		Backoff: Backoff{Base: 5 * time.Millisecond, Max: 10 * time.Millisecond},
	}
	sink := &memSink{}
	p := New(cfg, &fakeReader{fails: 3}, sink, nil, func(s DeviceStatus) {
		mu.Lock()
		seen = append(seen, s.State)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && p.Status().State != StateOnline {
		time.Sleep(5 * time.Millisecond)
	}
	if p.Status().State != StateOnline {
		t.Fatalf("never recovered, status=%+v", p.Status())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	want := []State{StateDegraded, StateOffline, StateOnline}
	if len(seen) != len(want) {
		t.Fatalf("transitions = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("transitions = %v, want %v", seen, want)
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.n == 0 {
		t.Fatal("nothing persisted after recovery")
	}
}
