package api

import (
	"backend/internal/auth"
	"backend/internal/speed"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
)

type apiSpeed struct{ writes int }

func (f *apiSpeed) Status(context.Context) (speed.State, error) { return speed.State{}, nil }
func (f *apiSpeed) Set(_ context.Context, p int) (speed.State, error) {
	f.writes++
	return speed.State{CommandPct: uint16(p)}, nil
}
func TestExtendedCapabilitiesModeAndResetAudit(t *testing.T) {
	e := newEnv(t)
	pwm := &apiSpeed{}
	calls := 0
	e.h = (&Server{Tokens: e.tokens, Store: e.store, Control: e.control, Speed: pwm, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), ResetDevice: func(context.Context, string) error { calls++; return nil }}).Handler()
	viewer, _, _ := e.tokens.Issue("viewer", auth.RoleViewer, []string{auth.ReadFanSpeed})
	operator, _, _ := e.tokens.Issue("acting-user", auth.RoleOperator, []string{auth.WriteFanSpeed, auth.WriteReset})
	for _, path := range []string{"/api/reset", "/api/fan/speed"} {
		if got := e.do("POST", path, viewer, `{"target":"relay","percent":50}`).Code; got != 403 {
			t.Fatal(path, got)
		}
	}
	if got := e.do("POST", "/api/fan/speed", operator, `{"percent":50}`).Code; got != 409 {
		t.Fatal(got)
	}
	if pwm.writes != 0 {
		t.Fatal("AUTO write")
	}
	e.control.allowFanManual()
	if got := e.do("POST", "/api/fan/speed", operator, `{"percent":50}`).Code; got != 200 {
		t.Fatal(got)
	}
	for _, target := range []string{"relay", "thermocouple", "speed-pwm"} {
		body, _ := json.Marshal(map[string]string{"target": target})
		if got := e.do("POST", "/api/reset", operator, string(body)).Code; got != 202 {
			t.Fatal(got)
		}
	}
	if got := e.do("POST", "/api/reset", operator, `{"target":"other"}`).Code; got != 400 {
		t.Fatal(got)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	for _, ev := range e.store.events {
		if ev.Actor != "acting-user" {
			t.Fatal(ev)
		}
	}
}
