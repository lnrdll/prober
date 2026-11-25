package output

import (
	"errors"
	"sync"

	"github.com/DataDog/datadog-go/v5/statsd"
)

// statsdSender wraps a StatsD client and keeps the errors its background
// sender reports, so the publishers can return them from Publish and Close.
type statsdSender struct {
	client *statsd.Client

	mu   sync.Mutex
	errs []error
}

type statsdGauge struct {
	name  string
	value float64
	tags  []string
}

func newStatsdSender(addr string, options ...statsd.Option) (*statsdSender, error) {
	s := &statsdSender{}
	c, err := statsd.New(addr, append(options, statsd.WithErrorHandler(s.recordError))...)
	if err != nil {
		return nil, err
	}
	s.client = c
	return s, nil
}

// recordError runs on the client's background goroutines.
func (s *statsdSender) recordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, err)
}

func (s *statsdSender) takeErrors() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := errors.Join(s.errs...)
	s.errs = nil
	return err
}

func (s *statsdSender) gauges(gauges ...statsdGauge) error {
	// A closed client drops metrics without an error, so check it here.
	if s.client.IsClosed() {
		return errors.New("statsd client is closed")
	}

	errs := make([]error, 0, len(gauges)+1)
	for _, g := range gauges {
		errs = append(errs, s.client.Gauge(g.name, g.value, g.tags, 1.0))
	}
	errs = append(errs, s.takeErrors())
	return errors.Join(errs...)
}

func (s *statsdSender) close() error {
	err := s.client.Close()
	return errors.Join(err, s.takeErrors())
}
