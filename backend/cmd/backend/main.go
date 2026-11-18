// Command backend is the device-facing platform backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"backend/internal/api"
	"backend/internal/auth"
	"backend/internal/control"
	"backend/internal/envfile"
	"backend/internal/poller"
	"backend/internal/relay"
	"backend/internal/reset"
	"backend/internal/speed"
	"backend/internal/store"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// buildAuth reads secrets from the environment; nothing is hard-coded.
func buildAuth() (*auth.Tokens, *auth.Users, error) {
	tokens, err := auth.NewTokens(os.Getenv("JWT_SECRET"), time.Hour)
	if err != nil {
		return nil, nil, fmt.Errorf("JWT_SECRET: %w", err)
	}
	users, err := auth.NewUsers()
	if err != nil {
		return nil, nil, err
	}
	accounts := []struct{ userVar, passVar, defUser, role string }{
		{"VIEWER_USERNAME", "VIEWER_PASSWORD", "viewer", auth.RoleViewer},
		{"OPERATOR_USERNAME", "OPERATOR_PASSWORD", "operator", auth.RoleOperator},
	}
	for _, a := range accounts {
		if err := users.Add(env(a.userVar, a.defUser), os.Getenv(a.passVar), a.role); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", a.passVar, err)
		}
	}
	return tokens, users, nil
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Optional .env in the working directory; real environment variables take precedence.
	if err := envfile.Load(".env"); err != nil {
		return fmt.Errorf("load .env: %w", err)
	}

	tokens, users, err := buildAuth()
	if err != nil {
		return err
	}

	dbPath := env("DB_PATH", filepath.Join("data", "backend.db"))
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	const pollInterval = 2 * time.Second
	modbusURL := env("MODBUS_URL", "tcp://127.0.0.1:5020")

	labels, err := relay.LoadLabels(os.Getenv("RELAY_LABELS_FILE"))
	if err != nil {
		return fmt.Errorf("relay labels: %w", err)
	}

	relayURL := env("RELAY_MODBUS_URL", "tcp://relay-sim:5022")
	relays := &relay.Controller{
		URL:     relayURL,
		Timeout: 2 * time.Second,
		Labels:  labels,
	}
	defer relays.Close()
	speedControl := &speed.Controller{URL: env("SPEED_MODBUS_URL", "tcp://speed-pwm-sim:5024"), Timeout: 2 * time.Second}
	defer speedControl.Close()

	// Rules engine: reads fresh temperatures, drives the fan relay, thresholds live in the DB.
	engine := control.New(control.Options{
		Speed:     speedControl,
		Switch:    relaySwitch(relays),
		FanReader: fanCoilReader{controller: relays},
		Recorder:  eventRecorder(db),
		Store:     db,
		Log:       log,
		Channels:  len(labels),
		MaxAge:    3 * pollInterval, // never act on a reading older than three polls
	})
	if err := engine.Load(ctx); err != nil {
		return err
	}

	var resetAuditMu sync.Mutex
	resetActors := make(map[string]string)
	// Persist each observed device health snapshot, including repeated failures and
	// last-seen time. Do not use persisted online statuses to drive automation.
	persistStatus := func(st poller.DeviceStatus) {
		dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := db.SaveDeviceStatus(dbCtx, st); err != nil && ctx.Err() == nil {
			log.Error("persist device health", "device", st.Name, "err", err)
		}
		if st.State == poller.StateOnline {
			resetAuditMu.Lock()
			by, ok := resetActors[st.Name]
			if ok {
				delete(resetActors, st.Name)
			}
			resetAuditMu.Unlock()
			if ok {
				if err := db.AddEvent(dbCtx, store.Event{Kind: "device_reset", Source: st.Name, Actor: by, Message: "recovered after fresh verified read"}); err != nil {
					log.Error("reset recovery audit", "err", err)
				}
			}
		}

	}

	// Every connectivity change becomes an audit event and informs the engine.
	onChange := func(s poller.DeviceStatus) {
		ectx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		engine.OnStatus(ectx, s.State == poller.StateOnline)
		msg := fmt.Sprintf("state=%s consecutive_failures=%d", s.State, s.ConsecutiveFailures)
		if s.LastError != "" {
			msg += " last_error=" + s.LastError
		}
		if err := db.AddEvent(ectx, store.Event{Kind: "status_change", Source: s.Name, Message: msg}); err != nil {
			log.Error("write event failed", "err", err)
		}
	}

	sensor := &poller.ModbusSensor{
		Source:  sensorSource,
		URL:     modbusURL,
		Timeout: 2 * time.Second,
	}
	p := poller.New(
		poller.Config{
			Name:     sensorSource,
			Interval: pollInterval,
			Backoff:  poller.Backoff{Base: time.Second, Max: 30 * time.Second, Jitter: 0.2},
		},
		sensor, poller.MultiSink{db, poller.LogSink{Log: log}, engineSink{engine}}, log, onChange,
	)

	thermoReader := &poller.Thermocouple{URL: env("THERMOCOUPLE_MODBUS_URL", "tcp://thermocouple-sim:5023"), Timeout: 2 * time.Second}
	thermo := poller.New(poller.Config{Name: "thermocouple", Interval: pollInterval, Backoff: poller.Backoff{Base: time.Second, Max: 30 * time.Second, Jitter: 0.2}},
		thermoReader, db, log, deviceEvent(db, log))
	pwm := poller.New(poller.Config{Name: "fan_speed", Interval: pollInterval, Backoff: poller.Backoff{Base: time.Second, Max: 30 * time.Second, Jitter: 0.2}},
		&poller.SpeedReader{URL: env("SPEED_MODBUS_URL", "tcp://speed-pwm-sim:5024"), Timeout: 2 * time.Second}, db, log, deviceEvent(db, log))
	// Read-only relay health polling does not interfere with writes.
	relayHealth := poller.New(poller.Config{Name: "relay", Interval: pollInterval, Backoff: poller.Backoff{Base: time.Second, Max: 30 * time.Second, Jitter: 0.2}},
		relayReader{controller: relays}, noopSink{}, log, deviceEvent(db, log))
	for _, current := range []*poller.Poller{p, relayHealth, thermo, pwm} {
		current.SetOnPoll(persistStatus)
	}
	resetClient := &reset.Client{Addr: env("RESET_TCP_ADDR", "remote-reset-sim:9001"), Timeout: 4 * time.Second}
	targets := map[string]*poller.Poller{"temp-humidity": p, "relay": relayHealth, "thermocouple": thermo, "speed-pwm": pwm}
	resetDevice := func(ctx context.Context, target string) error {
		return engine.ResetOperation(func() error {
			// Invalidate before sending: a lost acknowledgement may still mean reset occurred.
			ready := make(chan struct{})
			targets[target].BeginReset(func(ctx context.Context) (string, error) {
				select {
				case <-ready:
					return resetClient.Status(ctx, target)
				default:
					return "recovering", nil
				}
			})
			claims, _ := auth.ClaimsFrom(ctx)
			deviceName := target
			if target == "temp-humidity" {
				deviceName = "sensor"
			}
			if target == "speed-pwm" {
				deviceName = "fan_speed"
			}
			resetAuditMu.Lock()
			resetActors[deviceName] = claims.Username
			resetAuditMu.Unlock()
			if target == "thermocouple" {
				thermoReader.Invalidate()
			}
			_, err := resetClient.Command(ctx, "RESET", target)
			close(ready)
			return err
		})
	}
	srv := &http.Server{
		Addr: env("HTTP_ADDR", ":8080"),
		Handler: (&api.Server{
			Tokens: tokens, Users: users, Store: db, Relays: relays, Control: engine, Speed: speedControl, Thermocouple: thermoReader, ResetDevice: resetDevice,
			Statuses: func() []poller.DeviceStatus {
				return []poller.DeviceStatus{p.Status(), relayHealth.Status(), thermo.Status(), pwm.Status()}
			},
			Log: log,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(6)
	go func() { defer wg.Done(); p.Run(ctx) }()
	go func() { defer wg.Done(); relayHealth.Run(ctx) }()
	go func() { defer wg.Done(); thermo.Run(ctx) }()
	go func() { defer wg.Done(); pwm.Run(ctx) }()
	go func() { defer wg.Done(); engine.Run(ctx) }()
	go func() {
		defer wg.Done()
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
			stop()
		}
	}()

	log.Info("backend started", "modbus", sensor.URL, "db", dbPath, "http", srv.Addr)
	<-ctx.Done()
	log.Info("shutting down")

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	wg.Wait() // poller, engine and HTTP server stop before the DB closes (deferred above)
	return nil
}
