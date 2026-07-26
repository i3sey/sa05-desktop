// Package engine runs Xray in-process.
//
// The provider config is loaded as-is; only a runtime copy is touched, and only to add
// the Beeline XHTTP padding and the HTTP inbound the system-proxy toggle needs. The
// engine owns nothing else: TUN, Telegram and the system proxy are separate toggles
// layered on top of the SOCKS/HTTP inbounds published here.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	xcore "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all" // registers every protocol and transport

	"github.com/fife/sa05-desktop/internal/core/xrayconf"
)

// DefaultHTTPPort is the loopback HTTP inbound offered to the system proxy when the
// provider config has none.
const DefaultHTTPPort = 10809

const (
	startTimeout      = 10 * time.Second
	dialProbeInterval = 100 * time.Millisecond
	dialProbeTimeout  = 300 * time.Millisecond
)

// Ports describes where a running instance accepts local traffic.
type Ports struct {
	Socks int
	HTTP  int
}

// Engine is a single running Xray instance. It is safe for concurrent use; Start and
// Stop serialize against each other.
type Engine struct {
	// AssetDir is exported to Xray as XRAY_LOCATION_ASSET (geoip.dat / geosite.dat).
	AssetDir string
	// HTTPPort is the loopback HTTP inbound added when the provider config has none.
	HTTPPort int
	// ForceHTTPPort rebinds a provider-supplied HTTP inbound to HTTPPort, which is how a
	// collision with another proxy client on that port is resolved.
	ForceHTTPPort bool
	// SocksPort overrides the provider's SOCKS inbound port. Zero keeps the provider's.
	SocksPort int
	// OutboundMark is stamped on the core's own sockets so they bypass the client's TUN.
	// Zero leaves the provider's sockopt untouched.
	OutboundMark int
	// AutoPort rebinds a busy inbound to a free port instead of failing. Desktops
	// routinely already run another client on the provider's port.
	AutoPort bool
	// Ephemeral puts both inbounds on kernel-assigned ports. Latency probes use it so a
	// measurement can never collide with the running tunnel.
	Ephemeral bool

	mutex    sync.Mutex
	instance *xcore.Instance
	ports    Ports
}

// New returns an engine that reads geo assets from assetDir.
func New(assetDir string) *Engine {
	return &Engine{AssetDir: assetDir, HTTPPort: DefaultHTTPPort}
}

// Start brings up the profile and returns once its SOCKS inbound accepts connections.
// A previously running instance is stopped first, so Start is also the reconnect path.
func (e *Engine) Start(ctx context.Context, profileJSON string) (Ports, error) {
	runtimeJSON, ports, err := e.buildRuntime(profileJSON)
	if err != nil {
		return Ports{}, err
	}
	if e.AutoPort {
		runtimeJSON, ports, err = e.relocateBusyPorts(profileJSON, runtimeJSON, ports)
		if err != nil {
			return Ports{}, err
		}
	}

	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.stopLocked()

	if e.AssetDir != "" {
		// Xray reads this at rule-compile time, so it must be set before core.New.
		if err := os.Setenv("XRAY_LOCATION_ASSET", e.AssetDir); err != nil {
			return Ports{}, fmt.Errorf("не задан каталог гео-баз: %w", err)
		}
	}

	// Xray binds with SO_REUSEPORT: a port already held by another proxy client would be
	// accepted and the two listeners would split incoming connections. Refuse instead.
	if err := ensurePortFree(ports.Socks, "SOCKS"); err != nil {
		return Ports{}, err
	}
	if err := ensurePortFree(ports.HTTP, "HTTP"); err != nil {
		return Ports{}, err
	}

	config, err := serial.LoadJSONConfig(strings.NewReader(runtimeJSON))
	if err != nil {
		return Ports{}, fmt.Errorf("Xray не принял конфигурацию: %w", err)
	}
	instance, err := xcore.New(config)
	if err != nil {
		return Ports{}, fmt.Errorf("Xray не создан: %w", err)
	}
	if err := instance.Start(); err != nil {
		instance.Close()
		return Ports{}, fmt.Errorf("Xray не запустился: %w", err)
	}

	if err := waitForPort(ctx, ports.Socks, startTimeout); err != nil {
		instance.Close()
		return Ports{}, err
	}
	e.instance = instance
	e.ports = ports
	return ports, nil
}

// Stop shuts the instance down. Stopping a stopped engine is a no-op.
func (e *Engine) Stop() {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.stopLocked()
}

func (e *Engine) stopLocked() {
	if e.instance == nil {
		return
	}
	e.instance.Close()
	e.instance = nil
	e.ports = Ports{}
}

