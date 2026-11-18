package api

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"syscall"
	"time"

	"backend/internal/poller"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/simonvetter/modbus"
)

var pollDevices = []string{"sensor", "relay", "thermocouple", "fan_speed"}
var resetTargets = []string{"temp-humidity", "relay", "thermocouple", "speed-pwm"}
var pollReasons = []string{"timeout", "connection", "protocol", "invalid_data", "other"}
var eventKinds = []string{"status_change", "device_reset", "relay_write", "fan_speed_write", "fan_transition", "fan_sync", "fan_actuation_failed", "fan_speed_failed", "fan_speed_transition", "control_suspended", "control_resumed", "config_change", "control_mode_change"}

// Operations owns a private registry per backend instance.
type Operations struct {
	registry                                                 *prometheus.Registry
	polls, errors, resets, recoveries, events, controlErrors *prometheus.CounterVec
	duration                                                 *prometheus.HistogramVec
}

func NewOperations() *Operations {
	counter := func(name, help string, labels ...string) *prometheus.CounterVec {
		return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	}
	o := &Operations{
		registry:      prometheus.NewRegistry(),
		polls:         counter("hwm_modbus_polls_total", "Completed Modbus polling reads by result.", "device", "result"),
		errors:        counter("hwm_modbus_errors_total", "Failed Modbus polling reads by reason.", "device", "reason"),
		resets:        counter("hwm_reset_total", "Reset command attempts and acknowledgment results.", "target", "result"),
		recoveries:    counter("hwm_reset_recovery_total", "Observed reset recovery outcomes.", "target", "result"),
		events:        counter("hwm_operational_events_total", "Persisted operational events by kind.", "kind"),
		controlErrors: counter("hwm_control_errors_total", "Manual actuation errors and automatic failure episodes.", "operation"),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "hwm_modbus_poll_duration_seconds", Help: "Duration of a polling read, including connection and client lock waits.",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2, 3, 5, 10},
		}, []string{"device"}),
	}
	o.registry.MustRegister(o.polls, o.errors, o.resets, o.recoveries, o.events, o.controlErrors, o.duration)
	for _, device := range pollDevices {
		o.polls.WithLabelValues(device, "success")
		o.polls.WithLabelValues(device, "failure")
		o.duration.WithLabelValues(device)
		for _, reason := range pollReasons {
			o.errors.WithLabelValues(device, reason)
		}
	}
	for _, target := range resetTargets {
		for _, result := range []string{"attempt", "success", "failure"} {
			o.resets.WithLabelValues(target, result)
		}
		o.recoveries.WithLabelValues(target, "verified")
		o.recoveries.WithLabelValues(target, "failed")
	}
	for _, kind := range eventKinds {
		o.events.WithLabelValues(kind)
	}
	for _, op := range []string{"manual_relay", "manual_pwm", "auto_relay", "auto_pwm"} {
		o.controlErrors.WithLabelValues(op)
	}
	return o
}

func (o *Operations) ObservePoll(device string, elapsed time.Duration, err error) {
	if !slices.Contains(pollDevices, device) {
		return
	}
	result := "success"
	if err != nil {
		result = "failure"
		o.errors.WithLabelValues(device, pollReason(err)).Inc()
	}
	o.polls.WithLabelValues(device, result).Inc()
	o.duration.WithLabelValues(device).Observe(elapsed.Seconds())
}

func pollReason(err error) string {
	var network net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &network) && network.Timeout():
		return "timeout"
	case errors.Is(err, poller.ErrBadData):
		return "invalid_data"
	case errors.Is(err, modbus.ErrProtocolError), errors.Is(err, modbus.ErrUnknownProtocolId), errors.Is(err, modbus.ErrBadCRC):
		return "protocol"
	case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNREFUSED), errors.As(err, new(*net.DNSError)), errors.As(err, new(*os.SyscallError)):
		return "connection"
	default:
		return "other"
	}
}

func (o *Operations) ResetCommand(target, result string) {
	if slices.Contains(resetTargets, target) && (result == "attempt" || result == "success" || result == "failure") {
		o.resets.WithLabelValues(target, result).Inc()
	}
}
func (o *Operations) Recovery(target, result string) {
	if slices.Contains(resetTargets, target) && (result == "verified" || result == "failed") {
		o.recoveries.WithLabelValues(target, result).Inc()
	}
}

func (o *Operations) Event(kind string) {
	if slices.Contains(eventKinds, kind) {
		o.events.WithLabelValues(kind).Inc()
	}
}

func (o *Operations) ControlError(operation string) {
	if o != nil && (operation == "manual_relay" || operation == "manual_pwm" || operation == "auto_relay" || operation == "auto_pwm") {
		o.controlErrors.WithLabelValues(operation).Inc()
	}
}
