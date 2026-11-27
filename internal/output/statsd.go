package output

import (
	"errors"
	"fmt"
	"sync"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/lnrdll/prober/internal/prober"
)

// statsdPublisher wraps a StatsD client, formats probe results into gauges,
// and keeps the errors its background sender reports so Publish and Close can
// return them.
type statsdPublisher struct {
	client      *statsd.Client
	buildGauges func(prober.Result) []statsdGauge

	mu   sync.Mutex
	errs []error
}

type statsdGauge struct {
	name  string
	value float64
	tags  []string
}

func newStatsdPublisher(cfg Config, settingKey Name, buildGauges func(prober.Result) []statsdGauge, options ...statsd.Option) (publisher, error) {
	p := &statsdPublisher{buildGauges: buildGauges}
	c, err := statsd.New(cfg.Settings[settingKey].Value, append(options, statsd.WithErrorHandler(p.recordError))...)
	if err != nil {
		return nil, err
	}
	p.client = c
	return p, nil
}

func newDatadogPublisher(cfg Config) (publisher, error) {
	return newStatsdPublisher(cfg, outputStatsdDatadog, datadogGauges, statsd.WithNamespace("custom_monitoring."))
}

func newGCPPublisher(cfg Config) (publisher, error) {
	return newStatsdPublisher(cfg, outputStatsdGCP, gcpGauges)
}

func (p *statsdPublisher) Publish(res prober.Result) error {
	gauges := p.buildGauges(res)
	errs := make([]error, 0, len(gauges)+1)
	for _, g := range gauges {
		errs = append(errs, p.client.Gauge(g.name, g.value, g.tags, 1.0))
	}
	errs = append(errs, p.takeErrors())
	return errors.Join(errs...)
}

func (p *statsdPublisher) Close() error {
	err := p.client.Close()
	return errors.Join(err, p.takeErrors())
}

// recordError runs on the client's background goroutines.
func (p *statsdPublisher) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs = append(p.errs, err)
}

func (p *statsdPublisher) takeErrors() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	err := errors.Join(p.errs...)
	p.errs = nil
	return err
}

func statusGaugeValue(up bool) float64 {
	if up {
		return 1.0
	}
	return 0.0
}

func datadogGauges(res prober.Result) []statsdGauge {
	tagsList := make([]string, 0, len(res.Tags)+1)
	tagsList = append(tagsList, "url:"+res.URL)
	for k, v := range res.Tags {
		tagsList = append(tagsList, fmt.Sprintf("%s:%s", k, v))
	}

	gauges := []statsdGauge{
		{name: "uptime_prober.status", value: statusGaugeValue(res.Up), tags: tagsList},
		{name: "uptime_prober.latency_ms", value: float64(res.LatencyMS), tags: tagsList},
	}
	if res.SSLDaysLeft > 0 {
		gauges = append(gauges, statsdGauge{name: "uptime_prober.ssl_days_left", value: float64(res.SSLDaysLeft), tags: tagsList})
	}
	return gauges
}

func gcpGauges(res prober.Result) []statsdGauge {
	metricNameStatus := "uptime_prober.status"
	metricNameLatency := "uptime_prober.latency_ms"

	// Embed metadata labels into the metric name to match GCP Ops Agent processor regex rules.
	envVal := res.Tags["environment"]
	teamVal := res.Tags["team"]
	if envVal != "" && teamVal != "" {
		metricNameStatus = fmt.Sprintf("uptime_prober.status.env_%s.team_%s", envVal, teamVal)
		metricNameLatency = fmt.Sprintf("uptime_prober.latency_ms.env_%s.team_%s", envVal, teamVal)
	}

	tagsList := []string{"url:" + res.URL}
	return []statsdGauge{
		{name: metricNameStatus, value: statusGaugeValue(res.Up), tags: tagsList},
		{name: metricNameLatency, value: float64(res.LatencyMS), tags: tagsList},
	}
}
