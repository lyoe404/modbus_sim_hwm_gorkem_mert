package thermocouple

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"sim/internal/fault"
)

type Store struct {
	mu           sync.Mutex
	rng          *rand.Rand
	channels     [ChannelCount]float64
	channelFault [ChannelCount]bool
	pinned       [ChannelCount]bool
	faultMode    fault.Mode
	faultDelay   time.Duration
}

func NewStore() *Store {
	return &Store{
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
		channels:   [ChannelCount]float64{120.0, 145.5, 180.0, 95.0},
		faultMode:  fault.None,
		faultDelay: fault.DefaultDelay,
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func (s *Store) Step() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < ChannelCount; i++ {
		if s.pinned[i] {
			continue
		}
		s.channels[i] = clamp(s.channels[i]+(s.rng.Float64()-0.5)*2.5, TempMin, TempMax)
	}
}

func (s *Store) PinChannel(ch int, v float64) bool {
	if ch < 0 || ch >= ChannelCount {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels[ch] = clamp(v, -200, 1300)
	s.pinned[ch] = true
	return true
}

func (s *Store) UnpinChannel(ch int) bool {
	if ch < 0 || ch >= ChannelCount {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned[ch] = false
	s.channels[ch] = clamp(s.channels[ch], TempMin, TempMax)
	return true
}

func (s *Store) UnpinAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinned = [ChannelCount]bool{}
}

func (s *Store) SetChannelFault(ch int, broken bool) bool {
	if ch < 0 || ch >= ChannelCount {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelFault[ch] = broken
	return true
}

func (s *Store) InputRegisters(addr, qty uint16) ([]uint16, bool) {
	if qty == 0 || int(addr)+int(qty) > InputRegs {
		return nil, false
	}
	s.mu.Lock()
	var all [InputRegs]uint16
	for i := 0; i < ChannelCount; i++ {
		if s.channelFault[i] {
			all[i] = ChannelFaultSentinel // 0x8000 per-channel probe fault
		} else {
			all[i] = toReg(s.channels[i])
		}
	}
	s.mu.Unlock()
	return append([]uint16(nil), all[addr:addr+qty]...), true
}

func toReg(v float64) uint16 {
	r := math.Round(v * 10)
	if r < -32767 {
		return 0x8001
	}
	if r > 0x7FFF {
		return 0x7FFF
	}
	return uint16(int16(r))
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
	Device       string                `json:"device"`
	ChannelsC    [ChannelCount]float64 `json:"channels_c"`
	ChannelFault [ChannelCount]bool    `json:"channel_fault"`
	Pinned       [ChannelCount]bool    `json:"pinned"`
	Fault        fault.Mode            `json:"fault"`
	FaultDelay   string                `json:"fault_delay"`
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rounded [ChannelCount]float64
	for i := 0; i < ChannelCount; i++ {
		rounded[i] = math.Round(s.channels[i]*10) / 10
	}
	return Snapshot{
		Device:       "thermocouple-module",
		ChannelsC:    rounded,
		ChannelFault: s.channelFault,
		Pinned:       s.pinned,
		Fault:        s.faultMode,
		FaultDelay:   s.faultDelay.String(),
	}
}
