package temphumidity

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"sim/internal/fault"
)

type Store struct {
	mu         sync.Mutex
	rng        *rand.Rand
	temp       float64
	humidity   float64
	pinned     bool
	faultMode  fault.Mode
	faultDelay time.Duration
}

func NewStore() *Store {
	return &Store{
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
		temp:       25.0,
		humidity:   50.0,
		faultMode:  fault.None,
		faultDelay: fault.DefaultDelay,
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func (s *Store) Step() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pinned {
		s.temp = clamp(s.temp+(s.rng.Float64()-0.5)*0.6, TempMin, TempMax)
	}
	s.humidity = clamp(s.humidity+(s.rng.Float64()-0.5), HumidityMin, HumidityMax)
}

func (s *Store) PinTemp(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.temp = clamp(v, -40, 100)
	s.pinned = true
}

func (s *Store) UnpinTemp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned = false
	s.temp = clamp(s.temp, TempMin, TempMax)
}

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
	Device     string     `json:"device"`
	TempC      float64    `json:"temp_c"`
	HumidityPc float64    `json:"humidity_pct"`
	TempPinned bool       `json:"temp_pinned"`
	Fault      fault.Mode `json:"fault"`
	FaultDelay string     `json:"fault_delay"`
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Device:     "temp-humidity-sensor",
		TempC:      math.Round(s.temp*10) / 10,
		HumidityPc: math.Round(s.humidity*10) / 10,
		TempPinned: s.pinned,
		Fault:      s.faultMode,
		FaultDelay: s.faultDelay.String(),
	}
}