// Ports reports the inbounds of the running instance, or zeros when stopped.
func (e *Engine) Ports() Ports {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return e.ports
}

// Running reports whether an instance is up.
func (e *Engine) Running() bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return e.instance != nil
}

// Healthy checks that the SOCKS inbound still accepts connections. This is the health
// signal the supervisor and the TUN kill-switch act on: a live process with a dead
// inbound is indistinguishable from a hung tunnel for the user.
func (e *Engine) Healthy(ctx context.Context) bool {
	ports := e.Ports()
	if ports.Socks == 0 {
		return false
	}
	return dialLoopback(ctx, ports.Socks) == nil
}

// buildRuntime derives the runtime config: provider JSON + Beeline padding + an HTTP
// inbound for the system proxy. The stored profile itself is never modified.
func (e *Engine) buildRuntime(profileJSON string) (string, Ports, error) {
	if strings.TrimSpace(profileJSON) == "" {
		return "", Ports{}, errors.New("Профиль не выбран")
	}
	padded, err := xrayconf.ApplyBeelinePadding(profileJSON)
	if err != nil {
		return "", Ports{}, err
	}
	socksPort := e.SocksPort
	httpPort := e.HTTPPort
	forceHTTP := e.ForceHTTPPort
	if e.Ephemeral {
		if socksPort, err = freeLoopbackPort(); err != nil {
			return "", Ports{}, err
		}
		if httpPort, err = freeLoopbackPort(); err != nil {
			return "", Ports{}, err
		}
		forceHTTP = true
	}
	if socksPort != 0 {
		padded, err = xrayconf.OverrideSocksPort(padded, socksPort)
		if err != nil {
			return "", Ports{}, err
		}
	}
	if e.OutboundMark != 0 {
		padded, err = xrayconf.ApplyOutboundMark(padded, e.OutboundMark)
		if err != nil {
			return "", Ports{}, err
		}
	}
	if httpPort == 0 {
		httpPort = DefaultHTTPPort
	}
	withHTTP, resolvedHTTP, err := xrayconf.EnsureHTTPInbound(padded, httpPort, forceHTTP)
	if err != nil {
		return "", Ports{}, err
	}
	validated, err := xrayconf.Validate(withHTTP)
	if err != nil {
		return "", Ports{}, err
	}
	return validated.RuntimeJSON, Ports{Socks: validated.SocksPort, HTTP: resolvedHTTP}, nil
}

// relocateBusyPorts rebuilds the runtime config on free ports when the requested ones
// are taken. Rebuilding from the original profile keeps the change to the port fields
// only — the provider's routing and outbounds are re-derived, never edited twice.
func (e *Engine) relocateBusyPorts(profileJSON, runtimeJSON string, ports Ports) (string, Ports, error) {
	socksBusy := ensurePortFree(ports.Socks, "SOCKS") != nil
	httpBusy := ensurePortFree(ports.HTTP, "HTTP") != nil
	if !socksBusy && !httpBusy {
		return runtimeJSON, ports, nil
	}

	original := Engine{
		AssetDir:      e.AssetDir,
		HTTPPort:      e.HTTPPort,
		ForceHTTPPort: e.ForceHTTPPort,
		SocksPort:     e.SocksPort,
		OutboundMark:  e.OutboundMark,
	}
	if socksBusy {
		port, err := freeLoopbackPort()
		if err != nil {
			return "", Ports{}, err
		}
		original.SocksPort = port
	}
	if httpBusy {
		port, err := freeLoopbackPort()
		if err != nil {
			return "", Ports{}, err
		}
		original.HTTPPort = port
		original.ForceHTTPPort = true
	}
	return original.buildRuntime(profileJSON)
}

// freeLoopbackPort asks the kernel for an unused port. The window between closing the
// probe listener and Xray binding is tiny; a lost race surfaces as a start error rather
// than a silently shared port.
func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("свободный порт не найден: %w", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// ensurePortFree reports a busy loopback port as a user-facing error instead of letting
// Xray share it through SO_REUSEPORT.
func ensurePortFree(port int, role string) error {
	if port == 0 {
		return nil
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		return fmt.Errorf("порт %s %d занят другим приложением", role, port)
	}
	return listener.Close()
}

func waitForPort(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := dialLoopback(ctx, port); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Xray не открыл SOCKS-порт %d", port)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(dialProbeInterval):
		}
	}
}

func dialLoopback(ctx context.Context, port int) error {
	dialer := net.Dialer{Timeout: dialProbeTimeout}
	connection, err := dialer.DialContext(ctx, "tcp",
		net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		return err
	}
	return connection.Close()
}
