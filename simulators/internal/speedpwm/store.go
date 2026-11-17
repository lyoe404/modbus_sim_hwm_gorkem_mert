package speedpwm

import (
	"sync"
	"time"

	"sim/internal/fault"
)

type Store struct {
	mu          sync.Mutex
	targetPWM   uint16 // 0..100%
	measuredRPM uint16 // 0..3000 RPM
	faultMode   fault.Mode
	faultDelay  time.Duration
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
	for i, v := range vals {
		reg := addr + uint16(i)
		if reg == RegTargetPWM {
			if v > MaxPWMPercent {
				return true, false
			}
			s.targetPWM = v
		}
		// RegMeasuredRPM is read-only telemetry; writes to reg 1 are ignored or rejected
		if reg == RegMeasuredRPM {
			return false, false
		}
	}
	return true, true
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
	Device      string     `json:"device"`
	TargetPWM   uint16     `json:"target_pwm_pct"`
	MeasuredRPM uint16     `json:"measured_rpm"`
	Fault       fault.Mode `json:"fault"`
	FaultDelay  string     `json:"fault_delay"`
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Device:      "speed-control-pwm",
		TargetPWM:   s.targetPWM,
		MeasuredRPM: s.measuredRPM,
		Fault:       s.faultMode,
		FaultDelay:  s.faultDelay.String(),
	}
}
