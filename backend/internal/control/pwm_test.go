package control

import (
	"backend/internal/speed"
	"context"
	"errors"
	"testing"
	"time"
)

type pwmFake struct {
	pct    int
	writes int
	err    error
}

func (f *pwmFake) Status(context.Context) (speed.State, error) {
	return speed.State{CommandPct: uint16(f.pct)}, f.err
}
func (f *pwmFake) Set(_ context.Context, pct int) (speed.State, error) {
	f.pct = pct
	f.writes++
	return speed.State{CommandPct: uint16(pct)}, f.err
}
func TestSpeedCurve(t *testing.T) {
	for _, c := range []struct {
		temp float64
		pct  int
	}{{0, 0}, {25, 20}, {30, 60}, {35, 100}, {50, 100}} {
		if got := SpeedTarget(c.temp); got != c.pct {
			t.Fatalf("%v: %d", c.temp, got)
		}
	}
}
func TestPWMConfirmedRelayFreshnessAndManualMode(t *testing.T) {
	now := time.Now()
	pwm := &pwmFake{}
	rec := &fakeRecorder{}
	sw := &fakeSwitch{}
	e := New(Options{Speed: pwm, Switch: sw, Recorder: rec, Store: &fakeDB{}, Channels: 8, Now: func() time.Time { return now }})
	e.step(context.Background(), Reading{TempC: 35, At: now})
	if pwm.pct != 100 {
		t.Fatal(pwm)
	}
	e.step(context.Background(), Reading{TempC: 29, At: now})
	if pwm.pct != 52 {
		t.Fatal(pwm)
	}
	e.step(context.Background(), Reading{TempC: 20, At: now.Add(-time.Hour)})
	if pwm.pct != 52 {
		t.Fatal("stale reading acted")
	}
	e.step(context.Background(), Reading{TempC: 20, At: now})
	if pwm.pct != 0 {
		t.Fatal("off relay has nonzero PWM")
	}
	if err := e.ManualSpeedOperation(func() error { t.Fatal("AUTO manual action executed"); return nil }); !errors.Is(err, ErrFanAutoMode) {
		t.Fatal(err)
	}
	cfg := e.Snapshot().Config
	cfg.Enabled = false
	if err := e.UpdateConfig(context.Background(), cfg, "operator"); err != nil {
		t.Fatal(err)
	}
	if err := e.ManualSpeedOperation(func() error { pwm.pct = 77; return nil }); err != nil {
		t.Fatal(err)
	}
	e.step(context.Background(), Reading{TempC: 35, At: now})
	if pwm.pct != 77 {
		t.Fatal("AUTO acted in MANUAL")
	}
	if err := e.ResetOperation(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().FanOn != nil {
		t.Fatal("reset kept fan belief")
	}
}

func TestManualSpeedSerializesModeChange(t *testing.T) {
	e := New(Options{Store: &fakeDB{}, Recorder: &fakeRecorder{}, Channels: 8})
	cfg := e.Snapshot().Config
	cfg.Enabled = false
	if err := e.UpdateConfig(context.Background(), cfg, "operator"); err != nil {
		t.Fatal(err)
	}
	entered, release, manualDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(manualDone)
		e.ManualSpeedOperation(func() error { close(entered); <-release; return nil })
	}()
	<-entered
	cfg.Enabled = true
	modeDone := make(chan error, 1)
	go func() { modeDone <- e.UpdateConfig(context.Background(), cfg, "operator") }()
	select {
	case err := <-modeDone:
		t.Fatalf("mode overtook active manual write: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-manualDone
	if err := <-modeDone; err != nil {
		t.Fatal(err)
	}
	if err := e.ManualSpeedOperation(func() error { t.Error("manual action in AUTO"); return nil }); !errors.Is(err, ErrFanAutoMode) {
		t.Fatal(err)
	}
}

type fanReaderFunc func(context.Context, int) (bool, error)

func (f fanReaderFunc) ReadFan(ctx context.Context, channel int) (bool, error) {
	return f(ctx, channel)
}

type slowPWM struct {
	pwmFake
	started, release chan struct{}
}

func (f *slowPWM) Status(context.Context) (speed.State, error) {
	close(f.started)
	<-f.release
	return speed.State{}, nil
}

func TestSensorLossInvalidatesQueuedAndInFlightRelayWork(t *testing.T) {
	for _, queued := range []bool{true, false} {
		name := "in-flight feedback"
		if queued {
			name = "queued sample"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			h.e.fanReader = fanReaderFunc(func(context.Context, int) (bool, error) {
				close(started)
				<-release
				return false, nil
			})
			old := Reading{TempC: 35, At: h.now}
			h.e.Observe(old)
			if queued {
				h.e.OnStatus(t.Context(), false)
			}
			go func() { defer close(done); h.e.step(t.Context(), <-h.e.latest) }()
			if !queued {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("fan feedback did not start")
				}
				h.e.OnStatus(t.Context(), false)
			}
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("step did not finish")
			}
			if h.sw.count() != 0 || !h.e.Snapshot().Suspended {
				t.Fatal("pre-fault sample acted or resumed control")
			}
			h.now = h.now.Add(time.Second)
			h.e.fanReader = nil
			h.feed(35)
			if h.sw.count() != 1 || h.e.Snapshot().Suspended {
				t.Fatal("fresh reading did not resume AUTO")
			}
		})
	}
}

func TestSensorLossDuringPWMFeedbackPreventsWrite(t *testing.T) {
	h := newHarness()
	h.e.ObserveManualRelay(0, true)
	pwm := &slowPWM{started: make(chan struct{}), release: make(chan struct{})}
	h.e.speed = pwm
	done := make(chan struct{})
	go func() { defer close(done); h.feed(35) }()
	select {
	case <-pwm.started:
	case <-time.After(time.Second):
		t.Fatal("PWM feedback did not start")
	}
	h.e.OnStatus(t.Context(), false)
	close(pwm.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("step did not finish")
	}
	if pwm.writes != 0 {
		t.Fatal("pre-fault sample reached the PWM device")
	}
}
