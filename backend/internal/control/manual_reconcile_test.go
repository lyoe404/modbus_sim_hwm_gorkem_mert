package control

import (
	"testing"
)

func TestManualReadbackReconcilesFanState(t *testing.T) {
	e := New(Options{Channels: 8})
	e.ObserveManualRelay(0, true)
	snapshot := e.Snapshot()
	if snapshot.FanOn == nil || !*snapshot.FanOn {
		t.Fatalf("manual fan readback not reconciled: %+v", snapshot)
	}
	e.ObserveManualRelay(2, false)
	snapshot = e.Snapshot()
	if snapshot.FanOn == nil || !*snapshot.FanOn {
		t.Fatalf("other channel must not alter fan state: %+v", snapshot)
	}
	e.ObserveManualRelay(0, false)
	snapshot = e.Snapshot()
	if snapshot.FanOn == nil || *snapshot.FanOn {
		t.Fatalf("confirmed off not reconciled: %+v", snapshot)
	}
}
