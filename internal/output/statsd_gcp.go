package output

import "fmt"

type gcpPublisher struct {
	sender *statsdSender
}

func newGCPPublisher(cfg Config) (publisher, error) {
	sender, err := newStatsdSender(cfg.GCP.Value)
	if err != nil {
		return nil, err
	}
	return &gcpPublisher{sender: sender}, nil
}

func (p *gcpPublisher) Publish(res Result) error {
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

	return p.sender.gauges(
		statsdGauge{name: metricNameStatus, value: statusMetricVal, tags: tagsList},
		statsdGauge{name: metricNameLatency, value: float64(res.LatencyMS), tags: tagsList},
	)
}

func (p *gcpPublisher) Close() error { return p.sender.close() }
