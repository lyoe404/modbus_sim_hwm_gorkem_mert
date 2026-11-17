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
	onPoll   func(DeviceStatus)

	cycleMu    sync.Mutex
	mu         sync.RWMutex
	status     DeviceStatus
	generation uint64
	recovery   func(context.Context) (string, error)
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

// SetOnPoll registers a callback for *every* completed poll, including repeated
// failures. Configure before Run; it must not be replaced while Run is active.
func (p *Poller) SetOnPoll(fn func(DeviceStatus)) { p.onPoll = fn }

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

		p.mu.RLock()
		gen, probe := p.generation, p.recovery
		p.mu.RUnlock()
		if probe != nil {
			state, err := probe(ctx)
			if err != nil || state != "online" {
				p.mu.Lock()
				if p.generation == gen {
					p.status.State = State(state)
					if err != nil {
						p.status.State = StateUnknown
						p.status.LastError = err.Error()
					}
				}
				p.mu.Unlock()
				timer.Reset(p.cfg.Interval)
				continue
			}
		}
		p.cycleMu.Lock()
		ms, err := p.reader.Read(ctx)
		p.mu.RLock()
		valid := gen == p.generation
		p.mu.RUnlock()
		if !valid {
			p.cycleMu.Unlock()
			timer.Reset(p.cfg.Interval)
			continue
		}
		if err != nil {
			failures++
			p.record(failures, err)
		} else {
			failures = 0
			p.mu.Lock()
			if p.generation == gen {
				p.recovery = nil
			}
			p.mu.Unlock()
			if serr := p.sink.Save(ctx, ms); serr != nil {
				p.log.Error("persist failed", "err", serr)
			}
			p.record(0, nil)
		}

		p.cycleMu.Unlock()
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

	if p.onPoll != nil {
		p.onPoll(cur)
	}
	if cur.State != prev {
		p.log.Info("status changed", "from", prev, "to", cur.State)
		if p.onChange != nil {
			p.onChange(cur)
		}
	}
}

// BeginReset rejects reads that started before reset and gates recovery on TCP status.
func (p *Poller) BeginReset(probe func(context.Context) (string, error)) {
	p.cycleMu.Lock()
	defer p.cycleMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	p.recovery = probe
	p.status.State = State("recovering")
	p.status.LastSeen = time.Time{}
}
