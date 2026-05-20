package prober

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lnrdll/prober/internal/config"
	"github.com/lnrdll/prober/internal/output"
)

const defaultTargetTimeoutSeconds = 60

type Summary struct {
	Total    int
	Passed   int
	Failed   int
	Skipped  int
	Duration time.Duration
	Results  []output.Result
}

func Execute(targets []config.Target, publishers []output.Publisher) Summary {
	start := time.Now()
	summary := Summary{Total: len(targets)}
	// 1. Maintain precisely two global reusable transports for connection pooling
	secureClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Avoid open redirect token leaks
		},
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	insecureClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	resultsChan := make(chan output.Result, len(targets))
	var wg sync.WaitGroup

	// 2. Parallel network probing executions
	for _, target := range targets {
		if target.Disabled {
			summary.Skipped++
			continue // Skip muted entries seamlessly
		}

		wg.Add(1)
		go func(t config.Target) {
			defer wg.Done()

			// Introduce randomized stagger jitter (0 to 1500ms) to prevent thundering herds
			time.Sleep(time.Duration(rand.Intn(1500)) * time.Millisecond)

			client := secureClient
			if t.SSLSkipVerify {
				client = insecureClient
			}

			resultsChan <- probeTarget(t, client) // Non-blocking write to channel
		}(target)
	}

	// 3. Monitor completions in background thread to close channel safely
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// 4. Sequential asynchronous output routing (prevents disk lock contention)
	for res := range resultsChan {
		summary.Results = append(summary.Results, res)
		if res.Up {
			summary.Passed++
		} else {
			summary.Failed++
		}
		for _, pub := range publishers {
			_ = pub.Publish(res)
		}
	}

	summary.Duration = time.Since(start)
	return summary
}

func probeTarget(t config.Target, client *http.Client) output.Result {
	attempts := t.Retries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastResult output.Result
	for attempt := 0; attempt < attempts; attempt++ {
		lastResult = probeTargetOnce(t, client)
		if lastResult.Up {
			return lastResult
		}
		if lastResult.Error != "" && isNonRetryableProbeError(lastResult.Error) {
			return lastResult
		}
	}

	return lastResult
}

func probeTargetOnce(t config.Target, client *http.Client) output.Result {
	res := output.Result{
		URL:       t.URL,
		Method:    t.Method,
		Timestamp: time.Now(),
		Tags:      t.Tags,
	}
	if res.Method == "" {
		res.Method = http.MethodGet
	}

	// Security Guardrail: Block Server-Side Request Forgery (SSRF) against Cloud Metadata Endpoints
	parsedURL, err := url.Parse(t.URL)
	if err == nil {
		hostStr := strings.ToLower(parsedURL.Hostname())
		if hostStr == "169.254.169.254" || hostStr == "metadata.google.internal" {
			res.Error = "SSRF Alert: Request to Cloud Metadata Server explicitly blocked"
			return res
		}
	}

	req, err := http.NewRequest(res.Method, t.URL, bytes.NewBufferString(t.Body))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "prober")

	ctx, cancel := context.WithTimeout(context.Background(), targetTimeout(t))
	defer cancel()
	req = req.WithContext(ctx)

	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	// Intercept Host from headers to update transport settings and SNI records safely
	if hostOverride := req.Header.Get("Host"); hostOverride != "" {
		req.Host = hostOverride
		if transport, ok := client.Transport.(*http.Transport); ok {
			clonedTransport := transport.Clone()
			if clonedTransport.TLSClientConfig != nil {
				clonedTransport.TLSClientConfig = clonedTransport.TLSClientConfig.Clone()
			} else {
				clonedTransport.TLSClientConfig = &tls.Config{}
			}
			clonedTransport.TLSClientConfig.ServerName = hostOverride

			clonedClient := *client
			clonedClient.Transport = clonedTransport
			client = &clonedClient
		}
	}

	start := time.Now()
	resp, err := client.Do(req)
	res.LatencyMS = time.Since(start).Milliseconds()

	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer func() { _ = resp.Body.Close() }()

	res.Status = resp.StatusCode

	// Read and truncate body sharply to avoid logging enormous stack traces / leaking massive PII payload blobs
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	respStr := string(bodyBytes)

	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	res.Body = respStr
	res.Headers = respHeaders

	var issuerStr, subjectStr, dnsNamesStr string
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		res.SSLDaysLeft = int(time.Until(cert.NotAfter).Hours() / 24)
		issuerStr = cert.Issuer.String()
		subjectStr = cert.Subject.String()
		dnsNamesStr = strings.Join(cert.DNSNames, ",")
	}
	res.SSLIssuer = issuerStr
	res.SSLSubject = subjectStr
	res.SSLDNSNames = dnsNamesStr

	evalCtx := EvalContext{
		Status:      resp.StatusCode,
		Body:        respStr,
		LatencyMS:   res.LatencyMS,
		SSLDaysLeft: res.SSLDaysLeft,
		SSLIssuer:   issuerStr,
		SSLSubject:  subjectStr,
		SSLDNSNames: dnsNamesStr,
		Headers:     respHeaders,
	}

	res.Up = true
	// High Performance: Use precompiled execution references (Zero compilation at check runtime)
	for idx, prog := range t.CompiledCel {
		passed, err := EvaluatePrecompiled(prog, evalCtx)
		if err != nil || !passed {
			res.Up = false
			res.FailedAssertion = t.Assertions[idx]
			break
		}
	}

	return res
}

func isNonRetryableProbeError(errMsg string) bool {
	return strings.Contains(errMsg, "SSRF Alert") || strings.Contains(errMsg, "missing protocol scheme")
}

func targetTimeout(t config.Target) time.Duration {
	if t.Timeout <= 0 {
		return defaultTargetTimeoutSeconds * time.Second
	}

	return time.Duration(t.Timeout) * time.Second
}
