package speedpwm

import (
	"sync"
	"time"

	"sim/internal/fault"
)

type Store struct {
	mu               sync.Mutex
	targetPWM        uint16  // 0..100%
	measuredRPM      uint16  // 0..3000 RPM
	feedbackOverride *uint16 // optional fixed tachometer reading for persistent mismatch
	faultMode        fault.Mode
	faultDelay       time.Duration
}

func NewStore() *Store {
	return &Store{
		targetPWM:   30,
		measuredRPM: 900,
		faultMode:   fault.None,
		faultDelay:  fault.DefaultDelay,
	}
}

// Step smoothly converges measuredRPM toward targetPWM * 30.
func (s *Store) Step() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.feedbackOverride != nil {
		s.measuredRPM = *s.feedbackOverride
		return
	}
	desired := s.targetPWM * (MaxRPM / MaxPWMPercent)
	if s.measuredRPM < desired {
		diff := desired - s.measuredRPM
		if diff > 150 {
			s.measuredRPM += 150
		} else {
			s.measuredRPM = desired
		}
	} else if s.measuredRPM > desired {
		diff := s.measuredRPM - desired
		if diff > 150 {
			s.measuredRPM -= 150
		} else {
			s.measuredRPM = desired
		}
	}
}

func (s *Store) HoldingRegisters(addr, qty uint16) ([]uint16, bool) {
	if qty == 0 || int(addr)+int(qty) > HoldingRegs {
		return nil, false
	}
	s.mu.Lock()
	all := [HoldingRegs]uint16{
		RegTargetPWM:   s.targetPWM,
		RegMeasuredRPM: s.measuredRPM,
	}
	s.mu.Unlock()
	return append([]uint16(nil), all[addr:addr+qty]...), true
}

// WriteHolding writes target PWM percentage (0..100%). Rejects >100 with IllegalDataValue.
func (s *Store) WriteHolding(addr uint16, vals []uint16) (validAddr bool, validVal bool) {
	if len(vals) == 0 || int(addr)+len(vals) > HoldingRegs {
		return false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Validate the complete request before modifying any state.
	for i, v := range vals {
		reg := addr + uint16(i)
		if reg != RegTargetPWM {
			return false, false
		} // RPM telemetry is read-only
		if v > MaxPWMPercent {
			return true, false
		}
	}
	s.targetPWM = vals[0]
	return true, true
}

// SetFeedbackOverride pins the measured RPM independently of the PWM command.
// nil restores normal convergence on the next Step.
func (s *Store) SetFeedbackOverride(rpm *uint16) bool {
	if rpm != nil && *rpm > MaxRPM {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rpm == nil {
		s.feedbackOverride = nil
	} else {
		value := *rpm
		s.feedbackOverride = &value
		s.measuredRPM = value
	}
	return true
}

func (s *Store) SetFault(m fault.Mode, delay time.Duration) {
	if delay <= 0 {
		delay = fault.DefaultDelay
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faultMode, s.faultDelay = m, delay
}

func (s *Store) Fault() (fault.Mode, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.faultMode, s.faultDelay
}

type Snapshot struct {
	Device           string     `json:"device"`
	TargetPWM        uint16     `json:"target_pwm_pct"`
	MeasuredRPM      uint16     `json:"measured_rpm"`
	FeedbackOverride *uint16    `json:"feedback_override_rpm,omitempty"`
	Fault            fault.Mode `json:"fault"`
	FaultDelay       string     `json:"fault_delay"`
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Device:           "speed-control-pwm",
		TargetPWM:        s.targetPWM,
		MeasuredRPM:      s.measuredRPM,
		FeedbackOverride: s.feedbackOverride,
		Fault:            s.faultMode,
		FaultDelay:       s.faultDelay.String(),
	}
}
