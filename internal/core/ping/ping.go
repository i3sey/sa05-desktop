// Package ping measures how fast each subscription profile actually answers.
//
// A profile is measured by running it: the whole config is started on ephemeral
// loopback ports and probed through its own SOCKS inbound. That is slower than dialing
// one endpoint, but it validates what the user will actually get — protocol, TLS/Reality
// handshake, transport and the provider's balancer choice — which a raw TCP connect to
// the endpoint address cannot do.
//
// Ported in spirit from the Android client's XrayPingEngine, which measures the time
// from writing the HTTP request to the first response byte.
package ping

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"github.com/fife/sa05-desktop/internal/core/engine"
	"github.com/fife/sa05-desktop/internal/core/subscription"
	"github.com/fife/sa05-desktop/internal/core/xrayconf"
)

// DefaultConcurrency bounds how many cores run at once. Each one is a full Xray
// instance, so this trades wall-clock against memory.
const DefaultConcurrency = 4

const defaultTimeout = 10 * time.Second

// Result is one profile's latency or the reason it could not be measured.
type Result struct {
	ProfileID string        `json:"profileId"`
	Latency   time.Duration `json:"-"`
	LatencyMS int           `json:"latencyMs"`
	Error     string        `json:"error"`
}

// OK reports whether the profile answered.
func (r Result) OK() bool { return r.Error == "" }

// Measurer runs the probes.
type Measurer struct {
	// AssetDir is passed to every temporary core (geoip.dat / geosite.dat).
	AssetDir string
	// Concurrency bounds parallel cores; zero means DefaultConcurrency.
	Concurrency int
	// Timeout bounds one profile's measurement; zero means 10s.
	Timeout time.Duration
}

// Measure starts one profile and returns the time to the first response byte.
func (m *Measurer) Measure(ctx context.Context, profileJSON string) (time.Duration, error) {
	timeout := m.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	probeURL, err := xrayconf.ProbeURLOf(profileJSON)
	if err != nil {
		return 0, err
	}

	core := engine.New(m.AssetDir)
	// Ephemeral ports on both inbounds: a measurement must never collide with the
	// running tunnel or with another measurement.
	core.Ephemeral = true

	ports, err := core.Start(ctx, profileJSON)
	if err != nil {
		return 0, err
	}
	defer core.Stop()

	return probe(ctx, ports.Socks, probeURL)
}

// MeasureAll measures every profile, bounded by Concurrency, and returns results in the
// input order. A failed profile yields a Result with Error set, never a missing entry.
func (m *Measurer) MeasureAll(ctx context.Context, profiles []subscription.Profile) []Result {
	concurrency := m.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	results := make([]Result, len(profiles))
	slots := make(chan struct{}, concurrency)
	group := sync.WaitGroup{}

	for index, profile := range profiles {
		group.Add(1)
		go func(index int, profile subscription.Profile) {
			defer group.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				results[index] = Result{ProfileID: profile.ID, Error: "проверка отменена"}
				return
			}
			defer func() { <-slots }()

			latency, err := m.Measure(ctx, profile.JSON)
			if err != nil {
				results[index] = Result{ProfileID: profile.ID, Error: err.Error()}
				return
			}
			results[index] = Result{
				ProfileID: profile.ID,
				Latency:   latency,
				LatencyMS: milliseconds(latency),
			}
		}(index, profile)
	}
	group.Wait()
	return results
}

// milliseconds rounds up, so a sub-millisecond answer reports 1 rather than 0 — callers
// treat 0 as "not measured", and a loopback-fast profile is measured.
func milliseconds(latency time.Duration) int {
	value := int((latency + time.Millisecond - 1) / time.Millisecond)
	if value < 1 {
		return 1
	}
	return value
}

// probe measures the time from issuing the request to the first response byte through
// the given SOCKS port.
func probe(ctx context.Context, socksPort int, probeURL string) (time.Duration, error) {
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(socksPort)), nil, proxy.Direct)
	if err != nil {
		return 0, fmt.Errorf("SOCKS-клиент не создан: %w", err)
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return 0, errors.New("SOCKS-клиент не поддерживает контекст")
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext:       contextDialer.DialContext,
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return 0, err
	}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("проба не прошла: %w", err)
	}
	// Headers are in hand here: that is the first response byte, matching Android.
	elapsed := time.Since(started)
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return 0, fmt.Errorf("сервер ответил HTTP %d", response.StatusCode)
	}
	return elapsed, nil
}
