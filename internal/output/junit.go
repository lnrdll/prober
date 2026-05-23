package output

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type JUnitOutput struct {
	path string
	err  error
}

type junitTestSuite struct {
	XMLName   xml.Name        `xml:"testsuite"`
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      string          `xml:"time,attr"`
	Timestamp string          `xml:"timestamp,attr,omitempty"`
	TestCases []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr,omitempty"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

func init() {
	Register(string(OutputJunit), ExtensionHook{
		SetupFlags: func(ctx RuntimeContext, registerFlag func(func())) {
			path := new(string)
			ctx.Set(string(OutputJunit), path)
			registerFlag(func() {
				BindStringFlag(string(OutputJunit), "", "Write a JUnit XML report to a file", path)
			})
		},
		Factory: func(ctx RuntimeContext) (Publisher, error) {
			if !ctx.OutputSelected(OutputJunit) {
				return nil, nil
			}

			path := ctx.GetString(string(OutputJunit))
			if path == "" {
				return nil, fmt.Errorf("--%s is required when -o %s is set", OutputJunit, OutputJunit)
			}

			return &JUnitOutput{path: path}, nil
		},
	})
}

func (j *JUnitOutput) Publish(Result) error { return nil }

func (j *JUnitOutput) PublishSummary(summary Summary) error {
	if err := os.MkdirAll(filepath.Dir(j.path), 0o755); err != nil {
		j.err = err
		return err
	}

	report := junitTestSuite{
		Name:      "prober",
		Tests:     len(summary.Results) + len(summary.SkippedResults),
		Failures:  summary.Failed,
		Skipped:   len(summary.SkippedResults),
		Time:      formatJUnitDuration(summary.Duration),
		Timestamp: earliestTimestamp(summary.Results, summary.SkippedResults),
		TestCases: make([]junitTestCase, 0, len(summary.Results)+len(summary.SkippedResults)),
	}

	for _, res := range summary.SkippedResults {
		report.TestCases = append(report.TestCases, junitTestCase{
			Name:      junitCaseName(res),
			ClassName: "prober",
			Time:      formatJUnitDuration(0),
			Skipped:   &junitSkipped{Message: "target disabled"},
		})
	}

	for _, res := range summary.Results {
		testCase := junitTestCase{
			Name:      junitCaseName(res),
			ClassName: "prober",
			Time:      formatJUnitDuration(time.Duration(res.LatencyMS) * time.Millisecond),
		}

		if !res.Up {
			reason := failureReason(res)
			testCase.Failure = &junitFailure{
				Message: reason,
				Type:    junitFailureType(res),
				Body:    reason,
			}
		}

		if res.Body != "" {
			testCase.SystemOut = res.Body
		}

		report.TestCases = append(report.TestCases, testCase)
	}

	data, err := xml.MarshalIndent(report, "", "  ")
	if err != nil {
		j.err = err
		return err
	}

	data = append([]byte(xml.Header), append(data, '\n')...)
	j.err = os.WriteFile(j.path, data, 0o644)
	return j.err
}

func (j *JUnitOutput) Close() error { return j.err }

func formatJUnitDuration(duration time.Duration) string {
	return strconv.FormatFloat(duration.Seconds(), 'f', 3, 64)
}

func earliestTimestamp(resultSets ...[]Result) string {
	var earliest time.Time
	for _, results := range resultSets {
		for _, res := range results {
			if res.Timestamp.IsZero() {
				continue
			}
			if earliest.IsZero() || res.Timestamp.Before(earliest) {
				earliest = res.Timestamp
			}
		}
	}

	if earliest.IsZero() {
		return ""
	}

	return earliest.Format(time.RFC3339)
}

func junitCaseName(res Result) string {
	if res.Name != "" {
		return res.Name
	}

	return res.URL
}

func junitFailureType(res Result) string {
	if res.Error != "" {
		return "error"
	}

	return "assertion"
}
