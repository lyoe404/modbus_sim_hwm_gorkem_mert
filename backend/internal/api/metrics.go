package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backend/internal/poller"

	"github.com/prometheus/common/expfmt"
)

var metricLabelEscapes = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func metricLabel(value string) string { return "\"" + metricLabelEscapes.Replace(value) + "\"" }

// DeviceMetrics pairs availability with the readings accepted in the same poll.
type DeviceMetrics struct {
	Status       poller.DeviceStatus
	Measurements []poller.Measurement
}

type metricSample struct {
	labels string
	value  float64
}

type gaugeText struct{ strings.Builder }

func (b *gaugeText) gauge(name, help string, samples ...metricSample) {
	if len(samples) == 0 {
		return
	}
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	for _, sample := range samples {
		fmt.Fprintf(b, "%s%s %g\n", name, sample.labels, sample.value)
	}
}

const liveMetricAge = 6 * time.Second

func (d DeviceMetrics) fresh(now time.Time) bool {
	age := now.Sub(d.Status.LastSeen)
	return d.Status.State == poller.StateOnline && age >= 0 && age <= liveMetricAge
}

func (d DeviceMetrics) measurement(now time.Time, metric string) (float64, bool) {
	if d.fresh(now) {
		for _, m := range d.Measurements {
			if m.Metric == metric && now.Sub(m.At) >= 0 && now.Sub(m.At) <= liveMetricAge {
				return m.Value, true
			}
		}
	}
	return 0, false
}

func deviceStateCode(state poller.State) float64 {
	switch state {
	case poller.StateOnline:
		return 1
	case poller.StateDegraded:
		return 2
	case poller.StateOffline:
		return 3
	case poller.StateRecovering:
		return 4
	case poller.StateRecoveryFailed:
		return 5
	default:
		return 0
	}
}

