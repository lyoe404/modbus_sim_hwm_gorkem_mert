package relay

import "testing"

func TestIndependentCoilsAndReadback(t *testing.T) {
	s := NewStore()
	if !s.WriteCoils(2, []bool{true, true}) {
		t.Fatal("write")
	}
	got, ok := s.Coils(0, CoilCount)
	if !ok || !got[2] || !got[3] || got[1] || got[4] {
		t.Fatalf("readback %v", got)
	}
	if s.WriteCoils(7, []bool{true, true}) {
		t.Fatal("accepted invalid write")
	}
	got, _ = s.Coils(0, CoilCount)
	if got[7] {
		t.Fatal("partial write")
	}
}
func TestInvalidCoils(t *testing.T) {
	s := NewStore()
	for _, v := range [][2]uint16{{0, 0}, {7, 2}, {8, 1}} {
		if _, ok := s.Coils(v[0], v[1]); ok {
			t.Fatalf("accepted %v", v)
		}
	}
}
