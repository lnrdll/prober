package prober

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
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
	errSSRFBlocked         = errors.New("SSRF Alert: Request to Cloud Metadata Server explicitly blocked")
	errNonRetryableRequest = errors.New("non-retryable request")

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

	lookupIP func(ctx context.Context, host string) ([]netip.Addr, error)
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

	clients, cleanup := r.buildClients(enabled)
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
				resultsChan <- runTarget(ctx, t, clients[targetClientKey(t)], now, jitter)
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

type clientKey struct {
	sslSkipVerify bool
	serverName    string
}

func targetHostOverride(headers map[string]string) string {
	var matchedKey, matchedVal string
	for k, v := range headers {
		if strings.EqualFold(k, "Host") && v != "" {
			if matchedKey == "" || k < matchedKey {
				matchedKey = k
				matchedVal = v
			}
		}
	}
	return matchedVal
}

func targetClientKey(t preparedTarget) clientKey {
	key := clientKey{sslSkipVerify: t.sslSkipVerify}
	if t.parsedURL != nil && strings.EqualFold(t.parsedURL.Scheme, "https") {
		if hostOverride := targetHostOverride(t.headers); hostOverride != "" {
			key.serverName = extractServerName(hostOverride)
		}
	}
	return key
}

func (r Runner) buildClients(targets []preparedTarget) (map[clientKey]*http.Client, func()) {
	clients := make(map[clientKey]*http.Client)
	var ownedTransports []*http.Transport
	cleanup := func() {
		for _, tr := range ownedTransports {
			tr.CloseIdleConnections()
		}
	}

	if r.Transport != nil {
		baseTransport, isHTTPTransport := r.Transport.(*http.Transport)
		if !isHTTPTransport {
			shared := &http.Client{
				CheckRedirect: noRedirect,
				Transport:     r.Transport,
			}
			for _, t := range targets {
				clients[targetClientKey(t)] = shared
			}
			return clients, cleanup
		}

		for _, t := range targets {
			key := targetClientKey(t)
			if _, exists := clients[key]; exists {
				continue
			}
			if !key.sslSkipVerify && key.serverName == "" {
				clients[key] = &http.Client{
					CheckRedirect: noRedirect,
					Transport:     baseTransport,
				}
				continue
			}
			tr := cloneTransportWithTLS(baseTransport, key)
			ownedTransports = append(ownedTransports, tr)
			clients[key] = &http.Client{
				CheckRedirect: noRedirect,
				Transport:     tr,
			}
		}
		return clients, cleanup
	}

	dialContext := r.newSafeDialContext()
	for _, t := range targets {
		key := targetClientKey(t)
		if _, exists := clients[key]; exists {
			continue
		}
		tr := newBaseTransport(dialContext, key)
		ownedTransports = append(ownedTransports, tr)
		clients[key] = &http.Client{
			CheckRedirect: noRedirect,
			Transport:     tr,
		}
	}
	return clients, cleanup
}

func cloneTransportWithTLS(base *http.Transport, key clientKey) *http.Transport {
	tr := base.Clone()
	if tr.TLSClientConfig != nil {
		tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	} else {
		tr.TLSClientConfig = &tls.Config{}
	}
	if key.sslSkipVerify {
		tr.TLSClientConfig.InsecureSkipVerify = true
	}
	if key.serverName != "" {
		tr.TLSClientConfig.ServerName = key.serverName
	}
	return tr
}

func newBaseTransport(dialContext func(ctx context.Context, network, addr string) (net.Conn, error), key clientKey) *http.Transport {
	tr := &http.Transport{
		DialContext:         dialContext,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	if key.sslSkipVerify || key.serverName != "" {
		tr.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: key.sslSkipVerify,
			ServerName:         key.serverName,
		}
	}
	return tr
}

