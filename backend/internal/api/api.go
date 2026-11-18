// Package api is the REST layer: every route except login and healthz is
// guarded by the specific capability it needs.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backend/internal/auth"
	"backend/internal/control"
	"backend/internal/poller"
	"backend/internal/relay"
	"backend/internal/reset"
	"backend/internal/speed"
	"backend/internal/store"
)

// Store is the persistence the API needs (implemented by *store.Store).
type Store interface {
	Measurements(ctx context.Context, source, metric string, since time.Time, limit int) ([]poller.Measurement, error)
	Events(ctx context.Context, limit int) ([]store.Event, error)
	AddEvent(ctx context.Context, e store.Event) error
}

// Relays is the relay card (implemented by *relay.Controller).
type Relays interface {
	Channels(ctx context.Context) ([]relay.Channel, error)
	Set(ctx context.Context, id int, on bool) (relay.Result, error)
}

// Control is the auto-control engine (implemented by *control.Engine).
type Control interface {
	Snapshot() control.Snapshot
	PatchConfig(ctx context.Context, patch control.ConfigPatch, by string) error
}

// Server wires the dependencies of the API.
type SpeedController interface {
	Status(context.Context) (speed.State, error)
	Set(context.Context, int) (speed.State, error)
}
type Server struct {
	Tokens       *auth.Tokens
	Users        *auth.Users
	Store        Store
	Relays       Relays
	Control      Control
	Speed        SpeedController
	Thermocouple *poller.Thermocouple
	ResetDevice  func(context.Context, string) error
	Statuses     func() []poller.DeviceStatus
	Log          *slog.Logger
}

