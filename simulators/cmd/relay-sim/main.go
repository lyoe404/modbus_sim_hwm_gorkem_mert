package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/simonvetter/modbus"

	"sim/internal/fault"
	relay "sim/internal/relay"
)

var _ = strconv.Atoi // keep import if unused in some binaries

type supervisor struct {
	mu  sync.Mutex
	url string
	h   modbus.RequestHandler
	srv *modbus.ModbusServer
}

func (s *supervisor) up() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		return nil
	}
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{
		URL:        s.url,
		Timeout:    60 * time.Second,
		MaxClients: 10,
	}, s.h)
	if err != nil {
		return err
	}
	if err := srv.Start(); err != nil {
		return err
	}
	s.srv = srv
	return nil
}

func (s *supervisor) down() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		_ = s.srv.Stop()
		s.srv = nil
	}
}

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

	store := relay.NewStore()
	sup := &supervisor{
		url: "tcp://" + env("MODBUS_ADDR", "0.0.0.0:5022"),
		h:   relay.NewHandler(store),
	}
	if err := sup.up(); err != nil {
		log.Error("modbus listener failed", "err", err)
		os.Exit(1)
	}
	defer sup.down()
	log.Info("relay-sim listening", "url", sup.url)

	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}

	mux.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, store.Snapshot())
	})

	mux.HandleFunc("/fault", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		mode, err := fault.ParseMode(r.URL.Query().Get("mode"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var delay time.Duration
		if d := r.URL.Query().Get("delay"); d != "" {
			if delay, err = time.ParseDuration(d); err != nil {
				http.Error(w, "bad delay: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		store.SetFault(mode, delay)
		if mode == fault.Down {
			sup.down()
		} else if err := sup.up(); err != nil {
			http.Error(w, "restart failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Info("fault set", "mode", mode)
		writeJSON(w, http.StatusOK, store.Snapshot())
	})

	admin := &http.Server{
		Addr:              env("ADMIN_ADDR", "0.0.0.0:5122"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := admin.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("admin server failed", "err", err)
			stop()
		}
	}()
	log.Info("admin API listening", "addr", admin.Addr)

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = admin.Shutdown(shutCtx)
}
