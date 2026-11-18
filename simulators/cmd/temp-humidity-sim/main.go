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
	temphumidity "sim/internal/temphumidity"
)

type supervisor struct {
	mu        sync.Mutex
	addr      string
	store     *temphumidity.Store
	h         modbus.RequestHandler
	srv       *modbus.ModbusServer
	malformed func()
}

func (s *supervisor) setFault(mode fault.Mode, delay time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.SetFault(mode, delay)
	switch mode {
	case fault.Down:
		s.stopLocked()
		return nil
	case fault.Malformed:
		if s.malformed == nil {
			s.stopLocked()
			stop, err := temphumidity.StartMalformedListener(s.addr)
			if err != nil {
				return err
			}
			s.malformed = stop
		}
		return nil
	}
	if s.srv != nil {
		return nil
	}
	s.stopLocked()
	srv, err := modbus.NewServer(&modbus.ServerConfiguration{
		URL:        "tcp://" + s.addr,
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
	s.stopLocked()
}

func (s *supervisor) stopLocked() {
	if s.srv != nil {
		_ = s.srv.Stop()
		s.srv = nil
	}
	if s.malformed != nil {
		s.malformed()
		s.malformed = nil
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

	store := temphumidity.NewStore()
	sup := &supervisor{
		addr:  env("MODBUS_ADDR", "0.0.0.0:5021"),
		store: store,
		h:     temphumidity.NewHandler(store),
	}
	if err := sup.setFault(fault.None, 0); err != nil {
		log.Error("modbus listener failed", "err", err)
		os.Exit(1)
	}
	defer sup.down()
	log.Info("temp-humidity-sim listening", "addr", sup.addr)

	// Periodic simulation step
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
		mode, err := fault.ParseMode(r.URL.Query().Get("mode"), fault.Malformed)
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
		if err := sup.setFault(mode, delay); err != nil {
			http.Error(w, "restart failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Info("fault set", "mode", mode)
		writeJSON(w, http.StatusOK, store.Snapshot())
	})

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
		Addr:              env("ADMIN_ADDR", "0.0.0.0:5121"),
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
