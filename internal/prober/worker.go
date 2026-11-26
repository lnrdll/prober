package prober

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// DefaultMaxJitter is the upper bound for random stagger before each target probe.
	DefaultMaxJitter = 1500 * time.Millisecond
	// DefaultConcurrency is the default maximum number of simultaneous target probes.
	DefaultConcurrency = 64
)

var (
	errSSRFBlocked = errors.New("SSRF Alert: Request to Cloud Metadata Server explicitly blocked")

	blockedMetadataIPs = []netip.Addr{
		netip.MustParseAddr("169.254.169.254"),
		netip.MustParseAddr("169.254.170.2"),
		netip.MustParseAddr("100.100.100.200"),
		netip.MustParseAddr("fd00:ec2::254"),
	}
)

// Runner executes a prepared Plan with configurable transport, concurrency,
// stagger jitter, and clock. Its zero value uses production defaults.
type Runner struct {
	// Transport overrides the base HTTP transport. When nil, Runner builds
	// pooled transports with SSRF dial protection.
	Transport http.RoundTripper
	// MaxConcurrency caps simultaneous probes. Values <= 0 use DefaultConcurrency.
	MaxConcurrency int
	// Jitter returns the stagger delay before a probe starts. When nil, Runner
	// uses a random delay in [0, DefaultMaxJitter).
	Jitter func() time.Duration
	// Now returns the current time. When nil, time.Now is used.
	Now func() time.Time
}

// Execute probes every enabled target with default Runner settings and returns
// the summary. It calls publish once per result as each probe finishes, always
// from the calling goroutine, so calls within one Execute never overlap.
// Skipped targets are not published.
func Execute(ctx context.Context, plan Plan, publish func(Result)) Summary {
	return (Runner{}).Execute(ctx, plan, publish)
}

// Execute probes every enabled target in plan and returns the summary. It calls
// publish sequentially from the calling goroutine as probes finish. Skipped
// targets are not published.
func (r Runner) Execute(ctx context.Context, plan Plan, publish func(Result)) Summary {
	if ctx == nil {
		ctx = context.Background()
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	jitter := r.Jitter
	if jitter == nil {
		jitter = func() time.Duration {
			return time.Duration(rand.IntN(int(DefaultMaxJitter)))
		}
	}

	start := now()
	summary := Summary{Total: len(plan.targets)}

	enabled := make([]preparedTarget, 0, len(plan.targets))
	for _, target := range plan.targets {
		if target.disabled {
			summary.Skipped++
			summary.SkippedResults = append(summary.SkippedResults, Result{
				Name:      target.name,
				URL:       target.url,
				Method:    target.method,
				Timestamp: now(),
				Tags:      target.tags,
			})
			continue
		}
		enabled = append(enabled, target)
	}

	if len(enabled) == 0 {
		summary.Duration = now().Sub(start)
		return summary
	}

	secureClient, insecureClient, cleanup := r.buildClients()
	defer cleanup()

	workers := r.MaxConcurrency
	if workers <= 0 {
		workers = DefaultConcurrency
	}
	if workers > len(enabled) {
		workers = len(enabled)
	}

	jobs := make(chan preparedTarget, len(enabled))
	for _, target := range enabled {
		jobs <- target
	}
	close(jobs)

	resultsChan := make(chan Result, len(enabled))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				resultsChan <- runTarget(ctx, t, secureClient, insecureClient, now, jitter)
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	for res := range resultsChan {
		summary.Results = append(summary.Results, res)
		if res.Up {
			summary.Passed++
		} else {
			summary.Failed++
		}
		if publish != nil {
			publish(res)
		}
	}

	summary.Duration = now().Sub(start)
	return summary
}

func (r Runner) buildClients() (secureClient, insecureClient *http.Client, cleanup func()) {
	if r.Transport == nil {
		secureTransport := newBaseTransport(false)
		insecureTransport := newBaseTransport(true)
		return &http.Client{
			CheckRedirect: noRedirect,
			Transport:     secureTransport,
		}, &http.Client{
			CheckRedirect: noRedirect,
			Transport:     insecureTransport,
		}, func() {
			secureTransport.CloseIdleConnections()
			insecureTransport.CloseIdleConnections()
		}
	}

	secureClient = &http.Client{
		CheckRedirect: noRedirect,
		Transport:     r.Transport,
	}
	if tr, ok := r.Transport.(*http.Transport); ok {
		insecureTransport := tr.Clone()
		if insecureTransport.TLSClientConfig != nil {
			insecureTransport.TLSClientConfig = insecureTransport.TLSClientConfig.Clone()
		} else {
			insecureTransport.TLSClientConfig = &tls.Config{}
		}
		insecureTransport.TLSClientConfig.InsecureSkipVerify = true
		insecureClient = &http.Client{
			CheckRedirect: noRedirect,
			Transport:     insecureTransport,
		}
		return secureClient, insecureClient, insecureTransport.CloseIdleConnections
	}

	return secureClient, secureClient, func() {}
}

func newBaseTransport(insecure bool) *http.Transport {
	tr := &http.Transport{
		DialContext:         newSafeDialer().DialContext,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return tr
}

func newSafeDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   blockMetadataDial,
	}
}

func noRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func runTarget(
	ctx context.Context,
	t preparedTarget,
	secureClient *http.Client,
	insecureClient *http.Client,
	now func() time.Time,
	jitter func() time.Duration,
) Result {
	if err := sleepContext(ctx, jitter()); err != nil {
		return Result{
			Name:      t.name,
			URL:       t.url,
			Method:    t.method,
			Timestamp: now(),
			Tags:      t.tags,
			Error:     err.Error(),
		}
	}

	client := secureClient
	if t.sslSkipVerify {
		client = insecureClient
	}
	return probeTarget(ctx, t, client, now)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func probeTarget(ctx context.Context, t preparedTarget, client *http.Client, now func() time.Time) Result {
	var lastResult Result
	for range t.attempts {
		lastResult = probeTargetOnce(ctx, t, client, now)
		if lastResult.Up {
			return lastResult
		}
		if ctx.Err() != nil {
			return lastResult
		}
		if lastResult.Error != "" && isNonRetryableProbeError(lastResult.Error) {
			return lastResult
		}
	}

	return lastResult
}

func probeTargetOnce(ctx context.Context, t preparedTarget, client *http.Client, now func() time.Time) Result {
	res := Result{
		Name:      t.name,
		URL:       t.url,
		Method:    t.method,
		Timestamp: now(),
		Tags:      t.tags,
	}

	if isBlockedMetadataHost(t.parsedURL) {
		res.Error = errSSRFBlocked.Error()
		return res
	}

	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, t.method, t.url, bytes.NewBufferString(t.body))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "prober")

	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	// Intercept Host from headers to update transport settings and SNI records safely.
	if hostOverride := req.Header.Get("Host"); hostOverride != "" {
		req.Host = hostOverride
		if transport, ok := client.Transport.(*http.Transport); ok {
			clonedTransport := transport.Clone()
			defer clonedTransport.CloseIdleConnections()
			if clonedTransport.TLSClientConfig != nil {
				clonedTransport.TLSClientConfig = clonedTransport.TLSClientConfig.Clone()
			} else {
				clonedTransport.TLSClientConfig = &tls.Config{}
			}
			clonedTransport.TLSClientConfig.ServerName = extractServerName(hostOverride)

			clonedClient := *client
			clonedClient.Transport = clonedTransport
			client = &clonedClient
		}
	}

	start := now()
	resp, err := client.Do(req)
	res.LatencyMS = now().Sub(start).Milliseconds()

	if err != nil {
		if errors.Is(err, errSSRFBlocked) {
			res.Error = errSSRFBlocked.Error()
		} else {
			res.Error = err.Error()
		}
		return res
	}
	defer func() { _ = resp.Body.Close() }()

	res.Status = resp.StatusCode

	// Read and truncate body to avoid logging enormous stack traces.
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		res.Error = err.Error()
		return res
	}
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
		res.SSLDaysLeft = int(cert.NotAfter.Sub(now()).Hours() / 24)
		issuerStr = cert.Issuer.String()
		subjectStr = cert.Subject.String()
		dnsNamesStr = strings.Join(cert.DNSNames, ",")
	}
	res.SSLIssuer = issuerStr
	res.SSLSubject = subjectStr
	res.SSLDNSNames = dnsNamesStr

	evalCtx := evalContext{
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
	for _, a := range t.assertions {
		passed, err := a.evaluate(evalCtx)
		if err != nil || !passed {
			res.Up = false
			res.FailedAssertion = a.text
			break
		}
	}

	return res
}

func isBlockedMetadataHost(parsedURL *url.URL) bool {
	if parsedURL == nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsedURL.Hostname()), ".")
	if host == "metadata.google.internal" || host == "metadata.goog" {
		return true
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return isBlockedMetadataIP(addr)
	}
	return false
}

func blockMetadataDial(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil
	}
	if isBlockedMetadataIP(ap.Addr()) {
		return errSSRFBlocked
	}
	return nil
}

func isBlockedMetadataIP(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	for _, blocked := range blockedMetadataIPs {
		if addr == blocked {
			return true
		}
	}
	return false
}

func extractServerName(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.Trim(host, "[]")
}

func isNonRetryableProbeError(errMsg string) bool {
	return strings.Contains(errMsg, "SSRF Alert")
}
