// Package upstream is the vendored tg-ws-proxy MTProto proxy, adapted to run as a
// library inside SA05.
//
// proxy.go is generated from the upstream source and must not be edited by hand; this
// file holds everything the desktop client adds: a Go entry point instead of the cgo
// exports, an injectable dialer, and a log sink.
//
// Derived from tg-ws-proxy-android 1.2.0, GPL-3.0. See LICENSE in this directory.
package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// DialFunc opens an outbound connection. The host installs one that marks sockets so the
// proxy's own traffic bypasses the SA05 TUN instead of looping into it.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

var (
	dialMu   sync.RWMutex
	hostDial DialFunc

	logMu   sync.RWMutex
	logSink io.Writer

	runMu     sync.Mutex
	runCancel context.CancelFunc
	runPort   int

	directWSMu      sync.RWMutex
	directWSAllowed = true
)

// Options configures one run of the proxy.
type Options struct {
	// Host and Port are the local MTProto listener Telegram connects to.
	Host string
	Port int
	// Secret is the 32-char hex MTProto secret.
	Secret string
	// CloudflareEnabled selects the Cloudflare-fronted WebSocket path; CloudflareDomain
	// overrides the built-in front-end list.
	CloudflareEnabled bool
	CloudflareDomain  string
	// DirectWebSocket allows the direct WebSocket path to the datacenters. Turning it
	// off leaves Cloudflare-fronted WebSocket and plain TCP.
	DirectWebSocket bool
	// TCPOnly forces plain MTProto TCP to the datacenters, skipping WebSocket entirely.
	TCPOnly bool
	// DCOverrides maps a datacenter number to a fixed address, as the upstream CIDR pool
	// option did.
	DCOverrides map[int]string
	// CacheDir stores the Cloudflare domain cache between runs.
	CacheDir string
	// PoolSize is the number of warm WebSocket connections kept per datacenter.
	PoolSize int
	// Dial opens every outbound connection; nil means a plain net.Dialer.
	Dial DialFunc
	// Log receives the proxy's log lines; nil means stderr.
	Log io.Writer
	// Verbose enables debug logging.
	Verbose bool
}

// Start runs the proxy until ctx is cancelled or Stop is called. It returns once the
// listener is accepting, so callers know the port is ready without polling it.
func Start(ctx context.Context, options Options) error {
	if options.Port < 1 || options.Port > 65535 {
		return fmt.Errorf("некорректный порт MTProto: %d", options.Port)
	}
	if len(options.Secret) != 32 {
		return errors.New("секрет MTProto должен быть 32 hex-символа")
	}

	runMu.Lock()
	defer runMu.Unlock()
	if runCancel != nil {
		return errors.New("Telegram Proxy уже запущен")
	}

	setDialer(options.Dial)
	setLogSink(options.Log)
	initLogging(options.Verbose)
	clearCfproxy429Cooldowns()

	proxySecretMu.Lock()
	proxySecret = options.Secret
	proxySecretMu.Unlock()

	if options.PoolSize > 0 {
		size := int32(options.PoolSize)
		if size < 2 {
			size = 2
		}
		poolSize.Store(size)
	}

	cfproxyMu.Lock()
	cfproxyCacheDir = options.CacheDir
	// TCPOnly switches both WebSocket paths off, so every connection falls through to the
	// plain MTProto TCP dial.
	cfproxyEnabled = options.CloudflareEnabled && !options.TCPOnly
	cfproxyUserDomain = options.CloudflareDomain
	cfproxyMu.Unlock()
	setDirectWebSocket(options.DirectWebSocket && !options.TCPOnly)
	initCfproxyDomains()

	host := options.Host
	if host == "" {
		host = "127.0.0.1"
	}

	runCtx, cancel := context.WithCancel(ctx)
	started := make(chan error, 1)
	go func() {
		_ = runProxy(runCtx, host, options.Port, options.DCOverrides, started)
	}()

	select {
	case err := <-started:
		if err != nil {
			cancel()
			return err
		}
	case <-time.After(5 * time.Second):
		cancel()
		return fmt.Errorf("Telegram Proxy не открыл порт %d", options.Port)
	}

	runCancel = cancel
	runPort = options.Port
	return nil
}

// Stop shuts the proxy down and clears the per-run caches, so the next Start does not
// inherit blacklists from a network that no longer applies.
//
// It returns only once the port is free: a transport switch restarts the proxy on the
// same port, and returning early would race the new listener against the old one.
func Stop() {
	runMu.Lock()
	defer runMu.Unlock()
	if runCancel == nil {
		return
	}
	runCancel()
	runCancel = nil
	waitPortClosed(runPort)
	runPort = 0

	stats.Reset()

	wsBlackMu.Lock()
	wsBlacklist = make(map[[2]int]bool)
	wsBlackMu.Unlock()

	dcFailMu.Lock()
	dcFailUntil = make(map[[2]int]float64)
	dcFailMu.Unlock()

	clearCfproxy429Cooldowns()
}

// Running reports whether a proxy is currently up.
func Running() bool {
	runMu.Lock()
	defer runMu.Unlock()
	return runCancel != nil
}

// Summary is the upstream traffic report, for the diagnostics screen.
func Summary() string { return stats.SummaryRu() }

// waitPortClosed blocks until the listener stops accepting, or the deadline passes.
func waitPortClosed(port int) {
	if port == 0 {
		return
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err != nil {
			return
		}
		connection.Close()
		time.Sleep(20 * time.Millisecond)
	}
}

func setDirectWebSocket(enabled bool) {
	directWSMu.Lock()
	directWSAllowed = enabled
	directWSMu.Unlock()
}

// directWSDisabled is consulted by the generated proxy before it tries a direct
// WebSocket to a datacenter.
func directWSDisabled() bool {
	directWSMu.RLock()
	defer directWSMu.RUnlock()
	return !directWSAllowed
}

func setDialer(dial DialFunc) {
	dialMu.Lock()
	hostDial = dial
	dialMu.Unlock()
}

func setLogSink(sink io.Writer) {
	logMu.Lock()
	logSink = sink
	logMu.Unlock()
}

// dialContext is the single outbound path of the whole proxy: WebSocket, Cloudflare and
// plain TCP all go through it, which is what makes the host's bypass complete.
func dialContext(
	ctx context.Context,
	network, address string,
	timeout, keepAlive time.Duration,
) (net.Conn, error) {
	dialMu.RLock()
	dial := hostDial
	dialMu.RUnlock()

	if dial != nil {
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		return dial(ctx, network, address)
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: keepAlive}
	return dialer.DialContext(ctx, network, address)
}
