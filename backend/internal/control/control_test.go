package control

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- pure rule ----

func TestNextFanSweepGivesExactlyOneOnAndOneOff(t *testing.T) {
	on, ons, offs := false, 0, 0
	step := func(temp float64) {
		next := NextFan(temp, 30, 27, on)
		if next && !on {
			ons++
		}
		if !next && on {
			offs++
		}
		on = next
	}
	for temp := 20.0; temp <= 35.0; temp += 0.1 { // up through the band
		step(temp)
	}
	for temp := 35.0; temp >= 20.0; temp -= 0.1 { // and back down
		step(temp)
	}
	if ons != 1 || offs != 1 {
		t.Fatalf("on transitions=%d off transitions=%d, want 1 and 1", ons, offs)
	}
}

func TestNextFanHoldsInsideBandAndAtBoundaries(t *testing.T) {
	for _, temp := range []float64{27, 28.5, 30} {
		if NextFan(temp, 30, 27, false) {
			t.Errorf("off fan must stay off at %v", temp)
		}
		if !NextFan(temp, 30, 27, true) {
			t.Errorf("on fan must stay on at %v", temp)
		}
	}
}

func TestNextFanJitterAroundOneThresholdDoesNotOscillate(t *testing.T) {
	on, flips := false, 0
	for _, temp := range []float64{30.1, 29.9, 30.1, 29.9, 30.2, 29.8} {
		n := NextFan(temp, 30, 27, on)
		if n != on {
			flips++
		}
		on = n
	}
	if flips != 1 {
		t.Fatalf("flips = %d, want 1 (only the first crossing)", flips)
	}
}

func TestConfigValidate(t *testing.T) {
	good := DefaultConfig()
	if err := good.Validate(8); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Config{
		"equal thresholds":   {Enabled: true, OnAbove: 30, OffBelow: 30},
		"inverted":           {Enabled: true, OnAbove: 25, OffBelow: 30},
		"nan":                {Enabled: true, OnAbove: math.NaN(), OffBelow: 20},
		"out of range":       {Enabled: true, OnAbove: 500, OffBelow: 20},
		"channel negative":   {Enabled: true, OnAbove: 30, OffBelow: 27, FanChannel: -1},
		"channel past count": {Enabled: true, OnAbove: 30, OffBelow: 27, FanChannel: 8},
	}
	for name, c := range bad {
		if err := c.Validate(8); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidConfig", name, err)
		}
	}
}

// ---- engine with fakes ----

type fakeSwitch struct {
	mu    sync.Mutex
	calls []bool
	chans []int
	err   error
}

func (f *fakeSwitch) SetChannel(_ context.Context, ch int, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, on)
	f.chans = append(f.chans, ch)
	return f.err
}
func (f *fakeSwitch) setErr(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeSwitch) count() int       { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

type rec struct{ kind, source, msg, actor string }

type fakeRecorder struct {
	mu sync.Mutex
	ev []rec
}

func (f *fakeRecorder) Record(_ context.Context, kind, source, msg, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ev = append(f.ev, rec{kind, source, msg, actor})
	return nil
}
func (f *fakeRecorder) kinds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.ev {
		out = append(out, e.kind)
	}
	return out
}
func (f *fakeRecorder) count(kind string) int {
	n := 0
	for _, k := range f.kinds() {
		if k == kind {
			n++
		}
	}
	return n
}

type fakeDB struct {
	mu      sync.Mutex
	vals    map[string]string
	putErr  error
	putCall int
}

func (f *fakeDB) GetConfig(_ context.Context, k string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.vals[k]
	return v, ok, nil
}
func (f *fakeDB) PutConfig(_ context.Context, k, v, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCall++
	if f.putErr != nil {
		return f.putErr
	}
	if f.vals == nil {
		f.vals = map[string]string{}
	}
	f.vals[k] = v
	return nil
}

type harness struct {
	e   *Engine
	sw  *fakeSwitch
	rec *fakeRecorder
	db  *fakeDB
	now time.Time
}

func newHarness() *harness {
	h := &harness{sw: &fakeSwitch{}, rec: &fakeRecorder{}, db: &fakeDB{}, now: time.Unix(1_700_000_000, 0)}
	h.e = New(Options{
		Switch: h.sw, Recorder: h.rec, Store: h.db,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Channels: 8, MaxAge: 6 * time.Second,
		Now: func() time.Time { return h.now },
	})
	return h
}

func (h *harness) feed(temp float64) { h.e.step(context.Background(), Reading{TempC: temp, At: h.now}) }

