package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"backend/internal/auth"
	"backend/internal/control"
	"backend/internal/poller"
	"backend/internal/relay"
	"backend/internal/store"
)

const testSecret = "0123456789abcdef0123456789abcdef"

type fakeRelays struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeRelays) Channels(context.Context) ([]relay.Channel, error) {
	return []relay.Channel{{ID: 0, Label: "Fan 1"}}, nil
}
func (f *fakeRelays) Set(_ context.Context, id int, on bool) (relay.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return relay.Result{Channel: id, Label: "Fan 1", Requested: on, Actual: on && f.err == nil}, f.err
}
func (f *fakeRelays) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type fakeStore struct {
	mu     sync.Mutex
	events []store.Event
}

func (f *fakeStore) Measurements(context.Context, string, string, time.Time, int) ([]poller.Measurement, error) {
	return nil, nil
}
func (f *fakeStore) Events(context.Context, int) ([]store.Event, error) { return f.events, nil }
func (f *fakeStore) AddEvent(_ context.Context, e store.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

type fakeControl struct {
	mu    sync.Mutex
	cfg   control.Config
	by    string
	calls int
}

func (f *fakeControl) Snapshot() control.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return control.Snapshot{Config: f.cfg}
}
func (f *fakeControl) UpdateConfig(_ context.Context, cfg control.Config, by string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := cfg.Validate(8); err != nil {
		return err
	}
	f.cfg, f.by = cfg, by
	return nil
}
func (f *fakeControl) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type env struct {
	h       http.Handler
	relays  *fakeRelays
	store   *fakeStore
	control *fakeControl
	tokens  *auth.Tokens
}

