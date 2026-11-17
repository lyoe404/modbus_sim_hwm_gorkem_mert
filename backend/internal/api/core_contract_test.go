package api

import (
	"backend/internal/auth"
	"encoding/json"
	"testing"
)

// A verified coil read-back must be exposed using the actual channel schema
// that the React UI consumes, not the internal relay.Result DTO.
func TestRelayWriteReturnsConfirmedChannel(t *testing.T) {
	e := newEnv(t)
	e.control.allowFanManual()
	token := e.login(t, "operator", "operator-pass-1")
	rec := e.do("POST", "/api/relays/0", token, `{"on":true}`)
	if rec.Code != 200 {
		t.Fatalf("write code %d: %s", rec.Code, rec.Body)
	}
	var channel struct {
		ID    int    `json:"id"`
		Label string `json:"label"`
		On    bool   `json:"on"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &channel); err != nil {
		t.Fatal(err)
	}
	if !channel.On || channel.ID != 0 || channel.Label != "Fan 1" {
		t.Fatalf("unexpected readback: %+v", channel)
	}
}

func TestThermocoupleHistoryNeedsThermocoupleCapability(t *testing.T) {
	e := newEnv(t)
	onlySensor, _, err := e.tokens.Issue("sensor-only", "custom", []string{auth.ReadSensor})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.do("GET", "/api/measurements?source=thermocouple_ch0", onlySensor, "").Code; got != 403 {
		t.Fatalf("thermocouple without permission: %d", got)
	}
	if got := e.do("GET", "/api/measurements?source=sensor", onlySensor, "").Code; got != 200 {
		t.Fatalf("sensor with permission: %d", got)
	}
	thermo, _, err := e.tokens.Issue("thermo", "custom", []string{auth.ReadThermocouple})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.do("GET", "/api/measurements?source=thermocouple_ch0", thermo, "").Code; got != 200 {
		t.Fatalf("thermocouple with permission: %d", got)
	}
}

func TestAutoFanRelayIsLockedWhileOtherCoilsRemainWritable(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t, "operator", "operator-pass-1")
	rec := e.do("POST", "/api/relays/0", tok, `{"on":true}`)
	if rec.Code != 409 {
		t.Fatalf("auto-mode write: %d %s", rec.Code, rec.Body)
	}
	if e.relays.callCount() != 0 {
		t.Fatal("automatic fan relay must not be touched")
	}
	rec = e.do("POST", "/api/relays/3", tok, `{"on":true}`)
	if rec.Code != 200 {
		t.Fatalf("auxiliary coil: %d %s", rec.Code, rec.Body)
	}
	e.control.allowFanManual()
	rec = e.do("POST", "/api/relays/0", tok, `{"on":true}`)
	if rec.Code != 200 {
		t.Fatalf("manual-mode fan write: %d %s", rec.Code, rec.Body)
	}
}
