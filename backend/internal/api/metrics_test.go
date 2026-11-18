package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/poller"
	"backend/internal/relay"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

type labelledRelays struct {
	fakeRelays
	label string
}

func (r *labelledRelays) Channels(context.Context) ([]relay.Channel, error) {
	return []relay.Channel{{ID: 0, Label: r.label}}, nil
}

func TestMetricsPrometheusParsingAndFamilyGrouping(t *testing.T) {
	label := "Fan \"A\" \\ left\nupper\tpanel\r"
	server := &Server{
		Relays: &labelledRelays{label: label},
		Store:  &fakeStore{},
		Statuses: func() []poller.DeviceStatus {
			return []poller.DeviceStatus{
				{Name: "sensor", State: poller.StateOnline},
				{Name: "fan_speed", State: poller.StateOffline, ConsecutiveFailures: 3},
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
	}{
		{poller.StateOnline, 1},
		{poller.StateDegraded, 0},
		{poller.StateOffline, 0},
		{poller.StateRecovering, 0},
		{poller.StateOnline, 1},
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
	}
}
