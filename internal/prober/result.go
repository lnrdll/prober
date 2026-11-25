package prober

import "time"

// Result holds the outcome of probing one target.
type Result struct {
	Name            string
	URL             string
	Method          string
	Up              bool
	Status          int
	LatencyMS       int64
	SSLDaysLeft     int
	SSLIssuer       string
	SSLSubject      string
	SSLDNSNames     string
	FailedAssertion string
	Error           string
	Body            string
	Headers         map[string]string
	Timestamp       time.Time
	Tags            map[string]string
}

// Summary aggregates the results of a completed Execute call.
type Summary struct {
	Total          int
	Passed         int
	Failed         int
	Skipped        int
	Duration       time.Duration
	Results        []Result
	SkippedResults []Result
}
