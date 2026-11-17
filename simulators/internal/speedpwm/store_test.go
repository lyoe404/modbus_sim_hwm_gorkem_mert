package speedpwm

import "testing"

func TestAtomicWriteAndFeedback(t *testing.T) {
	s := NewStore()
	if addr, _ := s.WriteHolding(0, []uint16{40, 1000}); addr {
		t.Fatal("accepted read-only register")
	}
	if s.Snapshot().TargetPWM != 30 {
		t.Fatal("partial write")
	}
	if addr, val := s.WriteHolding(0, []uint16{101}); !addr || val {
		t.Fatal("accepted invalid pwm")
	}
	if addr, val := s.WriteHolding(0, []uint16{60}); !addr || !val {
		t.Fatal("valid write failed")
	}
	s.Step()
	if s.Snapshot().MeasuredRPM != 1050 {
		t.Fatal("unexpected ramp")
	}
	rpm := uint16(700)
	if !s.SetFeedbackOverride(&rpm) {
		t.Fatal("override")
	}
	s.Step()
	if s.Snapshot().MeasuredRPM != 700 {
		t.Fatal("feedback override failed")
	}
	if s.SetFeedbackOverride(func() *uint16 { v := uint16(3001); return &v }()) {
		t.Fatal("invalid override")
	}
	s.SetFeedbackOverride(nil)
	s.Step()
	if s.Snapshot().MeasuredRPM != 850 {
		t.Fatal("convergence did not resume")
	}
}
func TestInvalidHolding(t *testing.T) {
	s := NewStore()
	for _, v := range [][2]uint16{{0, 0}, {1, 2}, {2, 1}} {
		if _, ok := s.HoldingRegisters(v[0], v[1]); ok {
			t.Fatalf("accepted %v", v)
		}
	}
}
