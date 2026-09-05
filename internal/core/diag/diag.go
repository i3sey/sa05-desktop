// Package diag answers "why doesn't it work" with evidence instead of a guess.
//
// Each target is fetched through the tunnel and the response is validated, not merely
// counted: a captive portal, a DPI reset and a blocked-by-IP page all produce an HTTP
// exchange, and only the body and the final URL tell them apart.
//
// Ported from the Android client's ConnectivityDiagnostics, including its verdict rules,
// so both clients call the same network the same way.
package diag

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/proxy"
)

// Group says what a target proves.
type Group string

const (
	// GroupControl is plain reachability: if these fail, nothing else means anything.
	GroupControl Group = "CONTROL"
	// GroupDPI is blocked by deep packet inspection, so it measures the bypass itself.
	GroupDPI Group = "DPI"
	// GroupMedia is heavy traffic that throttling breaks before it breaks anything else.
	GroupMedia Group = "MEDIA"
	// GroupIP is blocked by address, which a bypass cannot fix — only a different exit can.
	GroupIP Group = "IP"
)

// Status is the outcome of one probe.
type Status string

const (
	StatusOK           Status = "OK"
	StatusFailed       Status = "FAILED"
	StatusInconclusive Status = "INCONCLUSIVE"
)

// Target is one thing to check.
type Target struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
	Group Group  `json:"group"`
	// AllowedRedirectHosts are the hosts a redirect may legitimately land on. Anything
	// else is an interception, not a redirect.
	AllowedRedirectHosts []string `json:"-"`
	// ExpectedStatus is the only acceptable status code; zero means any 2xx.
	ExpectedStatus int `json:"-"`
	// MinimumBodyBytes rejects a stub page served in place of the real one.
	MinimumBodyBytes int `json:"-"`
	// Informational marks a target whose failure does not condemn the tunnel.
	Informational bool `json:"informational"`
}

// Result is one probe's outcome.
type Result struct {
	Target     Target `json:"target"`
	Status     Status `json:"status"`
	DelayMS    int    `json:"delayMs"`
	StatusCode int    `json:"statusCode"`
	BodyBytes  int    `json:"bodyBytes"`
	FinalURL   string `json:"finalUrl"`
	Error      string `json:"error"`
}

// OK reports whether the target answered as expected.
func (r Result) OK() bool { return r.Status == StatusOK }

// RequiredDPISuccesses is how many blocked sites must load before the bypass counts as
// working. One could be luck or a stale cache; two is a pattern.
const RequiredDPISuccesses = 2

// Targets is the standard set, in the order they are probed: controls first, so a dead
// connection is reported as such instead of as a bypass failure.
var Targets = []Target{
	{
		ID:               "google",
		Label:            "Google",
		URL:              "https://www.google.com/generate_204",
		Group:            GroupControl,
		ExpectedStatus:   204,
		MinimumBodyBytes: 0,
	},
	{
		ID:                   "yandex",
		Label:                "Ya.ru",
		URL:                  "https://ya.ru/",
		Group:                GroupControl,
		AllowedRedirectHosts: []string{"dzen.ru", "www.dzen.ru"},
		MinimumBodyBytes:     128,
	},
	{
		ID:    "rutracker",
		Label: "RuTracker",
		URL:   "https://rutracker.org/",
		Group: GroupDPI,
		// RuTracker answers 5xx intermittently for reasons of its own, so its failure is
		// not evidence about the bypass.
		Informational:    true,
		MinimumBodyBytes: 128,
	},
	{
		ID:               "nnmclub",
		Label:            "NNMClub",
		URL:              "https://nnmclub.to/",
		Group:            GroupDPI,
		MinimumBodyBytes: 128,
	},
	{
		ID:                   "youtube",
		Label:                "YouTube",
		URL:                  "https://www.youtube.com/",
		Group:                GroupMedia,
		AllowedRedirectHosts: []string{"youtube.com", "www.youtube.com", "consent.youtube.com"},
		MinimumBodyBytes:     128,
	},
	{
		ID:    "telegram",
		Label: "Telegram",
		URL:   "https://telegram.org/",
		// Telegram is blocked by address in some networks: a working bypass changes
		// nothing there, so this is reported separately from the DPI verdict.
		Group:            GroupIP,
		MinimumBodyBytes: 128,
	},
}

const (
	probeTimeout   = 12 * time.Second
	maxBodyToRead  = 64 * 1024
	userAgentValue = "Mozilla/5.0 (X11; Linux x86_64) SA05/1.0"
)

// Runner probes targets through a SOCKS port.
type Runner struct {
	// SocksPort is the tunnel's inbound; zero probes directly, which is how the client
	// tells "the site is blocked" apart from "the tunnel is broken".
	SocksPort int
	// Timeout bounds one probe; zero means 12s.
	Timeout time.Duration
}

