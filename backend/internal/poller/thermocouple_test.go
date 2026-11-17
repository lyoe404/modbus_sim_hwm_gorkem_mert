package poller

import (
	"context"
	"testing"
	"time"
)

func TestThermocoupleIndependentFaultNegativeRecovery(t *testing.T) {
	tpc := &Thermocouple{}
	now := time.Now()
	ms := tpc.decode([]uint16{1200, 0x8000, 0xffff, 0xff9c}, now)
	if len(ms) != 3 || ms[1].Value != -0.1 || ms[2].Value != -10 {
		t.Fatalf("signed samples: %+v", ms)
	}
	ch := tpc.Channels()
	if ch[1].Status != "fault" || ch[1].Temperature != nil || ch[0].Status != "live" {
		t.Fatalf("channels: %+v", ch)
	}
	previous := ch[0].LastValid
	ms = tpc.decode([]uint16{0x8000, 0x8000, 0x8000, 0x8000}, now.Add(time.Second))
	if len(ms) != 0 {
		t.Fatal("faults produced measurements")
	}
	ch = tpc.Channels()
	for _, c := range ch {
		if c.Status != "fault" || c.Temperature != nil {
			t.Fatal(c)
		}
	}
	if !ch[0].LastValid.Equal(previous) {
		t.Fatal("fault refreshed last valid time")
	}
	tpc.decode([]uint16{100, 200, 300, 400}, now.Add(2*time.Second))
	ch = tpc.Channels()
	for _, c := range ch {
		if c.Status != "live" || c.Temperature == nil {
			t.Fatal(c)
		}
	}
	tpc.Invalidate()
	for _, c := range tpc.Channels() {
		if c.Status != "unknown" || c.Temperature != nil {
			t.Fatal(c)
		}
	}
}

func TestResetGenerationAndFreshReadRecovery(t *testing.T) {
	reader := &fakeReader{}
	sink := &memSink{}
	p := New(Config{Name: "device", Interval: time.Millisecond, Backoff: Backoff{Base: time.Millisecond}}, reader, sink, nil, nil)
	p.BeginReset(func(context.Context) (string, error) { return "online", nil })
	p.BeginReset(func(context.Context) (string, error) { return "offline", nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	time.Sleep(15 * time.Millisecond)
	if p.Status().State != StateOffline {
		t.Fatal(p.Status())
	}
	sink.mu.Lock()
	n := sink.n
	sink.mu.Unlock()
	if n != 0 {
		t.Fatal("old recovery produced live data")
	}
	p.BeginReset(func(context.Context) (string, error) { return "online", nil })
	deadline := time.Now().Add(time.Second)
	for p.Status().State != StateOnline && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.Status().State != StateOnline || p.Status().LastSeen.IsZero() {
		t.Fatal(p.Status())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
}
