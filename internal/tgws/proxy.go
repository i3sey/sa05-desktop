package tgws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/fife/sa05-desktop/internal/tgws/upstream"
)

// poolSize is how many warm WebSocket connections the proxy keeps per datacenter. Four
// is the value the Android client ships.
const poolSize = 4

// Settings configures the local MTProto proxy.
type Settings struct {
	// Port is the listener Telegram is pointed at; zero means the standard SA05 port.
	Port int
	// Secret is the 32-char hex MTProto secret.
	Secret string
	// Transport selects the upstream path to the datacenters.
	Transport Transport
	// CloudflareDomain overrides the built-in Cloudflare front-end list.
	CloudflareDomain string
	// CacheDir stores the Cloudflare domain cache between runs.
	CacheDir string
	// Dial opens the proxy's own outbound connections. The host passes a dialer that
	// keeps this traffic outside the SA05 TUN — otherwise the proxy would feed the tunnel
	// it is supposed to bypass.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Log receives the proxy's log lines; nil means stderr.
	Log io.Writer
}

// Proxy is a running MTProto proxy. The zero value is stopped.
type Proxy struct {
	mutex   sync.Mutex
	cancel  context.CancelFunc
	port    int
	current Transport
}

// Start brings the proxy up and returns once the port accepts connections. Starting an
// already running proxy restarts it with the new settings, which is how the transport
// switch works without changing what the user configured in Telegram.
func (p *Proxy) Start(ctx context.Context, settings Settings) error {
	if !ValidSecret(settings.Secret) {
		return errors.New("Некорректный секрет Telegram Proxy")
	}
	port := settings.Port
	if port == 0 {
		port = Port
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.stopLocked()

	transport := settings.Transport
	if transport == "" {
		transport = TransportAuto
	}
	runCtx, cancel := context.WithCancel(ctx)
	options := upstream.Options{
		Host:              "127.0.0.1",
		Port:              port,
		Secret:            settings.Secret,
		CloudflareEnabled: transport == TransportAuto || transport == TransportCloudflare,
		DirectWebSocket:   transport == TransportAuto || transport == TransportWebSocket,
		TCPOnly:           transport == TransportTCP,
		CloudflareDomain:  settings.CloudflareDomain,
		CacheDir:          settings.CacheDir,
		PoolSize:          poolSize,
		Dial:              settings.Dial,
		Log:               settings.Log,
	}
	if err := upstream.Start(runCtx, options); err != nil {
		cancel()
		return fmt.Errorf("Telegram Proxy не запустился: %w", err)
	}
	p.cancel = cancel
	p.port = port
	p.current = transport
	return nil
}

// Stop shuts the proxy down. Stopping a stopped proxy is a no-op.
func (p *Proxy) Stop() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.stopLocked()
}

func (p *Proxy) stopLocked() {
	if p.cancel == nil {
		return
	}
	upstream.Stop()
	p.cancel()
	p.cancel = nil
	p.port = 0
	p.current = ""
}

// Running reports whether the proxy is up.
func (p *Proxy) Running() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.cancel != nil
}

// Port is the listener port, or zero when stopped.
func (p *Proxy) Port() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.port
}

// Transport is the upstream path currently in use, or "" when stopped.
func (p *Proxy) Transport() Transport {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.current
}

// Summary is the upstream traffic report, for diagnostics.
func Summary() string { return upstream.Summary() }
