package output

import (
	"fmt"

	"github.com/DataDog/datadog-go/v5/statsd"
)

type GCPStatsDPublisher struct {
	client *statsd.Client
}

func init() {
	Register(string(OutputStatsdGCP), ExtensionHook{
		SetupFlags: func(ctx RuntimeContext, registerFlag func(func())) {
			addr := new(string)
			ctx.Set(string(OutputStatsdGCP), addr)
			registerFlag(func() {
				BindStringFlag(string(OutputStatsdGCP), DefaultStatsDAddr, "UDP network address location pointing to local GCP Ops Agent", addr)
			})
		},
		Factory: func(ctx RuntimeContext) (Publisher, error) {
			if !ctx.OutputSelected(OutputStatsdGCP) {
				return nil, nil
			}

			addr := ctx.GetString(string(OutputStatsdGCP))
			if addr == "" {
				return nil, fmt.Errorf("--%s is required when -o %s is set", OutputStatsdGCP, OutputStatsdGCP)
			}

			c, err := statsd.New(addr)
			return &GCPStatsDPublisher{client: c}, err
		},
	})
}

func (p *GCPStatsDPublisher) Publish(res Result) error {
	metricNameStatus := "uptime_prober.status"
	metricNameLatency := "uptime_prober.latency_ms"

	// Mangle metadata labels directly into the metric descriptor string name
	// to trigger your GCP Ops Agent configuration processor regex rules.
	envVal := res.Tags["environment"]
	teamVal := res.Tags["team"]
	if envVal != "" && teamVal != "" {
		metricNameStatus = fmt.Sprintf("uptime_prober.status.env_%s.team_%s", envVal, teamVal)
		metricNameLatency = fmt.Sprintf("uptime_prober.latency_ms.env_%s.team_%s", envVal, teamVal)
	}

	// Route the standard URL tracking as a base tag element boundary
	tagsList := []string{"url:" + res.URL}

	statusMetricVal := 0.0
	if res.Up {
		statusMetricVal = 1.0
	}

	_ = p.client.Gauge(metricNameStatus, statusMetricVal, tagsList, 1.0)
	_ = p.client.Gauge(metricNameLatency, float64(res.LatencyMS), tagsList, 1.0)
	return nil
}

func (p *GCPStatsDPublisher) Close() error { return p.client.Flush() }
