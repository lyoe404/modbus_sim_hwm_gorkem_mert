package control

import (
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
type Options struct {
	Switch   Switch
	Recorder Recorder
	Store    Persistence
	Log      *slog.Logger
	Channels int           // relay channels in use (validates fan_channel)
	MaxAge   time.Duration // readings older than this are not acted on
	Now      func() time.Time
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
	sw       Switch
	rec      Recorder
	db       Persistence
	log      *slog.Logger
	channels int
	maxAge   time.Duration
	now      func() time.Time

	updateMu sync.Mutex // serialises UpdateConfig

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
		sw: o.Switch, rec: o.Recorder, db: o.Store, log: o.Log.With("component", "control"),
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
	if err := cfg.Validate(e.channels); err != nil {
		return err
	}
	e.updateMu.Lock()
	defer e.updateMu.Unlock()

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
	return nil
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
		e.suspend(ctx, "sensor not online")
	}
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
	e.mu.Lock()
	cfg, gen := e.cfg, e.gen
	e.mu.Unlock()
	if !cfg.Enabled {
		return
	}

	switch {
	case math.IsNaN(r.TempC) || math.IsInf(r.TempC, 0) || r.TempC < MinPlausibleC || r.TempC > MaxPlausibleC:
		e.suspend(ctx, fmt.Sprintf("implausible reading %v", r.TempC))
		return
	case e.now().Sub(r.At) > e.maxAge:
		e.suspend(ctx, fmt.Sprintf("stale reading (%s old)", e.now().Sub(r.At).Round(time.Millisecond)))
		return
	}

	e.mu.Lock()
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
		return
	}

	if err := e.sw.SetChannel(ctx, cfg.FanChannel, want); err != nil {
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
