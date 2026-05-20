package output

import (
	"fmt"

	"github.com/DataDog/datadog-go/v5/statsd"
)

type DatadogStatsDPublisher struct {
	client *statsd.Client
}

func init() {
	Register("datadog_statsd", ExtensionHook{
		SetupFlags: func(ctx RuntimeContext, registerFlag func(func())) {
			addr := new(string)
			ctx.Set("datadog_statsd_addr", addr)
			registerFlag(func() {
				BindStringFlag("datadog-statsd", "", "UDP network address location pointing to local Datadog daemon", addr)
			})
		},
		Factory: func(ctx RuntimeContext) (Publisher, error) {
			addr := ctx.GetString("datadog_statsd_addr")
			if addr == "" {
				return nil, nil
			}

			c, err := statsd.New(addr, statsd.WithNamespace("custom_monitoring."))
			return &DatadogStatsDPublisher{client: c}, err
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

	_ = p.client.Gauge("uptime_prober.status", statusMetricVal, tagsList, 1.0)
	_ = p.client.Gauge("uptime_prober.latency_ms", float64(res.LatencyMS), tagsList, 1.0)
	if res.SSLDaysLeft > 0 {
		_ = p.client.Gauge("uptime_prober.ssl_days_left", float64(res.SSLDaysLeft), tagsList, 1.0)
	}
	return nil
}

func (p *DatadogStatsDPublisher) Close() error { return p.client.Flush() }