func flag(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	var b gaugeText
	var snapshots []DeviceMetrics
	if s.DeviceMetrics != nil {
		snapshots = s.DeviceMetrics()
	} else if s.Statuses != nil {
		for _, status := range s.Statuses() {
			snapshots = append(snapshots, DeviceMetrics{Status: status})
		}
	}
	now := time.Now()
	devices := make(map[string]DeviceMetrics, len(snapshots))
	var online, codes, failures []metricSample
	for _, device := range snapshots {
		st := device.Status
		devices[st.Name] = device
		label := "{device=" + metricLabel(st.Name) + "}"
		online = append(online, metricSample{label, flag(st.State == poller.StateOnline)})
		codes = append(codes, metricSample{label, deviceStateCode(st.State)})
		failures = append(failures, metricSample{label, float64(st.ConsecutiveFailures)})
	}
	b.gauge("hwm_device_online", "Whether a field device is online.", online...)
	b.gauge("hwm_device_consecutive_failures", "Consecutive polling failures per device.", failures...)
	b.gauge("hwm_device_state_code", "Device state: 0 unknown, 1 online, 2 degraded, 3 offline, 4 recovering, 5 recovery failed.", codes...)

	for _, gauge := range []struct{ device, measurement, name, help, labels string }{
		{"sensor", "temperature_c", "hwm_sensor_temperature_celsius", "Ambient temperature in Celsius.", "{source=\"sensor\"}"},
		{"sensor", "humidity_pct", "hwm_sensor_humidity_percent", "Relative humidity in percent.", "{source=\"sensor\"}"},
		{"fan_speed", "command_pct", "hwm_fan_speed_command_percent", "Verified PWM command in percent.", ""},
		{"fan_speed", "feedback_rpm", "hwm_fan_speed_feedback_rpm", "Independent fan tachometer feedback in RPM.", ""},
		{"fan_speed", "mismatch", "hwm_fan_speed_mismatch", "Whether PWM feedback persistently diverges after settling.", ""},
	} {
		if value, ok := devices[gauge.device].measurement(now, gauge.measurement); ok {
			b.gauge(gauge.name, gauge.help, metricSample{gauge.labels, value})
		}
	}
	relays := liveRelaySamples(devices["relay"], now)
	b.gauge("hwm_relay_channel_state", "Read-back state of relay channels (1 on, 0 off).", relays...)
	s.controlMetrics(&b, devices, now)
	if s.Thermocouple != nil {
		thermocoupleMetrics(&b, devices["thermocouple"].fresh(now), s.Thermocouple.Channels())
	}

	if s.Operations != nil {
		families, err := s.Operations.registry.Gather()
		if err != nil {
			http.Error(w, "could not gather metrics", http.StatusInternalServerError)
			return
		}
		for _, family := range families {
			if _, err := expfmt.MetricFamilyToText(&b, family); err != nil {
				http.Error(w, "could not encode metrics", http.StatusInternalServerError)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func liveRelaySamples(relays DeviceMetrics, now time.Time) []metricSample {
	var coils []metricSample
	if relays.fresh(now) {
		for channel, reading := range relays.Measurements {
			if age := now.Sub(reading.At); reading.Metric == "coil" && age >= 0 && age <= liveMetricAge {
				labels := "{channel=" + strconv.Quote(strconv.Itoa(channel)) + ",label=" + metricLabel(reading.Source) + "}"
				coils = append(coils, metricSample{labels, reading.Value})
			}
		}
	}
	return coils
}

func (s *Server) controlMetrics(b *gaugeText, devices map[string]DeviceMetrics, now time.Time) {
	if s.Control == nil {
		return
	}
	snap := s.Control.Snapshot()
	b.gauge("hwm_control_threshold_celsius", "Configured hysteresis thresholds in Celsius.",
		metricSample{"{bound=\"on_above\"}", snap.Config.OnAbove}, metricSample{"{bound=\"off_below\"}", snap.Config.OffBelow})
	b.gauge("hwm_control_auto_enabled", "Whether controller is in AUTO mode.", metricSample{"", flag(snap.Config.Enabled)})
	suspended := snap.Config.Enabled && snap.Suspended
	b.gauge("hwm_control_suspended", "Whether automatic control is suspended.", metricSample{"", flag(suspended)})
	relay := devices["relay"]
	code := 0.0
	switch {
	case !snap.Config.Enabled:
		code = 1
	case suspended:
		code = 3
	case snap.FanOn != nil && relay.fresh(now) && devices["sensor"].fresh(now):
		code = 2
	}
	b.gauge("hwm_control_state_code", "Control state: 0 unknown, 1 manual, 2 auto active, 3 auto suspended.", metricSample{"", code})

	channel := snap.Config.FanChannel
	if relay.fresh(now) && channel >= 0 && channel < len(relay.Measurements) {
		coil := relay.Measurements[channel]
		if age := now.Sub(coil.At); coil.Metric == "coil" && age >= 0 && age <= liveMetricAge {
			b.gauge("hwm_control_fan_on", "Confirmed state of the configured fan coil.", metricSample{"", coil.Value})
			return
		}
	}
}

func thermocoupleMetrics(b *gaugeText, online bool, channels []poller.ThermocoupleChannel) {
	var temperature, fault, status, lastValid []metricSample
	for i, channel := range channels {
		label := "{channel=" + strconv.Quote(strconv.Itoa(i)) + "}"
		code := 0.0
		if online {
			switch channel.Status {
			case "live":
				if channel.Temperature != nil {
					code = 1
					fault = append(fault, metricSample{label, 0})
					temperature = append(temperature, metricSample{label, *channel.Temperature})
				}
			case "fault":
				code = 2
				fault = append(fault, metricSample{label, 1})
			}
		}
		status = append(status, metricSample{label, code})
		if !channel.LastValid.IsZero() {
			lastValid = append(lastValid, metricSample{label, float64(channel.LastValid.Unix())})
		}
	}
	b.gauge("hwm_thermocouple_temperature_celsius", "Valid current thermocouple temperature in Celsius.", temperature...)
	b.gauge("hwm_thermocouple_fault", "Confirmed per-channel fault (1 fault, 0 healthy).", fault...)
	b.gauge("hwm_thermocouple_state_code", "Thermocouple channel state: 0 unknown, 1 healthy, 2 fault.", status...)
	b.gauge("hwm_thermocouple_last_valid_timestamp_seconds", "Unix timestamp of the last valid channel reading.", lastValid...)
}
