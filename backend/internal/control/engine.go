package control

import (
	"backend/internal/speed"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

const (
	configKey = "control"
	actor     = "auto-control"
)

// Switch drives one relay channel and returns nil only when the device
// confirmed the requested state (the relay layer's read-back).
type Switch interface {
	SetChannel(ctx context.Context, channel int, on bool) error
}

// FanReader reads the *actual* coil state, independent of the engine's
// previously requested state. A failed read cannot authorize an auto write.
type FanReader interface {
	ReadFan(ctx context.Context, channel int) (bool, error)
}

// Recorder appends an audit event.
type Recorder interface {
	Record(ctx context.Context, kind, source, message, actor string) error
}

// Persistence stores the editable configuration across restarts.
type Persistence interface {
	GetConfig(ctx context.Context, key string) (value string, found bool, err error)
	PutConfig(ctx context.Context, key, value, actor string) error
}

// SwitchFunc and RecorderFunc adapt plain functions to the interfaces.
type SwitchFunc func(ctx context.Context, channel int, on bool) error
type RecorderFunc func(ctx context.Context, kind, source, message, actor string) error

func (f SwitchFunc) SetChannel(ctx context.Context, ch int, on bool) error { return f(ctx, ch, on) }
func (f RecorderFunc) Record(ctx context.Context, kind, source, message, actor string) error {
	return f(ctx, kind, source, message, actor)
}

// Reading is one fresh temperature sample.
type Reading struct {
	TempC float64
	At    time.Time
}

// Snapshot is the engine state exposed over the API.
type Snapshot struct {
	Config    Config `json:"config"`
	FanOn     *bool  `json:"fan_on"`    // last device-confirmed fan state; null = unknown
	Suspended bool   `json:"suspended"` // true while no trustworthy reading is available
}

// Options configures an Engine.
type SpeedControl interface {
	Set(context.Context, int) (speed.State, error)
	Status(context.Context) (speed.State, error)
}
type Options struct {
	Speed     SpeedControl
	Switch    Switch
	FanReader FanReader
	Recorder  Recorder
	Store     Persistence
	Log       *slog.Logger
	Channels  int           // relay channels in use (validates fan_channel)
	MaxAge    time.Duration // readings older than this are not acted on
	Now       func() time.Time
}

// Engine consumes temperature readings and drives the fan relay.
//
// Safety rules:
//   - It never decides from stale data: a reading older than MaxAge, or an
//     implausible one, suspends control and the fan is left as it is.
//   - fan state only changes after the device confirmed the write; a failed
//     write is retried on the next reading, never assumed to have happened.
//   - Inside the band with unknown state, the hardware is left untouched.
type Engine struct {
	speed         SpeedControl
	speedFailing  bool
	invalidBefore time.Time
	sampleCancel  context.CancelFunc // guarded by mu
	sw            Switch
	fanReader     FanReader
	rec           Recorder
	db            Persistence
	log           *slog.Logger
	channels      int
	maxAge        time.Duration
	now           func() time.Time

	operationMu sync.Mutex // serialises mode switches against automatic actuations

	mu         sync.Mutex
	cfg        Config
	gen        uint64 // bumped on config changes that invalidate fanOn
	fanOn      *bool
	suspended  bool
	actFailing bool

	latest chan Reading // capacity 1, newest wins
}

// New builds an engine with DefaultConfig; call Load to apply a saved one.
func New(o Options) *Engine {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.MaxAge <= 0 {
		o.MaxAge = 10 * time.Second
	}
	return &Engine{
		speed: o.Speed, sw: o.Switch, fanReader: o.FanReader, rec: o.Recorder, db: o.Store, log: o.Log.With("component", "control"),
		channels: o.Channels, maxAge: o.MaxAge, now: o.Now,
		cfg: DefaultConfig(), latest: make(chan Reading, 1),
	}
}

// Load applies the saved configuration, keeping the defaults if none is saved.
// A saved config that no longer validates is reported, not silently used.
func (e *Engine) Load(ctx context.Context) error {
	v, found, err := e.db.GetConfig(ctx, configKey)
	if err != nil {
		return fmt.Errorf("load control config: %w", err)
	}
	if !found {
		return nil
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(v), &cfg); err != nil {
		return fmt.Errorf("parse saved control config: %w", err)
	}
	if err := cfg.Validate(e.channels); err != nil {
		return fmt.Errorf("saved control config: %w", err)
	}
	e.mu.Lock()
	e.cfg = cfg
	e.mu.Unlock()
	return nil
}

// Snapshot returns the current configuration and state.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := Snapshot{Config: e.cfg, Suspended: e.suspended}
	if e.fanOn != nil {
		v := *e.fanOn
		s.FanOn = &v
	}
	return s
}

