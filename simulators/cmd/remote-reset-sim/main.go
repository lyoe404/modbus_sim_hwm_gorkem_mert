package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sim/internal/remotereset"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	targets := map[string]string{
		"temp-humidity": env("TEMP_HUMIDITY_ADMIN_URL", "http://temp-humidity-sim:5121"),
		"relay":         env("RELAY_ADMIN_URL", "http://relay-sim:5122"),
		"thermocouple":  env("THERMOCOUPLE_ADMIN_URL", "http://thermocouple-sim:5123"),
		"speed-pwm":     env("SPEED_PWM_ADMIN_URL", "http://speed-pwm-sim:5124"),
	}

	dur, err := time.ParseDuration(env("RESET_DURATION", "5s"))
	if err != nil {
		dur = 5 * time.Second
	}

	srv := remotereset.NewServer(env("TCP_ADDR", "0.0.0.0:9001"), targets, dur, log)
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Error("remote-reset server failed", "err", err)
		os.Exit(1)
	}
	log.Info("remote-reset server shut down cleanly")
}