// Run probes every target in order, reporting each result as it arrives so a slow probe
// does not hold up the whole screen.
func (r *Runner) Run(ctx context.Context, targets []Target, report func(Result)) []Result {
	results := make([]Result, 0, len(targets))
	for _, target := range targets {
		result := r.Probe(ctx, target)
		results = append(results, result)
		if report != nil {
			report(result)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return results
}

// Probe fetches one target and classifies the response.
func (r *Runner) Probe(ctx context.Context, target Target) Result {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = probeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := r.client()
	if err != nil {
		return Result{Target: target, Status: StatusFailed, Error: err.Error()}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return Result{Target: target, Status: StatusFailed, Error: err.Error()}
	}
	request.Header.Set("User-Agent", userAgentValue)
	request.Header.Set("Accept", "*/*")

	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return Result{
			Target:  target,
			Status:  StatusFailed,
			DelayMS: milliseconds(time.Since(started)),
			Error:   transportError(err),
		}
	}
	delay := milliseconds(time.Since(started))
	defer response.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(response.Body, maxBodyToRead))
	return classify(target, response, len(body), delay)
}

// classify turns a response into a verdict.
func classify(target Target, response *http.Response, bodyBytes, delay int) Result {
	result := Result{
		Target:     target,
		DelayMS:    delay,
		StatusCode: response.StatusCode,
		BodyBytes:  bodyBytes,
		FinalURL:   response.Request.URL.String(),
		Status:     StatusOK,
	}

	if !redirectAllowed(target, response.Request.URL) {
		result.Status = StatusFailed
		result.Error = "перенаправление на " + response.Request.URL.Host
		return result
	}
	if target.ExpectedStatus != 0 && response.StatusCode != target.ExpectedStatus {
		result.Status = StatusFailed
		result.Error = fmt.Sprintf("ожидался HTTP %d, получен %d",
			target.ExpectedStatus, response.StatusCode)
		return result
	}
	if target.ExpectedStatus == 0 && (response.StatusCode < 200 || response.StatusCode > 299) {
		result.Status = StatusFailed
		result.Error = fmt.Sprintf("HTTP %d", response.StatusCode)
		// A server error says nothing about the tunnel; a block says everything.
		if response.StatusCode >= 500 {
			result.Status = StatusInconclusive
		}
		if response.StatusCode == http.StatusForbidden ||
			response.StatusCode == http.StatusUnavailableForLegalReasons {
			result.Error = fmt.Sprintf("HTTP %d — блокировка", response.StatusCode)
		}
		return result
	}
	if bodyBytes < target.MinimumBodyBytes {
		// A few bytes in place of a page is a stub, which is what interception looks like.
		result.Status = StatusFailed
		result.Error = fmt.Sprintf("ответ слишком короткий: %d байт", bodyBytes)
		return result
	}
	return result
}

func redirectAllowed(target Target, final *url.URL) bool {
	if final == nil {
		return true
	}
	original, err := url.Parse(target.URL)
	if err != nil {
		return true
	}
	if sameSite(original.Host, final.Host) {
		return true
	}
	for _, allowed := range target.AllowedRedirectHosts {
		if sameSite(allowed, final.Host) {
			return true
		}
	}
	return false
}

// sameSite treats www.example.com and example.com as the same site, and nothing else.
func sameSite(left, right string) bool {
	left = strings.TrimPrefix(strings.ToLower(left), "www.")
	right = strings.TrimPrefix(strings.ToLower(right), "www.")
	return left == right
}

func (r *Runner) client() (*http.Client, error) {
	transport := &http.Transport{
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if r.SocksPort != 0 {
		dialer, err := proxy.SOCKS5("tcp",
			net.JoinHostPort("127.0.0.1", fmt.Sprint(r.SocksPort)), nil, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("SOCKS-клиент не создан: %w", err)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS-клиент не поддерживает контекст")
		}
		transport.DialContext = contextDialer.DialContext
	}
	return &http.Client{Transport: transport}, nil
}

// transportError turns a Go network error into something a user can act on.
//
// The classification is by error value first and by text only as a fallback: the wording
// differs between platforms — Windows reports a refused connection as "connectex: No
// connection could be made because the target machine actively refused it" — and matching
// on English prose alone would leak raw socket errors into the UI.
func transportError(err error) string {
	var dnsError *net.DNSError
	var certificateError *tls.CertificateVerificationError
	var recordError tls.RecordHeaderError

	text := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "нет ответа за отведённое время"
	case errors.As(err, &dnsError):
		return "имя не разрешилось"
	case errors.As(err, &certificateError), errors.As(err, &recordError):
		return "проблема с TLS (возможна подмена сертификата)"
	case errors.Is(err, syscall.ECONNREFUSED), strings.Contains(text, "refused"):
		return "соединение отклонено"
	case errors.Is(err, syscall.ECONNRESET),
		strings.Contains(text, "reset"),
		strings.Contains(text, "forcibly closed"):
		return "соединение сброшено (похоже на блокировку)"
	case errors.Is(err, syscall.ECONNABORTED), strings.Contains(text, "aborted"):
		return "соединение прервано"
	case strings.Contains(text, "certificate"), strings.Contains(text, "tls"):
		return "проблема с TLS (возможна подмена сертификата)"
	}

	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return "нет ответа за отведённое время"
	}
	return err.Error()
}

func milliseconds(elapsed time.Duration) int {
	value := int((elapsed + time.Millisecond - 1) / time.Millisecond)
	if value < 1 {
		return 1
	}
	return value
}
