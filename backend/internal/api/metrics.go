package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var b strings.Builder

	// 1. Device connectivity status
	b.WriteString("# HELP hwm_device_online Whether the field device is currently online (1=online, 0=degraded/offline)\n")
	b.WriteString("# TYPE hwm_device_online gauge\n")
	b.WriteString("# HELP hwm_device_consecutive_failures Consecutive polling failures per device\n")
	b.WriteString("# TYPE hwm_device_consecutive_failures gauge\n")
	for _, st := range s.Statuses() {
		online := 0
		if st.State == "online" {
			online = 1
		}
		fmt.Fprintf(&b, "hwm_device_online{device=%q,state=%q} %d\n", st.Name, st.State, online)
		fmt.Fprintf(&b, "hwm_device_consecutive_failures{device=%q} %d\n", st.Name, st.ConsecutiveFailures)
	}

	// 2. Latest sensor readings (temperature & humidity)
	since := time.Now().Add(-15 * time.Second)
	sensorOnline := false
	for _, st := range s.Statuses() {
		if st.Name == "sensor" && st.State == "online" && time.Since(st.LastSeen) < 15*time.Second {
			sensorOnline = true
		}
	}
	if temps, err := s.Store.Measurements(ctx, "sensor", "temperature_c", since, 1); sensorOnline && err == nil && len(temps) > 0 {
		b.WriteString("# HELP hwm_sensor_temperature_celsius Latest ambient temperature in Celsius\n")
		b.WriteString("# TYPE hwm_sensor_temperature_celsius gauge\n")
		fmt.Fprintf(&b, "hwm_sensor_temperature_celsius{source=%q} %.2f\n", temps[0].Source, temps[0].Value)
	}
	if humids, err := s.Store.Measurements(ctx, "sensor", "humidity_pct", since, 1); sensorOnline && err == nil && len(humids) > 0 {
		b.WriteString("# HELP hwm_sensor_humidity_percent Latest relative humidity percentage\n")
		b.WriteString("# TYPE hwm_sensor_humidity_percent gauge\n")
		fmt.Fprintf(&b, "hwm_sensor_humidity_percent{source=%q} %.2f\n", humids[0].Source, humids[0].Value)
	}

	// 3. Relay channel states (read-back confirmed)
	if chs, err := s.Relays.Channels(ctx); err == nil {
		b.WriteString("# HELP hwm_relay_channel_state Current read-back state of relay channels (1=ON, 0=OFF)\n")
		b.WriteString("# TYPE hwm_relay_channel_state gauge\n")
		for _, ch := range chs {
			val := 0
			if ch.On {
				val = 1
			}
			fmt.Fprintf(&b, "hwm_relay_channel_state{channel=\"%d\",label=%q} %d\n", ch.ID, ch.Label, val)
		}
	}

	// 4. Hysteresis control loop config & state
	if s.Control != nil {
		snap := s.Control.Snapshot()
		b.WriteString("# HELP hwm_control_threshold_celsius Configured hysteresis thresholds in Celsius\n")
		b.WriteString("# TYPE hwm_control_threshold_celsius gauge\n")
		fmt.Fprintf(&b, "hwm_control_threshold_celsius{bound=\"on_above\"} %.2f\n", snap.Config.OnAbove)
		fmt.Fprintf(&b, "hwm_control_threshold_celsius{bound=\"off_below\"} %.2f\n", snap.Config.OffBelow)

		b.WriteString("# HELP hwm_control_suspended Whether auto fan control is currently suspended (1=suspended, 0=active)\n")
		b.WriteString("# TYPE hwm_control_suspended gauge\n")
		susp := 0
		if snap.Suspended {
			susp = 1
		}
		fmt.Fprintf(&b, "hwm_control_suspended %d\n", susp)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}