// UpdateConfig validates, persists and applies a new configuration; it takes
// effect on the next reading, without a restart. The change is audited with
// the acting user's name.
func (e *Engine) UpdateConfig(ctx context.Context, cfg Config, by string) error {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	return e.updateConfig(ctx, cfg, by)
}

// PatchConfig merges, validates and persists a partial edit under the same
// lock used for full replacements and device operations.
func (e *Engine) PatchConfig(ctx context.Context, patch ConfigPatch, by string) error {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	return e.updateConfig(ctx, patch.Apply(e.Snapshot().Config), by)
}

func (e *Engine) updateConfig(ctx context.Context, cfg Config, by string) error {
	if err := cfg.Validate(e.channels); err != nil {
		return err
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := e.db.PutConfig(ctx, configKey, string(b), by); err != nil { // persist first: never apply what we could not save
		return fmt.Errorf("save control config: %w", err)
	}

	e.mu.Lock()
	old := e.cfg
	e.cfg = cfg
	if cfg.Enabled != old.Enabled || cfg.FanChannel != old.FanChannel {
		// A different channel, or control re-enabled after a pause: what we
		// believed about the fan is no longer trustworthy.
		e.fanOn, e.actFailing = nil, false
		e.gen++
	}
	e.mu.Unlock()

	e.record(ctx, "config_change", "control", fmt.Sprintf(
		"enabled=%t on_above=%.1f off_below=%.1f fan_channel=%d (was enabled=%t on_above=%.1f off_below=%.1f fan_channel=%d)",
		cfg.Enabled, cfg.OnAbove, cfg.OffBelow, cfg.FanChannel,
		old.Enabled, old.OnAbove, old.OffBelow, old.FanChannel), by)
	if old.Enabled != cfg.Enabled {
		e.record(ctx, "control_mode_change", "fan", fmt.Sprintf("mode=%s", modeName(cfg.Enabled)), by)
	}
	return nil
}

func modeName(auto bool) string {
	if auto {
		return "AUTO"
	}
	return "MANUAL"
}

// Observe hands the engine a fresh reading. It never blocks: if the engine is
// busy (e.g. a slow relay write), the older pending reading is replaced.
func (e *Engine) Observe(r Reading) {
	for {
		select {
		case e.latest <- r:
			return
		default:
		}
		select {
		case <-e.latest: // drop the older one and retry
		default:
		}
	}
}

// OnStatus is fed the device connectivity state. When the device is not
// online the engine suspends: no decision is made until a fresh reading arrives.
func (e *Engine) OnStatus(ctx context.Context, online bool) {
	if !online {
		e.mu.Lock()
		e.invalidBefore = e.now()
		if e.sampleCancel != nil {
			e.sampleCancel()
		}
		e.mu.Unlock()
		e.suspend(ctx, "sensor not online")
	}
}

// ErrFanAutoMode indicates the automatic loop owns this relay channel.
var ErrFanAutoMode = errors.New("fan channel is controlled in AUTO mode")

// ManualOperation serializes a manual relay command with the automatic loop
// and mode changes. Other channels remain independently writable in AUTO.
// The supplied closure must only update engine belief after verified read-back.
func (e *Engine) ManualOperation(channel int, perform func() error) error {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.mu.Lock()
	blocked := e.cfg.Enabled && e.cfg.FanChannel == channel
	e.mu.Unlock()
	if blocked {
		return ErrFanAutoMode
	}
	return perform()
}

// ObserveManualRelay reconciles an operator's confirmed coil read-back with the
// automation's internal state. The next fresh sensor sample will re-evaluate
// the hysteresis policy; a failed/unconfirmed write must never call this.
func (e *Engine) ObserveManualRelay(channel int, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if channel != e.cfg.FanChannel {
		return
	}
	v := on
	e.fanOn = &v
	e.actFailing = false
	e.gen++ // reject any concurrent automatic operation's stale belief
}

// Run processes readings until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-e.latest:
			e.step(ctx, r)
		}
	}
}

