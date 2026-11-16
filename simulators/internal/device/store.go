package device

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// Store is the mutex-guarded device state. Every reader and writer goes
// through its methods; nothing else touches these fields.
type Store struct {
	mu       sync.Mutex
	rng      *rand.Rand
	temp     float64
	humidity float64
	pinned   bool 
	coils    [CoilCount]bool

	faultMode  FaultMode
	faultDelay time.Duration
}

// NewStore returns a store seeded with plausible values.
func NewStore() *Store {
	return &Store{
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
		temp:       25,
		humidity:   50,
		faultMode:  FaultNone,
		faultDelay: DefaultFaultDelay,
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Step advances the bounded random walk one tick.
func (s *Store) Step() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pinned {
		s.temp = clamp(s.temp+(s.rng.Float64()-0.5)*0.6, TempMin, TempMax)
	}
	s.humidity = clamp(s.humidity+(s.rng.Float64()-0.5), HumidityMin, HumidityMax)
}

// PinTemp forces the temperature (for driving the hysteresis band in tests).
func (s *Store) PinTemp(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.temp = clamp(v, -40, 100)
	s.pinned = true
}

// UnpinTemp resumes the random walk from the current value.
func (s *Store) UnpinTemp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned = false
	s.temp = clamp(s.temp, TempMin, TempMax)
}

// InputRegisters returns qty registers starting at addr, or ok=false if out of range.
func (s *Store) InputRegisters(addr, qty uint16) ([]uint16, bool) {
	if qty == 0 || int(addr)+int(qty) > InputRegs {
		return nil, false
	}
	s.mu.Lock()
	all := [InputRegs]uint16{
		AddrTemp:     toReg(s.temp),
		AddrHumidity: toReg(s.humidity),
	}
	s.mu.Unlock()
	return append([]uint16(nil), all[addr:addr+qty]...), true
}

// toReg converts a value to its x10 fixed-point register form.
func toReg(v float64) uint16 {
	r := math.Round(v * 10)
	if r < 0 {
		return 0
	}
	if r > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(r)
}

// Coils returns qty coil states starting at addr, or ok=false if out of range.
func (s *Store) Coils(addr, qty uint16) ([]bool, bool) {
	if qty == 0 || int(addr)+int(qty) > CoilCount {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]bool(nil), s.coils[addr:addr+qty]...), true
}

// WriteCoils sets coils starting at addr; ok=false if out of range.
func (s *Store) WriteCoils(addr uint16, vals []bool) bool {
	if len(vals) == 0 || int(addr)+len(vals) > CoilCount {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copy(s.coils[addr:], vals)
	return true
}

// SetFault changes the active fault mode and delay (delay<=0 keeps the default).
func (s *Store) SetFault(m FaultMode, delay time.Duration) {
	if delay <= 0 {
		delay = DefaultFaultDelay
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faultMode, s.faultDelay = m, delay
}

// Fault returns the active fault mode and delay.
func (s *Store) Fault() (FaultMode, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.faultMode, s.faultDelay
}

// Snapshot is a read-only view for the admin endpoint.
type Snapshot struct {
	TempC      float64         `json:"temp_c"`
	HumidityPc float64         `json:"humidity_pct"`
	TempPinned bool            `json:"temp_pinned"`
	Coils      [CoilCount]bool `json:"coils"`
	Fault      FaultMode       `json:"fault"`
	FaultDelay string          `json:"fault_delay"`
}

// Snapshot returns a consistent copy of the state.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		TempC:      math.Round(s.temp*10) / 10,
		HumidityPc: math.Round(s.humidity*10) / 10,
		TempPinned: s.pinned,
		Coils:      s.coils,
		Fault:      s.faultMode,
		FaultDelay: s.faultDelay.String(),
	}
}
