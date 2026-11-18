package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/control"
	"backend/internal/poller"
	"backend/internal/relay"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

type noScrapeRelays struct{ fakeRelays }

func (*noScrapeRelays) Channels(context.Context) ([]relay.Channel, error) {
	panic("scrape should not read Modbus coils")
}

func TestMetricsPrometheusParsingAndFamilyGrouping(t *testing.T) {
	label := "Fan \"A\" \\ left\nupper\tpanel\r"
	server := &Server{
		Relays: &noScrapeRelays{},
		DeviceMetrics: func() []DeviceMetrics {
			now := time.Now()
			return []DeviceMetrics{
				{Status: poller.DeviceStatus{Name: "fan_speed", State: poller.StateOffline, ConsecutiveFailures: 3}},
				{Status: poller.DeviceStatus{Name: "relay", State: poller.StateOnline, LastSeen: now},
					Measurements: []poller.Measurement{{Source: label, Metric: "coil", At: now}}},
			}
		},
	}
	response := httptest.NewRecorder()
	server.metrics(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	coil := families["hwm_relay_channel_state"]
	if coil == nil || len(coil.Metric) != 1 {
		t.Fatalf("missing relay sample: %s", body)
	}
	var parsedLabel string
	for _, pair := range coil.Metric[0].Label {
		if pair.GetName() == "label" {
			parsedLabel = pair.GetValue()
		}
	}
	if parsedLabel != label {
		t.Fatalf("label round trip = %q, want %q", parsedLabel, label)
	}
	if last, next := strings.LastIndex(body, "\nhwm_device_online{"), strings.Index(body, "\n# HELP hwm_device_consecutive_failures"); last < 0 || next < last {
		t.Fatalf("device metric families are interleaved:\n%s", body)
	}
}

type metricControl struct {
	fakeControl
	value control.Snapshot
}

func (c *metricControl) Snapshot() control.Snapshot { return c.value }

func TestControlAndFanTelemetry(t *testing.T) {
	now := time.Now()
	on := true
	ctrl := &metricControl{value: control.Snapshot{Config: control.DefaultConfig(), FanOn: &on}}
	relayState := poller.StateOnline
	pwmAge := time.Duration(0)
	s := &Server{Control: ctrl, DeviceMetrics: func() []DeviceMetrics {
		return []DeviceMetrics{
			{Status: poller.DeviceStatus{Name: "relay", State: relayState, LastSeen: now}, Measurements: []poller.Measurement{{Metric: "coil", At: now, Value: 1}}},
			{Status: poller.DeviceStatus{Name: "sensor", State: poller.StateOnline, LastSeen: now}},
			{Status: poller.DeviceStatus{Name: "fan_speed", State: poller.StateOnline, LastSeen: now}, Measurements: []poller.Measurement{
				{Metric: "command_pct", At: now.Add(-pwmAge), Value: 70},
				{Metric: "feedback_rpm", At: now.Add(-pwmAge), Value: 1234},
				{Metric: "mismatch", At: now.Add(-pwmAge), Value: 1},
			}},
		}
	}}
	body := func() string {
		response := httptest.NewRecorder()
		s.metrics(response, httptest.NewRequest("GET", "/metrics", nil))
		return response.Body.String()
	}
	assertSamples := func(out string, samples ...string) {
		t.Helper()
		for _, sample := range samples {
			if !strings.Contains(out, "\n"+sample+"\n") {
				t.Errorf("missing sample %q", sample)
			}
		}
	}
	assertSamples(body(), "hwm_control_state_code 2", "hwm_control_fan_on 1", "hwm_fan_speed_command_percent 70", "hwm_fan_speed_feedback_rpm 1234", "hwm_fan_speed_mismatch 1")
	ctrl.value.Suspended = true
	assertSamples(body(), "hwm_control_state_code 3", "hwm_control_auto_enabled 1", "hwm_control_suspended 1")
	ctrl.value.Config.Enabled = false
	assertSamples(body(), "hwm_control_state_code 1", "hwm_control_auto_enabled 0", "hwm_control_suspended 0")
	relayState, pwmAge = poller.StateRecovering, time.Minute
	if out := body(); strings.Contains(out, "hwm_control_fan_on") || strings.Contains(out, "hwm_fan_speed_feedback_rpm") {
		t.Errorf("unavailable or stale telemetry was exported")
	}
}

func TestThermocoupleTelemetryUnknownAndFaulted(t *testing.T) {
	at := time.Unix(1700000000, 0)
	temp := -3.5
	channels := []poller.ThermocoupleChannel{
		{Status: "live", Temperature: &temp, LastValid: at},
		{Status: "fault", LastValid: at},
		{Status: "fault"},
		{Status: "unknown"},
	}
	for _, online := range []bool{true, false} {
		var b gaugeText
		thermocoupleMetrics(&b, online, channels)
		parser := expfmt.NewTextParser(model.LegacyValidation)
		m, err := parser.TextToMetricFamilies(strings.NewReader(b.String()))
		if err != nil {
			t.Fatal(err)
		}
		if len(m["hwm_thermocouple_state_code"].GetMetric()) != 4 ||
			m["hwm_thermocouple_last_valid_timestamp_seconds"].GetMetric()[1].GetGauge().GetValue() != float64(at.Unix()) {
			t.Fatal("missing status or original valid timestamp")
		}
		if online {
			if len(m["hwm_thermocouple_fault"].GetMetric()) != 3 || m["hwm_thermocouple_temperature_celsius"].GetMetric()[0].GetGauge().GetValue() != temp {
				t.Fatal("unexpected confirmed fault or temperature samples")
			}
		} else if m["hwm_thermocouple_fault"] != nil || m["hwm_thermocouple_temperature_celsius"] != nil {
			t.Fatal("disconnected readings exported as valid")
		}
	}
}

func TestOperationMetricsConcurrentCollection(t *testing.T) {
	operations := NewOperations()
	server := &Server{Operations: operations}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				operations.ObservePoll("sensor", time.Millisecond, nil)
				operations.ObservePoll("sensor", time.Second, context.DeadlineExceeded)
				operations.ResetCommand("relay", "attempt")
				operations.ResetCommand("relay", "success")
				operations.Recovery("relay", "verified")
				operations.ControlError("manual_relay")
				operations.Event("status_change")
				response := httptest.NewRecorder()
				server.metrics(response, httptest.NewRequest("GET", "/metrics", nil))
				if response.Code != 200 {
					t.Errorf("scrape HTTP %d", response.Code)
				}
			}
		})
	}
	wg.Wait()
	operations.ResetCommand("unbounded", "success")
	operations.Event("unbounded")
	response := httptest.NewRecorder()
	server.metrics(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	for _, sample := range []string{
		`hwm_modbus_polls_total{device="sensor",result="success"} 80`,
		`hwm_modbus_polls_total{device="sensor",result="failure"} 80`,
		`hwm_modbus_errors_total{device="sensor",reason="timeout"} 80`,
		`hwm_modbus_poll_duration_seconds_count{device="sensor"} 160`,
		`hwm_reset_total{result="success",target="relay"} 80`,
		`hwm_reset_recovery_total{result="verified",target="relay"} 80`,
		`hwm_operational_events_total{kind="status_change"} 80`,
	} {
		if !strings.Contains(body, sample) {
			t.Errorf("missing %q", sample)
		}
	}
	if strings.Contains(body, "unbounded") {
		t.Error("unexpected unbounded label")
	}
}

