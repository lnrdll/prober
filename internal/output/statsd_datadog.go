package output

import (
	"fmt"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/lnrdll/prober/internal/prober"
)

type datadogPublisher struct {
	sender *statsdSender
}

func newDatadogPublisher(cfg Config) (publisher, error) {
	sender, err := newStatsdSender(cfg.Datadog.Value, statsd.WithNamespace("custom_monitoring."))
	if err != nil {
		return nil, err
	}
	return &datadogPublisher{sender: sender}, nil
}

func (p *datadogPublisher) Publish(res prober.Result) error {
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

func (p *datadogPublisher) Close() error { return p.sender.close() }
