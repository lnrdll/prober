package output

import (
	"context"
	"log/slog"
	"os"
)

type StdoutPublisher struct {
	logger *slog.Logger
}

func init() {
	Register(string(OutputStdout), ExtensionHook{
		Factory: func(ctx RuntimeContext) (Publisher, error) {
			if len(ctx.GetStrings(string(SelectionKey))) > 0 && !ctx.OutputSelected(OutputStdout) {
				return nil, nil
			}
			return &StdoutPublisher{logger: slog.New(slog.NewJSONHandler(os.Stdout, nil))}, nil
		},
	})
}

func (p *StdoutPublisher) Publish(res Result) error {
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

func (p *StdoutPublisher) Close() error { return nil }