func (e *Engine) suspend(ctx context.Context, reason string) {
	e.mu.Lock()
	if !e.cfg.Enabled || e.suspended {
		e.mu.Unlock()
		return
	}
	e.suspended = true
	e.mu.Unlock()
	e.record(ctx, "control_suspended", "fan", reason+"; fan left unchanged", actor)
}

func (e *Engine) step(ctx context.Context, r Reading) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	sampleCtx, cancel := context.WithTimeout(ctx, e.maxAge-e.now().Sub(r.At))
	e.mu.Lock()
	cfg, gen := e.cfg, e.gen
	invalidBefore := e.invalidBefore
	e.sampleCancel = cancel
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		e.sampleCancel = nil
		e.mu.Unlock()
	}()
	if !cfg.Enabled {
		return
	}

	switch {
	case math.IsNaN(r.TempC) || math.IsInf(r.TempC, 0) || r.TempC < MinPlausibleC || r.TempC > MaxPlausibleC:
		e.suspend(ctx, fmt.Sprintf("implausible reading %v", r.TempC))
		return
	case !r.At.After(invalidBefore):
		e.suspend(ctx, "reading predates sensor invalidation")
		return
	case !e.sampleCurrent(sampleCtx, r.At):
		e.suspend(ctx, fmt.Sprintf("reading unavailable or stale (%s old)", e.now().Sub(r.At).Round(time.Millisecond)))
		return
	}

	// Re-read physical feedback before deciding. An external Modbus client or
	// simulator restart may have changed the coil without going through our API.
	// Inside the hysteresis band we preserve the *device's* state, not a guess.
	if e.fanReader != nil {
		actual, err := e.fanReader.ReadFan(sampleCtx, cfg.FanChannel)
		if err != nil {
			e.suspend(ctx, fmt.Sprintf("fan feedback unavailable: %v", err))
			return
		}
		e.mu.Lock()
		if e.gen != gen || !e.cfg.Enabled {
			e.mu.Unlock()
			return
		}
		if e.fanOn == nil || *e.fanOn != actual {
			e.log.Info("fan coil reconciled from read-back", "channel", cfg.FanChannel, "actual", actual)
		}
		v := actual
		e.fanOn = &v
		e.mu.Unlock()
	}

	e.mu.Lock()
	if !e.sampleCurrent(sampleCtx, r.At) {
		e.mu.Unlock()
		e.suspend(ctx, "reading expired or invalidated during fan feedback")
		return
	}
	wasSuspended := e.suspended
	e.suspended = false
	cur := e.fanOn
	e.mu.Unlock()
	if wasSuspended {
		e.record(ctx, "control_resumed", "fan", fmt.Sprintf("fresh reading temp=%.1fC", r.TempC), actor)
	}

	// Unknown fan state and inside the band: nothing to enforce yet.
	if cur == nil && inBand(r.TempC, cfg.OnAbove, cfg.OffBelow) {
		return
	}
	prev := cur != nil && *cur
	want := NextFan(r.TempC, cfg.OnAbove, cfg.OffBelow, prev)
	if cur != nil && *cur == want {
		e.applySpeed(ctx, sampleCtx, r, want)
		return
	}

	if !e.sampleCurrent(sampleCtx, r.At) {
		e.suspend(ctx, "reading expired or invalidated before relay write")
		return
	}
	if err := e.sw.SetChannel(sampleCtx, cfg.FanChannel, want); err != nil {
		e.log.Warn("fan write failed", "want", want, "err", err)
		e.mu.Lock()
		first := !e.actFailing
		e.actFailing = true
		e.mu.Unlock()
		if first { // one event per outage, not one per poll
			e.record(ctx, "fan_actuation_failed", "fan",
				fmt.Sprintf("wanted %s at temp=%.1fC channel=%d: %v", onOff(want), r.TempC, cfg.FanChannel, err), actor)
		}
		return
	}

	e.mu.Lock()
	if e.gen != gen { // config changed while writing; do not record stale belief
		e.mu.Unlock()
		return
	}
	e.fanOn = &want
	e.actFailing = false
	e.mu.Unlock()

	e.applySpeed(ctx, sampleCtx, r, want)
	kind := "fan_transition"
	if cur == nil {
		kind = "fan_sync"
	}
	e.record(ctx, kind, "fan", fmt.Sprintf("fan=%s temp=%.1fC on_above=%.1f off_below=%.1f channel=%d",
		onOff(want), r.TempC, cfg.OnAbove, cfg.OffBelow, cfg.FanChannel), actor)
}

