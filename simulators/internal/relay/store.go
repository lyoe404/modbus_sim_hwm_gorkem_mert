package relay

import (
	"sync"
	"time"

	"sim/internal/fault"
)

type Store struct {
	mu         sync.Mutex
	coils      [CoilCount]bool
	faultMode  fault.Mode
	faultDelay time.Duration
}

func NewStore() *Store {
	return &Store{
		faultMode:  fault.None,
		faultDelay: fault.DefaultDelay,
	}
}

func (s *Store) Coils(addr, qty uint16) ([]bool, bool) {
	if qty == 0 || int(addr)+int(qty) > CoilCount {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]bool(nil), s.coils[addr:addr+qty]...), true
}

func (s *Store) WriteCoils(addr uint16, vals []bool) bool {
	if len(vals) == 0 || int(addr)+len(vals) > CoilCount {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copy(s.coils[addr:], vals)
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
	Device     string          `json:"device"`
	Coils      [CoilCount]bool `json:"coils"`
	Fault      fault.Mode      `json:"fault"`
	FaultDelay string          `json:"fault_delay"`
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Device:     "relay-card",
		Coils:      s.coils,
		Fault:      s.faultMode,
		FaultDelay: s.faultDelay.String(),
	}
}
