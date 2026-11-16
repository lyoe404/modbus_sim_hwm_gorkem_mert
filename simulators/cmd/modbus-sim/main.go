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

	"sim/internal/device"
)

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

	store := device.NewStore()
	sup := &supervisor{url: "tcp://" + env("MODBUS_ADDR", "0.0.0.0:5020"), h: device.NewHandler(store)}
	if err := sup.up(); err != nil {
		log.Error("modbus listener failed", "err", err)
		os.Exit(1)
	}
	defer sup.down()
	log.Info("modbus-sim listening", "url", sup.url)

	// Sensor random walk.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				store.Step()
			}
		}
	}()

	// Admin / fault-injection API.
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, store.Snapshot())
	})
	// POST /fault?mode=none|slow|timeout|error|garbage|down[&delay=3s]
	mux.HandleFunc("/fault", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		mode, err := device.ParseFaultMode(r.URL.Query().Get("mode"))
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
		if mode == device.FaultDown {
			sup.down()
		} else if err := sup.up(); err != nil {
			http.Error(w, "restart failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Info("fault set", "mode", mode)
		writeJSON(w, http.StatusOK, store.Snapshot())
	})
	// POST /temp?value=33.5  pins the temperature; /temp?value=auto resumes the walk.
	mux.HandleFunc("/temp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		v := r.URL.Query().Get("value")
		if v == "auto" {
			store.UnpinTemp()
		} else {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				http.Error(w, "value must be a number or 'auto'", http.StatusBadRequest)
				return
			}
			store.PinTemp(f)
		}
		writeJSON(w, http.StatusOK, store.Snapshot())
	})

	admin := &http.Server{
		Addr:              env("ADMIN_ADDR", "0.0.0.0:5021"),
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
