package main

import (
	"context"

	"backend/internal/control"
	"backend/internal/poller"
	"backend/internal/relay"
	"backend/internal/store"
)

const (
	sensorSource      = "sensor"
	temperatureMetric = "temperature_c"
)

// engineSink feeds fresh temperature readings to the control engine. The
// poller only calls its sinks after a successful, validated read, so the
// engine never sees an error or bad-data value from this path.
type engineSink struct{ eng *control.Engine }

func (s engineSink) Save(_ context.Context, ms []poller.Measurement) error {
	for _, m := range ms {
		if m.Source == sensorSource && m.Metric == temperatureMetric {
			s.eng.Observe(control.Reading{TempC: m.Value, At: m.At})
		}
	}
	return nil
}

// relaySwitch adapts the relay controller (write + read-back) to control.Switch.
func relaySwitch(r *relay.Controller) control.Switch {
	return control.SwitchFunc(func(ctx context.Context, channel int, on bool) error {
		_, err := r.Set(ctx, channel, on)
		return err
	})
}

// eventRecorder adapts the store's audit log to control.Recorder.
func eventRecorder(db *store.Store) control.Recorder {
	return control.RecorderFunc(func(ctx context.Context, kind, source, message, actor string) error {
		return db.AddEvent(ctx, store.Event{Kind: kind, Source: source, Message: message, Actor: actor})
	})
}
