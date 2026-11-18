// Package control is the rules engine: temperature-driven fan control with
// genuine hysteresis. The decision itself (NextFan) is a pure function; the
// Engine around it adds persistence, safety checks and audit events. It has
// no dependency on Modbus or the database, only on small interfaces.
package control

import (
	"errors"
	"fmt"
	"math"
)

// Plausible sensor range; readings outside it are never used for a decision.
const (
	MinPlausibleC = -40.0
	MaxPlausibleC = 125.0
)

// ErrInvalidConfig wraps every validation failure of a Config.
var ErrInvalidConfig = errors.New("invalid control config")

// Config is the runtime-editable control configuration.
type Config struct {
	Enabled    bool    `json:"enabled"`     // auto control on/off
	OnAbove    float64 `json:"on_above"`    // fan on when temp rises above this
	OffBelow   float64 `json:"off_below"`   // fan off when temp falls below this
	FanChannel int     `json:"fan_channel"` // relay channel that drives the fan
}

// ConfigPatch carries only the fields supplied by a control configuration request.
type ConfigPatch struct {
	Enabled    *bool    `json:"enabled"`
	OnAbove    *float64 `json:"on_above"`
	OffBelow   *float64 `json:"off_below"`
	FanChannel *int     `json:"fan_channel"`
}

func (p ConfigPatch) Apply(c Config) Config {
	if p.Enabled != nil {
		c.Enabled = *p.Enabled
	}
	if p.OnAbove != nil {
		c.OnAbove = *p.OnAbove
	}
	if p.OffBelow != nil {
		c.OffBelow = *p.OffBelow
	}
	if p.FanChannel != nil {
		c.FanChannel = *p.FanChannel
	}
	return c
}

// DefaultConfig matches the brief's reference values (on above 30, off below 27).
func DefaultConfig() Config {
	return Config{Enabled: true, OnAbove: 30.0, OffBelow: 27.0, FanChannel: 0}
}

// Validate checks the thresholds form a real band and the channel exists.
// channels is the number of relay channels in use.
func (c Config) Validate(channels int) error {
	for name, v := range map[string]float64{"on_above": c.OnAbove, "off_below": c.OffBelow} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < MinPlausibleC || v > MaxPlausibleC {
			return fmt.Errorf("%w: %s=%v outside [%v, %v]", ErrInvalidConfig, name, v, MinPlausibleC, MaxPlausibleC)
		}
	}
	if c.OnAbove <= c.OffBelow {
		return fmt.Errorf("%w: on_above (%v) must be greater than off_below (%v); equal thresholds would oscillate",
			ErrInvalidConfig, c.OnAbove, c.OffBelow)
	}
	if c.FanChannel < 0 || c.FanChannel >= channels {
		return fmt.Errorf("%w: fan_channel %d, want 0..%d", ErrInvalidConfig, c.FanChannel, channels-1)
	}
	return nil
}

// NextFan is the hysteresis rule. State (on) persists across calls: inside the
// band [offBelow, onAbove] the previous state is held, so sweeping the
// temperature across the band yields exactly one on and one off transition.
func NextFan(temp, onAbove, offBelow float64, on bool) bool {
	if !on && temp > onAbove {
		return true
	}
	if on && temp < offBelow {
		return false
	}
	return on
}

// inBand reports whether temp is inside [offBelow, onAbove], where the
// rule holds whatever state it was in.
func inBand(temp, onAbove, offBelow float64) bool {
	return temp >= offBelow && temp <= onAbove
}
