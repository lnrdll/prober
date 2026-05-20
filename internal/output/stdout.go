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
	Register("stdout", ExtensionHook{
		SetupFlags: func(ctx RuntimeContext, registerFlag func(func())) {
			f := new(bool)
			ctx.Set("enable_stdout", f)
			registerFlag(func() { BindBoolFlag("stdout", true, "Output structured JSON log lines directly to stdout", f) })
		},
		Factory: func(ctx RuntimeContext) (Publisher, error) {
			if !ctx.GetBool("enable_stdout") {
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