func TestEngineSweepWritesOneOnOneOffWithTriggerValues(t *testing.T) {
	h := newHarness()
	h.feed(25) // below band, fan state unknown: enforce "off" once
	for temp := 25.0; temp <= 33; temp += 0.5 {
		h.feed(temp)
	}
	for temp := 33.0; temp >= 24; temp -= 0.5 {
		h.feed(temp)
	}
	if got := h.rec.count("fan_transition"); got != 2 {
		t.Fatalf("fan_transition events = %d (%v), want 2", got, h.rec.kinds())
	}
	// 25 °C at start is below the band with unknown state: one sync-off, then on, then off.
	if want := []bool{false, true, false}; len(h.sw.calls) != 3 || h.sw.calls[0] != want[0] || h.sw.calls[1] != want[1] || h.sw.calls[2] != want[2] {
		t.Fatalf("switch calls = %v, want %v", h.sw.calls, want)
	}
	var onMsg string
	for _, e := range h.rec.ev {
		if e.kind == "fan_transition" && strings.Contains(e.msg, "fan=on") {
			onMsg = e.msg
			if e.actor != "auto-control" || e.source != "fan" {
				t.Errorf("event attribution = %+v", e)
			}
		}
	}
	if !strings.Contains(onMsg, "temp=30.5C") || !strings.Contains(onMsg, "on_above=30.0") {
		t.Fatalf("on event must carry the trigger value, got %q", onMsg)
	}
}

func TestEngineInBandWithUnknownStateLeavesHardwareAlone(t *testing.T) {
	h := newHarness()
	h.feed(28.5)
	h.feed(29.0)
	if h.sw.count() != 0 {
		t.Fatalf("hardware touched %d times inside the band at startup", h.sw.count())
	}
	if h.e.Snapshot().FanOn != nil {
		t.Fatal("fan state must stay unknown")
	}
}

func TestEngineRepeatedReadingsDoNotRewrite(t *testing.T) {
	h := newHarness()
	h.feed(31)
	h.feed(32)
	h.feed(31.5)
	if h.sw.count() != 1 {
		t.Fatalf("writes = %d, want 1 (state already confirmed on)", h.sw.count())
	}
}

func TestEngineStaleReadingSuspendsAndDoesNotAct(t *testing.T) {
	h := newHarness()
	old := Reading{TempC: 35, At: h.now.Add(-time.Minute)}
	h.e.step(context.Background(), old)
	h.e.step(context.Background(), old)
	if h.sw.count() != 0 {
		t.Fatal("acted on a stale reading")
	}
	if got := h.rec.count("control_suspended"); got != 1 {
		t.Fatalf("suspended events = %d, want exactly 1", got)
	}
	if !h.e.Snapshot().Suspended {
		t.Fatal("snapshot should report suspended")
	}
	h.feed(35) // fresh reading resumes
	if h.rec.count("control_resumed") != 1 || h.sw.count() != 1 {
		t.Fatalf("resume failed: events=%v calls=%v", h.rec.kinds(), h.sw.calls)
	}
}

func TestEngineImplausibleReadingNeverDrivesTheFan(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), 6553.5, -100} {
		h := newHarness()
		h.feed(bad)
		if h.sw.count() != 0 || h.rec.count("control_suspended") != 1 {
			t.Errorf("%v: calls=%v events=%v", bad, h.sw.calls, h.rec.kinds())
		}
	}
}

func TestEngineDeviceOfflineHoldsFan(t *testing.T) {
	h := newHarness()
	h.feed(32) // fan on
	h.e.OnStatus(context.Background(), false)
	h.e.OnStatus(context.Background(), false)
	if h.rec.count("control_suspended") != 1 {
		t.Fatalf("events = %v", h.rec.kinds())
	}
	if h.sw.count() != 1 {
		t.Fatalf("offline must not cause writes: %v", h.sw.calls)
	}
	if on := h.e.Snapshot().FanOn; on == nil || !*on {
		t.Fatal("fan state should be held as on")
	}
}

func TestEngineFailedWriteIsNotAssumedAndIsRetried(t *testing.T) {
	h := newHarness()
	h.sw.setErr(errors.New("device down"))
	h.feed(32)
	h.feed(32)
	h.feed(32)
	if h.e.Snapshot().FanOn != nil {
		t.Fatal("state must not flip when the device did not confirm")
	}
	if h.sw.count() != 3 {
		t.Fatalf("expected a retry on every reading, calls=%d", h.sw.count())
	}
	if h.rec.count("fan_actuation_failed") != 1 || h.rec.count("fan_sync") != 0 {
		t.Fatalf("one failure event per outage expected, got %v", h.rec.kinds())
	}
	h.sw.setErr(nil)
	h.feed(32)
	if on := h.e.Snapshot().FanOn; on == nil || !*on {
		t.Fatal("fan should be confirmed on after recovery")
	}
	if h.rec.count("fan_sync") != 1 {
		t.Fatalf("events = %v", h.rec.kinds())
	}
}

func TestEngineDisabledDoesNothing(t *testing.T) {
	h := newHarness()
	cfg := DefaultConfig()
	cfg.Enabled = false
	if err := h.e.UpdateConfig(context.Background(), cfg, "op"); err != nil {
		t.Fatal(err)
	}
	h.feed(40)
	h.e.OnStatus(context.Background(), false)
	if h.sw.count() != 0 || h.rec.count("control_suspended") != 0 {
		t.Fatalf("disabled engine acted: calls=%v events=%v", h.sw.calls, h.rec.kinds())
	}
}