func newEnv(t *testing.T) *env {
	t.Helper()
	tokens, err := auth.NewTokens(testSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	users, _ := auth.NewUsers()
	if err := users.Add("viewer", "viewer-pass-1", auth.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := users.Add("operator", "operator-pass-1", auth.RoleOperator); err != nil {
		t.Fatal(err)
	}
	e := &env{relays: &fakeRelays{}, store: &fakeStore{}, control: &fakeControl{cfg: control.DefaultConfig()}, tokens: tokens}
	e.h = (&Server{
		Tokens: tokens, Users: users, Store: e.store, Relays: e.relays, Control: e.control,
		Statuses: func() []poller.DeviceStatus {
			return []poller.DeviceStatus{{Name: "sensor", State: poller.StateOnline}}
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handler()
	return e
}

func (e *env) do(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) login(t *testing.T, user, pass string) string {
	t.Helper()
	rec := e.do("POST", "/api/auth/login", "", `{"username":"`+user+`","password":"`+pass+`"}`)
	if rec.Code != 200 {
		t.Fatalf("login %s: %d %s", user, rec.Code, rec.Body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("bad login body: %s", rec.Body)
	}
	return out.Token
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("POST", "/api/auth/login", "", `{"username":"viewer","password":"nope-nope"}`); rec.Code != 401 {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestUnauthenticatedAndExpired(t *testing.T) {
	e := newEnv(t)
	if rec := e.do("GET", "/api/relays", "", ""); rec.Code != 401 {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/relays", "garbage", ""); rec.Code != 401 {
		t.Fatalf("garbage token: %d", rec.Code)
	}
	expired, _ := auth.NewTokens(testSecret, -time.Minute)
	perms, _ := auth.PermsForRole(auth.RoleOperator)
	tok, _, _ := expired.Issue("operator", auth.RoleOperator, perms)
	rec := e.do("GET", "/api/relays", tok, "")
	if rec.Code != 401 || !bytes.Contains(rec.Body.Bytes(), []byte("token_expired")) {
		t.Fatalf("expired: %d %s", rec.Code, rec.Body)
	}
}

func TestViewerCanReadButNotControl(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t, "viewer", "viewer-pass-1")

	for _, p := range []string{"/api/relays", "/api/devices", "/api/measurements", "/api/events", "/api/auth/me", "/api/config/control"} {
		if rec := e.do("GET", p, tok, ""); rec.Code != 200 {
			t.Fatalf("viewer GET %s = %d", p, rec.Code)
		}
	}
	rec := e.do("POST", "/api/relays/0", tok, `{"on":true}`)
	if rec.Code != 403 {
		t.Fatalf("viewer POST relay = %d, want 403", rec.Code)
	}
	if e.relays.callCount() != 0 {
		t.Fatal("device must not be touched on a 403")
	}
}

func TestOperatorControlsRelayAndIsAudited(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t, "operator", "operator-pass-1")

	rec := e.do("POST", "/api/relays/0", tok, `{"on":true}`)
	if rec.Code != 200 {
		t.Fatalf("operator POST = %d %s", rec.Code, rec.Body)
	}
	if len(e.store.events) != 1 || e.store.events[0].Actor != "operator" || e.store.events[0].Kind != "relay_write" {
		t.Fatalf("audit = %+v", e.store.events)
	}
}

func TestSingleCapabilityIsEnough(t *testing.T) {
	// A token with ONLY write:relay may switch relays but nothing else:
	// routes check capabilities, not roles.
	e := newEnv(t)
	tok, _, _ := e.tokens.Issue("custom", "custom", []string{auth.WriteRelay})
	if rec := e.do("POST", "/api/relays/0", tok, `{"on":true}`); rec.Code != 200 {
		t.Fatalf("relay write = %d", rec.Code)
	}
	if rec := e.do("GET", "/api/events", tok, ""); rec.Code != 403 {
		t.Fatalf("events without read:events = %d, want 403", rec.Code)
	}
}

func TestRelayErrorsMapToDistinctStatuses(t *testing.T) {
	cases := []struct {
		err  error
		code int
		name string
	}{
		{relay.ErrInvalidChannel, 404, "invalid_channel"},
		{relay.ErrWriteFailed, 502, "write_failed"},
		{relay.ErrReadBackFailed, 502, "readback_failed"},
		{relay.ErrReadBackMismatch, 409, "readback_mismatch"},
		{errors.New("other"), 502, "device_error"},
	}
	for _, c := range cases {
		e := newEnv(t)
		e.relays.err = c.err
		tok := e.login(t, "operator", "operator-pass-1")
		rec := e.do("POST", "/api/relays/0", tok, `{"on":true}`)
		if rec.Code != c.code || !bytes.Contains(rec.Body.Bytes(), []byte(c.name)) {
			t.Errorf("%v: got %d %s", c.err, rec.Code, rec.Body)
		}
	}
}

func TestBadRelayRequests(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t, "operator", "operator-pass-1")
	if rec := e.do("POST", "/api/relays/0", tok, `{}`); rec.Code != 400 {
		t.Fatalf("missing 'on' = %d", rec.Code)
	}
	if rec := e.do("POST", "/api/relays/abc", tok, `{"on":true}`); rec.Code != 404 {
		t.Fatalf("non-numeric id = %d", rec.Code)
	}
	if rec := e.do("GET", "/api/relays/0", tok, ""); rec.Code != 405 {
		t.Fatalf("GET on write route = %d", rec.Code)
	}
}

func TestControlConfigNeedsItsOwnCapabilities(t *testing.T) {
	e := newEnv(t)
	body := `{"on_above":31,"off_below":26}`

	viewer := e.login(t, "viewer", "viewer-pass-1")
	if rec := e.do("PUT", "/api/config/control", viewer, body); rec.Code != 403 {
		t.Fatalf("viewer PUT = %d, want 403", rec.Code)
	}
	if e.control.callCount() != 0 {
		t.Fatal("config must not be touched on a 403")
	}

	// write:thresholds alone (no relay, no read caps) may edit but not read.
	onlyThresholds, _, _ := e.tokens.Issue("shift", "custom", []string{auth.WriteThresholds})
	if rec := e.do("PUT", "/api/config/control", onlyThresholds, body); rec.Code != 200 {
		t.Fatalf("write:thresholds PUT = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/api/config/control", onlyThresholds, ""); rec.Code != 403 {
		t.Fatalf("GET without read:events = %d, want 403", rec.Code)
	}
	if rec := e.do("POST", "/api/relays/0", onlyThresholds, `{"on":true}`); rec.Code != 403 {
		t.Fatalf("relay write with only write:thresholds = %d, want 403", rec.Code)
	}
	if e.control.by != "shift" {
		t.Fatalf("actor = %q, want username from the JWT", e.control.by)
	}

	// The reverse: every write capability except write:thresholds is still refused.
	noThresholds, _, _ := e.tokens.Issue("ops", "custom", []string{auth.WriteRelay, auth.WriteFanSpeed, auth.WriteReset, auth.ReadEvents})
	if rec := e.do("PUT", "/api/config/control", noThresholds, body); rec.Code != 403 {
		t.Fatalf("PUT without write:thresholds = %d, want 403", rec.Code)
	}
}

func TestControlConfigUpdateValidatesAndMergesFields(t *testing.T) {
	e := newEnv(t)
	op := e.login(t, "operator", "operator-pass-1")

	// partial update keeps the other fields
	if rec := e.do("PUT", "/api/config/control", op, `{"on_above":33}`); rec.Code != 200 {
		t.Fatalf("partial PUT = %d %s", rec.Code, rec.Body)
	}
	got := e.control.Snapshot().Config
	if got.OnAbove != 33 || got.OffBelow != 27 || !got.Enabled {
		t.Fatalf("config = %+v", got)
	}

	// invalid band, unknown field, malformed body
	if rec := e.do("PUT", "/api/config/control", op, `{"on_above":20,"off_below":25}`); rec.Code != 400 || !bytes.Contains(rec.Body.Bytes(), []byte("invalid_config")) {
		t.Fatalf("inverted band = %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("PUT", "/api/config/control", op, `{"on_abov":31}`); rec.Code != 400 {
		t.Fatalf("unknown field = %d", rec.Code)
	}
	if rec := e.do("PUT", "/api/config/control", op, `nope`); rec.Code != 400 {
		t.Fatalf("malformed = %d", rec.Code)
	}
	if e.control.Snapshot().Config != got {
		t.Fatal("rejected requests must not change the config")
	}
	if rec := e.do("DELETE", "/api/config/control", op, ""); rec.Code != 405 {
		t.Fatalf("DELETE = %d", rec.Code)
	}
}

func TestMetricsEndpointExposesPrometheusFormat(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/metrics", "", "")
	if rec.Code != 200 {
		t.Fatalf("metrics code = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"hwm_device_online", "hwm_relay_channel_state", "hwm_control_threshold_celsius"} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Fatalf("missing metric %q in:\n%s", want, body)
		}
	}
}
