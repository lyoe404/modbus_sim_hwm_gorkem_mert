package device

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/simonvetter/modbus"
)

func startSim(t *testing.T, port int) (*Store, *modbus.ModbusClient) {
	t.Helper()
	st := NewStore()
	url := fmt.Sprintf("tcp://127.0.0.1:%d", port)
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: url, Timeout: 30 * time.Second, MaxClients: 5}, NewHandler(st))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	c, err := modbus.NewClient(&modbus.ClientConfiguration{URL: url, Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return st, c
}

func TestSensorOverRealModbus(t *testing.T) {
	st, c := startSim(t, 25020)
	st.PinTemp(31.4)
	regs, err := c.ReadRegisters(0, 2, modbus.INPUT_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if regs[0] != 314 {
		t.Fatalf("temp reg = %d, want 314", regs[0])
	}
	if regs[1] < 300 || regs[1] > 800 {
		t.Fatalf("humidity reg out of range: %d", regs[1])
	}
}

func TestRelayWriteAndReadBack(t *testing.T) {
	_, c := startSim(t, 25021)
	if err := c.WriteCoil(2, true); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadCoils(0, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{false, false, true, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("coils = %v, want %v", got, want)
		}
	}
	if err := c.WriteCoil(8, true); err == nil {
		t.Fatal("write to coil 8 should fail (illegal data address)")
	}
}

func TestFaultModes(t *testing.T) {
	st, c := startSim(t, 25022)

	st.SetFault(FaultError, 0)
	if _, err := c.ReadRegisters(0, 2, modbus.INPUT_REGISTER); err == nil {
		t.Fatal("error mode should return a Modbus exception")
	}

	st.SetFault(FaultTimeout, 2*time.Second)
	start := time.Now()
	if _, err := c.ReadRegisters(0, 2, modbus.INPUT_REGISTER); err == nil {
		t.Fatal("timeout mode should make the client time out")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("client should give up after ~500ms, took %v", time.Since(start))
	}

	st.SetFault(FaultGarbage, 0)
	// timeout above may have dropped the connection; reopen defensively.
	_ = c.Close()
	if err := c.Open(); err != nil {
		t.Fatal(err)
	}
	regs, err := c.ReadRegisters(0, 2, modbus.INPUT_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if regs[0] != GarbageValue {
		t.Fatalf("garbage mode: got %d", regs[0])
	}

	st.SetFault(FaultNone, 0)
	regs, err = c.ReadRegisters(0, 2, modbus.INPUT_REGISTER)
	if err != nil || regs[0] == GarbageValue {
		t.Fatalf("recovery failed: %v %v", regs, err)
	}
}

// Hammer the store from many goroutines; meaningful under -race.
func TestStoreConcurrency(t *testing.T) {
	st := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				st.Step()
				st.InputRegisters(0, 2)
				st.WriteCoils(uint16(i), []bool{j%2 == 0})
				st.Coils(0, CoilCount)
				st.SetFault(FaultNone, 0)
				st.Snapshot()
				if j%100 == 0 {
					st.PinTemp(30)
					st.UnpinTemp()
				}
			}
		}(i)
	}
	wg.Wait()
}
