package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
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
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strings.TrimPrefix(env("TCP_ADDR", "0.0.0.0:9001"), "0.0.0.0:"), time.Second)
		if err != nil {
			os.Exit(1)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, err = fmt.Fprintln(conn, "PING")
		if err != nil {
			os.Exit(1)
		}
		response, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil || strings.TrimSpace(response) != "PONG" {
			os.Exit(1)
		}
		return
	}

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