func TestUpdateConfigAppliesWithoutRestartPersistsAndAudits(t *testing.T) {
	h := newHarness()
	h.feed(29) // in band, unknown: nothing
	if h.sw.count() != 0 {
		t.Fatal("precondition")
	}
	if err := h.e.UpdateConfig(context.Background(), Config{Enabled: true, OnAbove: 28, OffBelow: 25, FanChannel: 0}, "alice"); err != nil {
		t.Fatal(err)
	}
	h.feed(29) // now above the new on_above
	if h.sw.count() != 1 || !h.sw.calls[0] {
		t.Fatalf("new threshold not applied: %v", h.sw.calls)
	}
	var cc *rec
	for i := range h.rec.ev {
		if h.rec.ev[i].kind == "config_change" {
			cc = &h.rec.ev[i]
		}
	}
	if cc == nil || cc.actor != "alice" || !strings.Contains(cc.msg, "on_above=28.0") || !strings.Contains(cc.msg, "was") {
		t.Fatalf("config_change event = %+v", cc)
	}

	// A fresh engine on the same store restores the saved config.
	h2 := newHarness()
	h2.db = h.db
	h2.e.db = h.db
	if err := h2.e.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h2.e.Snapshot().Config; got.OnAbove != 28 || got.OffBelow != 25 {
		t.Fatalf("loaded config = %+v", got)
	}
}

func TestUpdateConfigRejectsInvalidAndNeverSaves(t *testing.T) {
	h := newHarness()
	err := h.e.UpdateConfig(context.Background(), Config{Enabled: true, OnAbove: 20, OffBelow: 25}, "alice")
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v", err)
	}
	if h.db.putCall != 0 || h.rec.count("config_change") != 0 {
		t.Fatal("invalid config must not be persisted or audited")
	}
	if h.e.Snapshot().Config != DefaultConfig() {
		t.Fatal("config must be unchanged")
	}
}

func TestUpdateConfigNotAppliedWhenSaveFails(t *testing.T) {
	h := newHarness()
	h.db.putErr = errors.New("disk full")
	if err := h.e.UpdateConfig(context.Background(), Config{Enabled: true, OnAbove: 40, OffBelow: 20}, "a"); err == nil {
		t.Fatal("expected error")
	}
	if h.e.Snapshot().Config != DefaultConfig() {
		t.Fatal("config applied although it could not be saved")
	}
}

func TestChangingFanChannelForgetsFanState(t *testing.T) {
	h := newHarness()
	h.feed(32)
	if h.e.Snapshot().FanOn == nil {
		t.Fatal("precondition")
	}
	if err := h.e.UpdateConfig(context.Background(), Config{Enabled: true, OnAbove: 30, OffBelow: 27, FanChannel: 3}, "a"); err != nil {
		t.Fatal(err)
	}
	if h.e.Snapshot().FanOn != nil {
		t.Fatal("state of the old channel must not carry over")
	}
	h.feed(32)
	if last := h.sw.chans[len(h.sw.chans)-1]; last != 3 {
		t.Fatalf("wrote channel %d, want 3", last)
	}
}

func TestLoadRejectsInvalidSavedConfig(t *testing.T) {
	h := newHarness()
	h.db.vals = map[string]string{configKey: `{"enabled":true,"on_above":10,"off_below":20,"fan_channel":0}`}
	if err := h.e.Load(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunConsumesReadingsAndStopsOnCancel(t *testing.T) {
	h := newHarness()
	h.e.now = time.Now
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.e.Run(ctx); close(done) }()

	h.e.Observe(Reading{TempC: 33, At: time.Now()})
	deadline := time.After(2 * time.Second)
	for h.sw.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("engine never acted on the reading")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}

func TestObserveNeverBlocksAndKeepsNewest(t *testing.T) {
	h := newHarness()
	for i := 0; i < 100; i++ { // no consumer running: must not block
		h.e.Observe(Reading{TempC: float64(i), At: h.now})
	}
	select {
	case r := <-h.e.latest:
		if r.TempC != 99 {
			t.Fatalf("kept %v, want newest (99)", r.TempC)
		}
	default:
		t.Fatal("nothing pending")
	}
}

func TestConcurrentObserveUpdateAndSnapshotAreRaceFree(t *testing.T) {
	h := newHarness()
	h.e.now = time.Now
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); h.e.Run(ctx) }()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			h.e.Observe(Reading{TempC: 20 + float64(i%20), At: time.Now()})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = h.e.UpdateConfig(ctx, Config{Enabled: true, OnAbove: 30 + float64(i%3), OffBelow: 27, FanChannel: i % 2}, "x")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			_ = h.e.Snapshot()
			h.e.OnStatus(ctx, i%2 == 0)
		}
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()
}
