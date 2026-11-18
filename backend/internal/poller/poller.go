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

// New builds a poller. onChange (optional) is called on state changes, including
// reset transitions. Callbacks are serialized and must not reenter BeginReset.
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

	timer := time.NewTimer(0) // first poll immediately
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		failures := p.poll(ctx)
		next := p.cfg.Interval
		if failures > 0 {
			if d := p.cfg.Backoff.Delay(failures); d > next {
				next = d
			}
		}
		timer.Reset(next)
	}
}

func (p *Poller) poll(ctx context.Context) int {
	p.cycleMu.Lock()
	p.mu.RLock()
	gen, probe, status := p.generation, p.recovery, p.status
	p.mu.RUnlock()
	p.cycleMu.Unlock()
	if probe != nil {
		state, err := probe(ctx)
		if err != nil || state != "online" {
			p.cycleMu.Lock()
			defer p.cycleMu.Unlock()
			if p.current(gen) && ctx.Err() == nil {
				status.State, status.LastError = State(state), ""
				if err != nil {
					status.State, status.LastError = StateUnknown, err.Error()
				}
				p.publish(status)
			}
			return 0
		}
	}
	if !p.current(gen) || ctx.Err() != nil {
		return 0
	}

	// Only commit observable results after confirming this read still belongs
	// to the active generation. Device I/O must not delay reset invalidation.
	var measurements []Measurement
	var commit func() []Measurement
	var err error
	if staged, ok := p.reader.(interface {
		readStaged(context.Context) (func() []Measurement, error)
	}); ok {
		commit, err = staged.readStaged(ctx)
	} else {
		measurements, err = p.reader.Read(ctx)
	}
	p.cycleMu.Lock()
	defer p.cycleMu.Unlock()
	if !p.current(gen) || ctx.Err() != nil {
		return 0
	}
	if err != nil {
		p.invalidateReader()
		status.ConsecutiveFailures++
		status.State, status.LastError = StateDegraded, err.Error()
		if status.ConsecutiveFailures >= p.cfg.OfflineAfter {
			status.State = StateOffline
		}
		p.publish(status)
		return status.ConsecutiveFailures
	}
	if commit != nil {
		measurements = commit()
	}
	if err := p.sink.Save(ctx, measurements); err != nil {
		p.log.Error("persist failed", "err", err)
	}
	p.mu.Lock()
	p.recovery = nil
	p.mu.Unlock()
	p.publish(DeviceStatus{Name: status.Name, State: StateOnline, LastSeen: time.Now()})
	return 0
}

func (p *Poller) current(gen uint64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return gen == p.generation
}

func (p *Poller) invalidateReader() {
	if reader, ok := p.reader.(interface{ Invalidate() }); ok {
		reader.Invalidate()
	}
}

// publish runs under cycleMu so callbacks see transitions in commit order.
func (p *Poller) publish(cur DeviceStatus) {
	p.mu.Lock()
	prev := p.status.State
	p.status = cur
	p.mu.Unlock()

	if cur.State != prev {
		p.log.Info("status changed", "from", prev, "to", cur.State)
		if p.onChange != nil {
			p.onChange(cur)
		}
	}
	if p.onPoll != nil {
		p.onPoll(cur)
	}
}

// BeginReset rejects reads that started before reset and gates recovery on TCP status.
func (p *Poller) BeginReset(probe func(context.Context) (string, error)) {
	p.cycleMu.Lock()
	defer p.cycleMu.Unlock()
	p.mu.Lock()
	p.generation++
	p.recovery = probe
	p.mu.Unlock()
	p.invalidateReader()
	p.publish(DeviceStatus{Name: p.cfg.Name, State: StateRecovering})
}
