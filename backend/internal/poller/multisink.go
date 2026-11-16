package poller

import (
	"context"
	"errors"
)

// MultiSink fans measurements out to several sinks. Every sink is tried even
// if an earlier one fails; the errors are joined.
type MultiSink []Sink

// Save implements Sink.
func (m MultiSink) Save(ctx context.Context, ms []Measurement) error {
	var errs []error
	for _, s := range m {
		if err := s.Save(ctx, ms); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
