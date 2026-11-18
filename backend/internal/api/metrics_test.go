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
