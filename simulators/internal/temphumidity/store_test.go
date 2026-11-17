package temphumidity

import "testing"

func TestBoundsAndPin(t *testing.T) {
	s := NewStore()
	s.PinTemp(32)
	for i := 0; i < 1000; i++ {
		s.Step()
		v := s.Snapshot()
		if v.TempC != 32 || v.HumidityPc < HumidityMin || v.HumidityPc > HumidityMax {
			t.Fatal(v)
		}
	}
	s.UnpinTemp()
	if s.Snapshot().TempPinned {
		t.Fatal("still pinned")
	}
}
func TestRegisterBounds(t *testing.T) {
	s := NewStore()
	for _, v := range [][2]uint16{{0, 0}, {1, 2}, {2, 1}} {
		if _, ok := s.InputRegisters(v[0], v[1]); ok {
			t.Fatalf("accepted %v", v)
		}
	}
}
