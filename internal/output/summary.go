package output

import (
	"fmt"
	"io"
	"os"
	"time"
)

type Summary struct {
	Total          int
	Passed         int
	Failed         int
	Skipped        int
	Duration       time.Duration
	Results        []Result
	SkippedResults []Result
}

type SummaryPublisher interface {
	PublishSummary(summary Summary) error
}

type SummaryOutput struct {
	writer io.Writer
}

func init() {
	Register(string(OutputSummary), ExtensionHook{
		Factory: func(ctx RuntimeContext) (Publisher, error) {
			if !ctx.OutputSelected(OutputSummary) {
				return nil, nil
			}

			writer := ctx.GetWriter(StdoutWriterKey)
			if writer == nil {
				writer = os.Stdout
			}

			return &SummaryOutput{writer: writer}, nil
		},
	})
}

func (s *SummaryOutput) Publish(Result) error { return nil }

func (s *SummaryOutput) PublishSummary(summary Summary) error {
	_, err := fmt.Fprintf(s.writer, "Summary: total=%d passed=%d failed=%d skipped=%d duration=%s\n", summary.Total, summary.Passed, summary.Failed, summary.Skipped, summary.Duration.Round(time.Millisecond))
	if err != nil {
		return err
	}

	for _, res := range summary.Results {
		if res.Up {
			continue
		}

		reason := failureReason(res)

		if _, err := fmt.Fprintf(s.writer, "FAILED %s status=%d reason=%s\n", res.URL, res.Status, reason); err != nil {
			return err
		}
	}

	return nil
}

func (s *SummaryOutput) Close() error { return nil }

func failureReason(res Result) string {
	reason := res.Error
	if reason == "" {
		reason = res.FailedAssertion
	}
	if reason == "" {
		reason = "unknown failure"
	}

	return reason
}
