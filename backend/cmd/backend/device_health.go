package main

import (
	"backend/internal/poller"
	"backend/internal/relay"
	"backend/internal/store"
	"context"
	"fmt"
	"log/slog"
	"time"
)

type noopSink struct{}

func (noopSink) Save(context.Context, []poller.Measurement) error { return nil }

type relayReader struct{ controller *relay.Controller }

func (r relayReader) Read(ctx context.Context) ([]poller.Measurement, error) {
	_, err := r.controller.Channels(ctx)
	return nil, err
}

// Controller connection is shared with the API and closed by main after pollers stop.
func (r relayReader) Close() error { return nil }
func deviceEvent(db *store.Store, log *slog.Logger) func(poller.DeviceStatus) {
	return func(st poller.DeviceStatus) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := db.AddEvent(ctx, store.Event{Kind: "status_change", Source: st.Name, Message: fmt.Sprintf("state=%s failures=%d error=%s", st.State, st.ConsecutiveFailures, st.LastError)}); err != nil {
			log.Error("device status audit", "err", err)
		}
	}
}
