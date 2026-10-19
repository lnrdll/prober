package output

import (
	"errors"
	"fmt"
	"sync"

	"github.com/DataDog/datadog-go/v5/statsd"
)

type DatadogStatsDPublisher struct {
	sender *statsdSender
}

// statsdSender wraps a StatsD client and keeps the errors its background
// sender reports, so the publishers can return them from Publish and Close.
type statsdSender struct {
	client *statsd.Client

	mu   sync.Mutex
	errs []error
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

type statsdGauge struct {
	name  string
	value float64
	tags  []string
}

func init() {
	Register(string(OutputStatsdDatadog), ExtensionHook{
		SetupFlags: func(ctx RuntimeContext, registerFlag func(func())) {
			addr := new(string)
			ctx.Set(string(OutputStatsdDatadog), addr)
			registerFlag(func() {
				BindStringFlag(string(OutputStatsdDatadog), DefaultStatsDAddr, "UDP network address location pointing to local Datadog daemon", addr)
			})
		},
		Factory: func(ctx RuntimeContext) (Publisher, error) {
			if !ctx.OutputSelected(OutputStatsdDatadog) {
				return nil, nil
			}

			addr := ctx.GetString(string(OutputStatsdDatadog))
			if addr == "" {
				return nil, fmt.Errorf("--%s is required when -o %s is set", OutputStatsdDatadog, OutputStatsdDatadog)
			}

			sender, err := newStatsdSender(addr, statsd.WithNamespace("custom_monitoring."))
			if err != nil {
				return nil, err
			}
			return &DatadogStatsDPublisher{sender: sender}, nil
		},
	})
}

func (p *DatadogStatsDPublisher) Publish(res Result) error {
	// Datadog natively treats metadata as a flat array of key:value string tags
	tagsList := []string{"url:" + res.URL}
	for k, v := range res.Tags {
		tagsList = append(tagsList, fmt.Sprintf("%s:%s", k, v))
	}

	statusMetricVal := 0.0
	if res.Up {
		statusMetricVal = 1.0
	}

	gauges := []statsdGauge{
		{name: "uptime_prober.status", value: statusMetricVal, tags: tagsList},
		{name: "uptime_prober.latency_ms", value: float64(res.LatencyMS), tags: tagsList},
	}
	if res.SSLDaysLeft > 0 {
		gauges = append(gauges, statsdGauge{name: "uptime_prober.ssl_days_left", value: float64(res.SSLDaysLeft), tags: tagsList})
	}
	return p.sender.gauges(gauges...)
}

func (p *DatadogStatsDPublisher) Close() error { return p.sender.close() }
