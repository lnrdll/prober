package output

import (
	"context"
	"log/slog"
)

type stdoutPublisher struct {
	logger *slog.Logger
}

func newStdoutPublisher(cfg Config) (publisher, error) {
	return &stdoutPublisher{logger: slog.New(slog.NewJSONHandler(cfg.Writer, nil))}, nil
}

func (p *stdoutPublisher) Publish(res Result) error {
	logAttrs := []slog.Attr{
		slog.String("url", res.URL), slog.Bool("up", res.Up), slog.Int("status", res.Status), slog.Int64("latency_ms", res.LatencyMS),
	}
	for k, v := range res.Tags {
		logAttrs = append(logAttrs, slog.String(k, v))
	}
	if res.FailedAssertion != "" {
		logAttrs = append(logAttrs, slog.String("failed_assertion", res.FailedAssertion))
	}
	if res.Error != "" {
		logAttrs = append(logAttrs, slog.String("error", res.Error))
	}

	if res.Up {
		p.logger.LogAttrs(context.Background(), slog.LevelInfo, "Uptime check passed", logAttrs...)
	} else {
		p.logger.LogAttrs(context.Background(), slog.LevelError, "Uptime check failed", logAttrs...)
	}
	return nil
}

func (p *stdoutPublisher) Close() error { return nil }
