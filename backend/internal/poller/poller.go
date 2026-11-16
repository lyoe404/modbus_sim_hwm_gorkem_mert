package poller

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Config tunes one poller.
type Config struct {
	Name         string        // device name, used in status and logs
	Interval     time.Duration // normal polling period
	Backoff      Backoff       // retry delays while failing
	OfflineAfter int           // consecutive failures before "offline" (default 3)
}

// Poller polls a single device until its context is cancelled.
type Poller struct {
	cfg      Config
	reader   Reader
	sink     Sink
	log      *slog.Logger
	onChange func(DeviceStatus)

	mu     sync.RWMutex
	status DeviceStatus
}

// New builds a poller. onChange (optional) is called whenever State changes,
// from the poller goroutine; it must not block for long.
func New(cfg Config, r Reader, s Sink, log *slog.Logger, onChange func(DeviceStatus)) *Poller {
	if cfg.OfflineAfter <= 0 {
		cfg.OfflineAfter = 3
	}
	if log == nil {
		log = slog.Default()
	}
	return &Poller{
		cfg: cfg, reader: r, sink: s, log: log.With("device", cfg.Name), onChange: onChange,
		status: DeviceStatus{Name: cfg.Name, State: StateUnknown},
	}
}

// Status returns a copy of the current status; safe for concurrent use.
func (p *Poller) Status() DeviceStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

// Run blocks until ctx is cancelled, then closes the reader.
func (p *Poller) Run(ctx context.Context) {
	defer func() { _ = p.reader.Close() }()

	failures := 0
	timer := time.NewTimer(0) // first poll immediately
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		ms, err := p.reader.Read(ctx)
		if err != nil {
			failures++
			p.record(failures, err)
			p.log.Warn("poll failed", "err", err, "consecutive_failures", failures)
		} else {
			failures = 0
			p.record(0, nil)
			// A storage problem is not a device problem: log it, keep the device online.
			if serr := p.sink.Save(ctx, ms); serr != nil {
				p.log.Error("persist failed", "err", serr)
			}
		}

		next := p.cfg.Interval
		if failures > 0 {
			if d := p.cfg.Backoff.Delay(failures); d > next {
				next = d
			}
		}
		timer.Reset(next)
	}
}

func (p *Poller) record(failures int, err error) {
	p.mu.Lock()
	prev := p.status.State
	p.status.ConsecutiveFailures = failures
	switch {
	case err == nil:
		p.status.State = StateOnline
		p.status.LastSeen = time.Now()
		p.status.LastError = ""
	case failures >= p.cfg.OfflineAfter:
		p.status.State = StateOffline
		p.status.LastError = err.Error()
	default:
		p.status.State = StateDegraded
		p.status.LastError = err.Error()
	}
	cur := p.status
	p.mu.Unlock()

	if cur.State != prev {
		p.log.Info("status changed", "from", prev, "to", cur.State)
		if p.onChange != nil {
			p.onChange(cur)
		}
	}
}