var errDeviceRecovering = errors.New("device recovering")

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// only restricts a handler to one HTTP method.
func only(method string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	guard := func(capability string, h http.HandlerFunc) http.Handler {
		return auth.Guard(s.Tokens, capability)(h)
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("/metrics", only(http.MethodGet, http.HandlerFunc(s.metrics)))
	mux.Handle("/api/auth/login", only(http.MethodPost, http.HandlerFunc(s.login)))
	mux.Handle("/api/auth/me", only(http.MethodGet, auth.Guard(s.Tokens)(http.HandlerFunc(s.me))))

	mux.Handle("/api/devices", only(http.MethodGet, guard(auth.ReadDeviceStatus, s.devices)))
	mux.Handle("/api/measurements", only(http.MethodGet, auth.Guard(s.Tokens)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var required string
		switch r.URL.Query().Get("source") {
		case "", "sensor":
			required = auth.ReadSensor
		case "thermocouple_ch0", "thermocouple_ch1", "thermocouple_ch2", "thermocouple_ch3":
			required = auth.ReadThermocouple
		case "fan_speed":
			required = auth.ReadFanSpeed
		default:
			writeErr(w, http.StatusBadRequest, "invalid_source")
			return
		}
		claims, _ := auth.ClaimsFrom(r.Context())
		for _, perm := range claims.Perms {
			if perm == required {
				s.measurements(w, r)
				return
			}
		}
		writeErr(w, http.StatusForbidden, "forbidden")
	}))))
	mux.Handle("/api/reset", only(http.MethodPost, guard(auth.WriteReset, s.resetDevice)))
	mux.Handle("/api/thermocouple", only(http.MethodGet, guard(auth.ReadThermocouple, s.getThermocouple)))
	mux.Handle("/api/events", only(http.MethodGet, guard(auth.ReadEvents, s.events)))
	mux.Handle("/api/fan/speed", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			guard(auth.ReadFanSpeed, s.getSpeed).ServeHTTP(w, r)
		case http.MethodPost:
			guard(auth.WriteFanSpeed, s.setSpeed).ServeHTTP(w, r)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
	}))
	mux.Handle("/api/relays", only(http.MethodGet, guard(auth.ReadRelay, s.listRelays)))
	mux.Handle("/api/relays/", only(http.MethodPost, guard(auth.WriteRelay, s.setRelay)))

	// Reading the control config needs read:events; changing it needs write:thresholds.
	getCtl, putCtl := guard(auth.ReadEvents, s.getControl), guard(auth.WriteThresholds, s.putControl)
	mux.HandleFunc("/api/config/control", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCtl.ServeHTTP(w, r)
		case http.MethodPut:
			putCtl.ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, PUT")
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		}
	})
	return mux
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	user, ok := s.Users.Authenticate(req.Username, req.Password)
	if !ok {
		s.Log.Warn("login failed", "username", req.Username)
		writeErr(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	perms, _ := auth.PermsForRole(user.Role)
	token, exp, err := s.Tokens.Issue(user.Username, user.Role, perms)
	if err != nil {
		s.Log.Error("issue token", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": exp})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClaimsFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"username": c.Username, "role": c.Role, "perms": c.Perms})
}

func (s *Server) devices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Statuses())
}

func intParam(r *http.Request, name string, def, max int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil && v > 0 {
		if v > max {
			return max
		}
		return v
	}
	return def
}

func (s *Server) measurements(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	source, metric := q.Get("source"), q.Get("metric")
	if source == "" {
		source = "sensor"
	}
	if metric == "" {
		metric = "temperature_c"
	}
	since := time.Now().Add(-time.Hour)
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_since_use_rfc3339")
			return
		}
		since = t
	}
	ms, err := s.Store.Measurements(r.Context(), source, metric, since, intParam(r, "limit", 500, 5000))
	if err != nil {
		s.Log.Error("query measurements", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal")
		return
	}
	if ms == nil {
		ms = []poller.Measurement{}
	}
	type point struct {
		Source string    `json:"source"`
		Metric string    `json:"metric"`
		Value  float64   `json:"value"`
		At     time.Time `json:"at"`
	}
	out := make([]point, len(ms))
	for i, m := range ms {
		out[i] = point{m.Source, m.Metric, m.Value, m.At}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	evs, err := s.Store.Events(r.Context(), intParam(r, "limit", 100, 1000))
	if err != nil {
		s.Log.Error("query events", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal")
		return
	}
	if evs == nil {
		evs = []store.Event{}
	}
	writeJSON(w, http.StatusOK, evs)
}

func (s *Server) listRelays(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	chs, err := s.readRelayChannels(ctx)
	if errors.Is(err, errDeviceRecovering) {
		writeErr(w, 503, "device_recovering")
		return
	}
	if err != nil {
		s.Log.Warn("read relays", "err", err)
		writeErr(w, http.StatusBadGateway, "device_unreachable")
		return
	}
	writeJSON(w, http.StatusOK, chs)
}

func (s *Server) readRelayChannels(ctx context.Context) ([]relay.Channel, error) {
	if !s.deviceAvailable("relay") {
		return nil, errDeviceRecovering
	}
	channels, err := s.Relays.Channels(ctx)
	if !s.deviceAvailable("relay") {
		return nil, errDeviceRecovering
	}
	return channels, err
}

func (s *Server) setRelay(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/relays/"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "invalid_channel")
		return
	}
	var req struct {
		On *bool `json:"on"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.On == nil {
		writeErr(w, http.StatusBadRequest, `body_must_be_{"on":true|false}`)
		return
	}
	claims, _ := auth.ClaimsFrom(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var res relay.Result
	var errWrite error
	write := func() error {
		if !s.deviceAvailable("relay") {
			return errDeviceRecovering
		}
		res, errWrite = s.Relays.Set(ctx, id, *req.On)
		if errWrite == nil {
			if observer, ok := s.Control.(interface{ ObserveManualRelay(int, bool) }); ok {
				observer.ObserveManualRelay(id, res.Actual)
			}
		}
		return errWrite
	}
	if controlled, ok := s.Control.(interface{ ManualOperation(int, func() error) error }); ok {
		errWrite = controlled.ManualOperation(id, write)
	} else {
		if s.Control != nil {
			cfg := s.Control.Snapshot().Config
			if cfg.Enabled && cfg.FanChannel == id {
				writeErr(w, http.StatusConflict, "fan_in_auto_mode_switch_to_manual_first")
				return
			}
		}
		errWrite = write()
	}
	if errors.Is(errWrite, control.ErrFanAutoMode) {
		writeErr(w, http.StatusConflict, "fan_in_auto_mode_switch_to_manual_first")
		return
	}
	err = errWrite

	status, code := http.StatusOK, "ok"
	switch {
	case err == nil:
	case errors.Is(err, errDeviceRecovering):
		status, code = http.StatusServiceUnavailable, "device_recovering"
	case errors.Is(err, relay.ErrInvalidChannel):
		status, code = http.StatusNotFound, "invalid_channel"
	case errors.Is(err, relay.ErrReadBackMismatch):
		status, code = http.StatusConflict, "readback_mismatch"
	case errors.Is(err, relay.ErrReadBackFailed):
		status, code = http.StatusBadGateway, "readback_failed"
	case errors.Is(err, relay.ErrWriteFailed):
		status, code = http.StatusBadGateway, "write_failed"
	default:
		status, code = http.StatusBadGateway, "device_error"
	}

	if status != http.StatusNotFound { // audit every real attempt, including failures
		s.audit(claims.Username, fmt.Sprintf("channel=%d label=%q requested=%v result=%s", id, res.Label, *req.On, code))
	}
	if err != nil {
		s.Log.Warn("relay write", "user", claims.Username, "channel", id, "err", err)
		writeJSON(w, status, map[string]any{"error": code, "result": res})
		return
	}
	// The UI consumes a Channel shape; return confirmed read-back, not the command.
	writeJSON(w, status, relay.Channel{ID: id, Label: res.Label, On: res.Actual})
}

// audit records who did what, using the username from the verified token.
func (s *Server) audit(actor, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Store.AddEvent(ctx, store.Event{Kind: "relay_write", Source: "relay", Message: msg, Actor: actor}); err != nil {
		s.Log.Error("audit write failed", "err", err)
	}
}

func (s *Server) getControl(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Control.Snapshot())
}

// putControl edits the control configuration. Omitted fields keep their
// current value; unknown fields are rejected so typos do not pass silently.
func (s *Server) putControl(w http.ResponseWriter, r *http.Request) {
	var patch control.ConfigPatch
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	claims, _ := auth.ClaimsFrom(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.Control.PatchConfig(ctx, patch, claims.Username); err != nil {
		if errors.Is(err, control.ErrInvalidConfig) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_config", "detail": err.Error()})
			return
		}
		s.Log.Error("update control config", "user", claims.Username, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, s.Control.Snapshot())
}

func (s *Server) getSpeed(w http.ResponseWriter, r *http.Request) {
	if !s.deviceAvailable("fan_speed") {
		writeErr(w, 503, "device_recovering")
		return
	}
	if s.Speed == nil {
		writeErr(w, 503, "speed_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	st, err := s.Speed.Status(ctx)
	if !s.deviceAvailable("fan_speed") {
		writeErr(w, 503, "device_recovering")
		return
	}
	if err != nil {
		writeErr(w, 502, "device_unreachable")
		return
	}
	writeJSON(w, 200, st)
}
func (s *Server) setSpeed(w http.ResponseWriter, r *http.Request) {
	if s.Speed == nil {
		writeErr(w, 503, "speed_unavailable")
		return
	}
	var req struct {
		Percent *int `json:"percent"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Percent == nil || *req.Percent < 0 || *req.Percent > 100 {
		writeErr(w, 400, "percent_must_be_0_to_100")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var st speed.State
	perform := func() error {
		if !s.deviceAvailable("fan_speed") {
			return errDeviceRecovering
		}
		var err error
		st, err = s.Speed.Set(ctx, *req.Percent)
		return err
	}
	var err error
	if ctl, ok := s.Control.(interface{ ManualSpeedOperation(func() error) error }); ok {
		err = ctl.ManualSpeedOperation(perform)
	} else if s.Control != nil && s.Control.Snapshot().Config.Enabled {
		err = control.ErrFanAutoMode
	} else {
		err = perform()
	}
	if errors.Is(err, control.ErrFanAutoMode) {
		writeErr(w, 409, "fan_in_auto_mode_switch_to_manual_first")
		return
	}
	if errors.Is(err, errDeviceRecovering) {
		writeErr(w, 503, "device_recovering")
		return
	}
	claims, _ := auth.ClaimsFrom(r.Context())
	msg := fmt.Sprintf("speed_pct=%d feedback_rpm=%d error=%v", *req.Percent, st.FeedbackRPM, err)
	auditCtx, auditCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer auditCancel()
	if auditErr := s.Store.AddEvent(auditCtx, store.Event{Kind: "fan_speed_write", Source: "fan_speed", Message: msg, Actor: claims.Username}); auditErr != nil {
		s.Log.Error("speed audit", "err", auditErr)
	}
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": "speed_write_failed", "detail": err.Error(), "state": st})
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) getThermocouple(w http.ResponseWriter, r *http.Request) {
	if s.Thermocouple == nil {
		writeErr(w, 503, "thermocouple_unavailable")
		return
	}
	channels := s.Thermocouple.Channels()
	if !s.deviceAvailable("thermocouple") {
		for i := range channels {
			channels[i].Status = "unknown"
			channels[i].Temperature = nil
		}
	}
	all := true
	for _, ch := range channels {
		if ch.Status != "fault" {
			all = false
		}
	}
	writeJSON(w, 200, map[string]any{"channels": channels, "all_channels_faulted": all})
}

func (s *Server) resetDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target string `json:"target"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	claims, _ := auth.ClaimsFrom(r.Context())
	err := json.NewDecoder(r.Body).Decode(&req)
	result := "attempt"
	audit := func(result string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if e := s.Store.AddEvent(ctx, store.Event{Kind: "device_reset", Source: req.Target, Actor: claims.Username, Message: result, At: time.Now()}); e != nil {
			s.Log.Error("reset audit", "err", e)
		}
	}
	audit(result)
	if err != nil || !reset.Valid(req.Target) {
		audit("invalid_target")
		writeErr(w, 400, "invalid_target")
		return
	}
	if s.ResetDevice == nil {
		audit("unavailable")
		writeErr(w, 503, "reset_unavailable")
		return
	}
	if err = s.ResetDevice(r.Context(), req.Target); err != nil {
		audit("failed: " + err.Error())
		code := 502
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
			code = 504
		}
		writeErr(w, code, "reset_failed")
		return
	}
	audit("initiated")
	writeJSON(w, 202, map[string]string{"target": req.Target, "state": "recovering"})
}

func (s *Server) deviceAvailable(name string) bool {
	if s.Statuses == nil {
		return true
	}
	for _, st := range s.Statuses() {
		if st.Name == name {
			return st.State == poller.StateOnline
		}
	}
	return true
}