func (e *Engine) record(ctx context.Context, kind, source, msg, by string) {
	if err := e.rec.Record(ctx, kind, source, msg, by); err != nil && !errors.Is(err, context.Canceled) {
		e.log.Error("record event failed", "kind", kind, "err", err)
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// SpeedTarget is the reference linear curve, clamped to the physical range.
func SpeedTarget(temp float64) int {
	return int(math.Round(math.Max(0, math.Min(100, 20+(temp-25)*8))))
}
func (e *Engine) sampleCurrent(ctx context.Context, at time.Time) bool {
	age := e.now().Sub(at)
	return ctx.Err() == nil && !at.IsZero() && age >= 0 && age <= e.maxAge
}

func (e *Engine) applySpeed(ctx, sampleCtx context.Context, reading Reading, on bool) {
	if e.speed == nil || !e.sampleCurrent(sampleCtx, reading.At) {
		return
	}
	target := 0
	if on {
		target = SpeedTarget(reading.TempC)
	}
	st, err := e.speed.Status(sampleCtx)
	if !e.sampleCurrent(sampleCtx, reading.At) {
		e.suspend(ctx, "reading expired or invalidated during PWM feedback")
		return
	}
	changed := false
	if err == nil && int(st.CommandPct) != target {
		changed = true
		_, err = e.speed.Set(sampleCtx, target)
	}
	if err != nil {
		if !e.speedFailing {
			e.record(ctx, "fan_speed_failed", "fan_speed", err.Error(), actor)
		}
		e.speedFailing = true
		return
	}
	if changed || e.speedFailing {
		e.record(ctx, "fan_speed_transition", "fan_speed", fmt.Sprintf("percent=%d", target), actor)
	}
	e.speedFailing = false
}
func (e *Engine) ManualSpeedOperation(perform func() error) error {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.mu.Lock()
	blocked := e.cfg.Enabled
	e.mu.Unlock()
	if blocked {
		return ErrFanAutoMode
	}
	return perform()
}

// Device reset invalidates confirmed relay belief and queued readings atomically.
func (e *Engine) ResetOperation(perform func() error) error {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.mu.Lock()
	e.gen++
	e.invalidBefore = e.now()
	e.fanOn = nil
	e.suspended = true
	e.mu.Unlock()
	for {
		select {
		case <-e.latest:
		default:
			return perform()
		}
	}
}
