package poller

import (
	"context"
	"log/slog"
)

// LogSink prints measurements; a placeholder until the database layer exists.
type LogSink struct{ Log *slog.Logger }

// Save implements Sink.
func (l LogSink) Save(_ context.Context, ms []Measurement) error {
	for _, m := range ms {
		l.Log.Info("measurement", "source", m.Source, "metric", m.Metric, "value", m.Value)
	}
	return nil
}