func TestDeviceOnlineMetricKeepsRelaySeriesAcrossStateChanges(t *testing.T) {
	var state poller.State
	server := &Server{
		Relays: &fakeRelays{},
		Store:  &fakeStore{},
		Statuses: func() []poller.DeviceStatus {
			return []poller.DeviceStatus{{Name: "relay", State: state}}
		},
	}
	for _, test := range []struct {
		state poller.State
		value float64
		code  float64
	}{
		{poller.StateUnknown, 0, 0},
		{poller.StateOnline, 1, 1},
		{poller.StateDegraded, 0, 2},
		{poller.StateOffline, 0, 3},
		{poller.StateRecovering, 0, 4},
		{poller.StateRecoveryFailed, 0, 5},
		{poller.StateOnline, 1, 1},
	} {
		state = test.state
		response := httptest.NewRecorder()
		server.metrics(response, httptest.NewRequest("GET", "/metrics", nil))
		parser := expfmt.NewTextParser(model.LegacyValidation)
		families, err := parser.TextToMetricFamilies(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		metrics := families["hwm_device_online"].GetMetric()
		if len(metrics) != 1 {
			t.Fatalf("%s: got %d series, want 1", state, len(metrics))
		}
		labels := metrics[0].GetLabel()
		if len(labels) != 1 || labels[0].GetName() != "device" || labels[0].GetValue() != "relay" {
			t.Fatalf("%s: unexpected series labels: %v", state, labels)
		}
		if got := metrics[0].GetGauge().GetValue(); got != test.value {
			t.Fatalf("%s: got %v, want %v", state, got, test.value)
		}
		codes := families["hwm_device_state_code"].GetMetric()
		if len(codes) != 1 || codes[0].GetGauge().GetValue() != test.code {
			t.Fatalf("%s: expected device code %v", state, test.code)
		}
	}
}
