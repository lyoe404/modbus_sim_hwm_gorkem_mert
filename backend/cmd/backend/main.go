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
	relays := &relay.Controller{URL: modbusURL, Timeout: 2 * time.Second, Labels: labels}
	defer relays.Close()

	// Rules engine: reads fresh temperatures, drives the fan relay, thresholds live in the DB.
	engine := control.New(control.Options{
		Switch:   relaySwitch(relays),
		Recorder: eventRecorder(db),
		Store:    db,
		Log:      log,
		Channels: len(labels),
		MaxAge:   3 * pollInterval, // never act on a reading older than three polls
	})
	if err := engine.Load(ctx); err != nil {
		return err
	}

	// Every connectivity change becomes an audit event and informs the engine.
	onChange := func(s poller.DeviceStatus) {
		ectx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		msg := fmt.Sprintf("state=%s consecutive_failures=%d", s.State, s.ConsecutiveFailures)
		if s.LastError != "" {
			msg += " last_error=" + s.LastError
		}
		if err := db.AddEvent(ectx, store.Event{Kind: "status_change", Source: s.Name, Message: msg}); err != nil {
			log.Error("write event failed", "err", err)
		}
		engine.OnStatus(ectx, s.State == poller.StateOnline)
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

	srv := &http.Server{
		Addr: env("HTTP_ADDR", ":8080"),
		Handler: (&api.Server{
			Tokens: tokens, Users: users, Store: db, Relays: relays, Control: engine,
			Statuses: func() []poller.DeviceStatus { return []poller.DeviceStatus{p.Status()} },
			Log:      log,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); p.Run(ctx) }()
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