func (r Runner) newSafeDialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := newSafeDialer()
	lookup := r.lookupIP
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		if ip, err := netip.ParseAddr(host); err == nil {
			if isBlockedMetadataIP(ip) {
				return nil, errSSRFBlocked
			}
			return dialer.DialContext(ctx, network, addr)
		}

		addrs, err := lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}

		for _, ip := range addrs {
			if isBlockedMetadataIP(ip) {
				return nil, errSSRFBlocked
			}
		}

		return dialCandidates(ctx, dialer, network, port, addrs)
	}
}

func dialCandidates(
	ctx context.Context,
	dialer *net.Dialer,
	network string,
	port string,
	addrs []netip.Addr,
) (net.Conn, error) {
	if len(addrs) == 1 {
		return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].String(), port))
	}

	deadline, hasDeadline := ctx.Deadline()
	if dialer.Timeout > 0 {
		dialDeadline := time.Now().Add(dialer.Timeout)
		if !hasDeadline || dialDeadline.Before(deadline) {
			deadline = dialDeadline
			hasDeadline = true
		}
	}

	var firstErr error
	for i, ip := range addrs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		attemptCtx := ctx
		var cancel context.CancelFunc
		remaining := len(addrs) - i
		if hasDeadline {
			budget := time.Until(deadline)
			if budget <= 0 {
				return nil, context.DeadlineExceeded
			}
			if remaining > 1 {
				attemptCtx, cancel = context.WithTimeout(ctx, budget/time.Duration(remaining))
			} else {
				attemptCtx, cancel = context.WithDeadline(ctx, deadline)
			}
		}

		conn, err := dialer.DialContext(attemptCtx, network, net.JoinHostPort(ip.String(), port))
		if cancel != nil {
			cancel()
		}
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	return nil, firstErr
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
	client *http.Client,
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
		var err error
		lastResult, err = probeTargetOnce(ctx, t, client, now)
		if lastResult.Up {
			return lastResult
		}
		if ctx.Err() != nil {
			return lastResult
		}
		if errors.Is(err, errSSRFBlocked) || errors.Is(err, errNonRetryableRequest) {
			return lastResult
		}
	}

	return lastResult
}

func probeTargetOnce(ctx context.Context, t preparedTarget, client *http.Client, now func() time.Time) (Result, error) {
	res := Result{
		Name:      t.name,
		URL:       t.url,
		Method:    t.method,
		Timestamp: now(),
		Tags:      t.tags,
	}

	if isBlockedMetadataHost(t.parsedURL) {
		res.Error = errSSRFBlocked.Error()
		return res, errSSRFBlocked
	}

	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, t.method, t.url, bytes.NewBufferString(t.body))
	if err != nil {
		res.Error = err.Error()
		return res, fmt.Errorf("%w: %w", errNonRetryableRequest, err)
	}
	req.Header.Set("User-Agent", "prober")

	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	if hostOverride := targetHostOverride(t.headers); hostOverride != "" {
		req.Host = hostOverride
	}

	start := now()
	resp, err := client.Do(req)
	res.LatencyMS = now().Sub(start).Milliseconds()

	if err != nil {
		if errors.Is(err, errSSRFBlocked) {
			res.Error = errSSRFBlocked.Error()
			return res, errSSRFBlocked
		}
		res.Error = err.Error()
		return res, err
	}
	defer func() { _ = resp.Body.Close() }()

	res.Status = resp.StatusCode

	// Read and truncate body to avoid logging enormous stack traces.
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		res.Error = err.Error()
		return res, err
	}

	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	res.Body = string(bodyBytes)
	res.Headers = respHeaders

	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		res.SSLDaysLeft = int(cert.NotAfter.Sub(now()).Hours() / 24)
		res.SSLIssuer = cert.Issuer.String()
		res.SSLSubject = cert.Subject.String()
		res.SSLDNSNames = strings.Join(cert.DNSNames, ",")
	}

	res.Up = true
	for _, a := range t.assertions {
		passed, err := a.evaluate(res)
		if err != nil || !passed {
			res.Up = false
			res.FailedAssertion = a.text
			break
		}
	}

	return res, nil
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
