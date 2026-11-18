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

type stagedThermocouple struct {
	Thermocouple
	stage func() (func() []Measurement, error)
}

func (r *stagedThermocouple) readStaged(context.Context) (func() []Measurement, error) {
	return r.stage()
}

func TestResetDropsInFlightSnapshotAndPublishesRecoveryOutcome(t *testing.T) {
	now := time.Now()
	reader := &stagedThermocouple{}
	reader.decode([]uint16{100, 200, 300, 400}, now)
	readStarted, releaseRead := make(chan struct{}), make(chan struct{})
	reader.stage = func() (func() []Measurement, error) {
		close(readStarted)
		<-releaseRead
		return func() []Measurement {
			return reader.decode([]uint16{900, 900, 900, 900}, now.Add(time.Second))
		}, nil
	}
	var states []State
	sink := &memSink{}
	p := New(Config{Name: "thermocouple"}, reader, sink, nil, nil)
	p.SetOnPoll(func(st DeviceStatus) { states = append(states, st.State) })
	done := make(chan struct{})
	go func() { defer close(done); p.poll(t.Context()) }()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}
	p.BeginReset(func(context.Context) (string, error) { return string(StateRecoveryFailed), nil })
	if ch := reader.Channels()[0]; ch.Status != "unknown" || ch.Temperature != nil || !ch.LastValid.Equal(now) {
		t.Fatalf("reset did not immediately invalidate current telemetry: %+v", ch)
	}
	close(releaseRead)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("original read did not return")
	}
	if ch := reader.Channels()[0]; ch.Status != "unknown" || !ch.LastValid.Equal(now) || sink.n != 0 {
		t.Fatalf("discarded read changed cache or history: %+v, history=%d", ch, sink.n)
	}
	p.poll(t.Context())
	if st := p.Status(); st.State != StateRecoveryFailed || !st.LastSeen.IsZero() {
		t.Fatalf("failed recovery should stay unavailable: %+v", st)
	}
	p.BeginReset(func(context.Context) (string, error) { return string(StateOnline), nil })
	reader.stage = func() (func() []Measurement, error) {
		if p.Status().State == StateOnline {
			t.Error("reset service status alone marked the device online")
		}
		return func() []Measurement {
			return reader.decode([]uint16{500, 600, 700, 800}, now.Add(2*time.Second))
		}, nil
	}
	p.poll(t.Context())
	if st := p.Status(); st.State != StateOnline || st.LastSeen.IsZero() || sink.n == 0 {
		t.Fatalf("fresh read did not verify recovery: %+v, history=%d", st, sink.n)
	}
	if len(states) != 4 || states[0] != StateRecovering || states[1] != StateRecoveryFailed ||
		states[2] != StateRecovering || states[3] != StateOnline {
		t.Fatalf("persistence callbacks missed a reset transition: %v", states)
	}
}

func TestNewResetSupersedesAlreadyRunningRecoveryProbe(t *testing.T) {
	probing, release := make(chan struct{}), make(chan struct{})
	reader := &stagedThermocouple{stage: func() (func() []Measurement, error) {
		t.Error("superseded probe started a device read")
		return func() []Measurement { return nil }, nil
	}}
	p := New(Config{Name: "thermocouple"}, reader, &memSink{}, nil, nil)
	p.BeginReset(func(context.Context) (string, error) {
		close(probing)
		<-release
		return string(StateOnline), nil
	})
	done := make(chan struct{})
	go func() { defer close(done); p.poll(t.Context()) }()
	select {
	case <-probing:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("recovery probe did not start")
	}
	p.BeginReset(func(context.Context) (string, error) { return string(StateOffline), nil })
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old recovery probe did not complete")
	}
	p.poll(t.Context())
	if st := p.Status(); st.State != StateOffline || !st.LastSeen.IsZero() {
		t.Fatalf("superseded probe reopened the device: %+v", st)
	}
}
