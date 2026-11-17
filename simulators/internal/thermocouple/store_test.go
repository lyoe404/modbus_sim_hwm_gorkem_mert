package thermocouple

import "testing"

func TestIndependentPinAndFault(t *testing.T) {
	s := NewStore()
	if !s.PinChannel(0, 200) {
		t.Fatal("pin")
	}
	before := s.Snapshot()
	for i := 0; i < 30; i++ {
		s.Step()
	}
	after := s.Snapshot()
	if after.ChannelsC[0] != before.ChannelsC[0] {
		t.Fatal("pinned channel moved")
	}
	if after.ChannelsC[1] == before.ChannelsC[1] && after.ChannelsC[2] == before.ChannelsC[2] {
		t.Fatal("other channels frozen")
	}
	if !s.SetChannelFault(2, true) {
		t.Fatal("fault")
	}
	regs, ok := s.InputRegisters(0, ChannelCount)
	if !ok || regs[2] != ChannelFaultSentinel || regs[0] == ChannelFaultSentinel {
		t.Fatal("fault not isolated")
	}
	s.SetChannelFault(2, false)
	regs, _ = s.InputRegisters(2, 1)
	if regs[0] == ChannelFaultSentinel {
		t.Fatal("channel did not recover")
	}
	if !s.UnpinChannel(0) {
		t.Fatal("unpin")
	}
	if s.Snapshot().Pinned[0] {
		t.Fatal("still pinned")
	}
	if s.UnpinChannel(ChannelCount) {
		t.Fatal("accepted invalid channel")
	}
}
func TestInvalidRegisters(t *testing.T) {
	s := NewStore()
	for _, v := range [][2]uint16{{0, 0}, {3, 2}, {4, 1}} {
		if _, ok := s.InputRegisters(v[0], v[1]); ok {
			t.Fatalf("accepted %v", v)
		}
	}
}
