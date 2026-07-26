// Package sysproxy switches the per-user system proxy.
//
// The machine may already have a proxy configured by another client. Enabling SA05
// therefore snapshots whatever was there and restores it verbatim on disable — the
// toggle must never leave a foreign configuration destroyed or a stale SA05 port behind.
package sysproxy

import "fmt"

// Settings is the proxy SA05 wants applied.
type Settings struct {
	// Host is always loopback; the field exists so tests can be explicit.
	Host string
	// HTTPPort serves HTTP/HTTPS proxying, SocksPort serves SOCKS5.
	HTTPPort  int
	SocksPort int
	// Bypass lists hosts that must never go through the proxy.
	Bypass []string
}

// DefaultBypass keeps loopback and link-local traffic off the tunnel; without it a local
// service call would be proxied back into the client.
var DefaultBypass = []string{"localhost", "127.0.0.0/8", "::1", "192.168.0.0/16",
	"10.0.0.0/8", "172.16.0.0/12", "169.254.0.0/16"}

// Snapshot is the configuration that existed before SA05 touched anything. It is opaque
// to callers and must be handed back unchanged to Disable.
type Snapshot struct {
	// Backend names the implementation that produced this snapshot, so a snapshot taken
	// under GNOME is never replayed into KDE.
	Backend string            `json:"backend"`
	Values  map[string]string `json:"values"`
}

// host is loopback unless a caller overrides it.
func (s Settings) host() string {
	if s.Host != "" {
		return s.Host
	}
	return "127.0.0.1"
}

// bypass falls back to DefaultBypass, so local traffic never loops through the tunnel.
func (s Settings) bypass() []string {
	if len(s.Bypass) > 0 {
		return s.Bypass
	}
	return DefaultBypass
}

// Controller applies and reverts the system proxy for one desktop environment.
type Controller interface {
	// Name identifies the backend in snapshots and error messages.
	Name() string
	// Available reports whether this backend can act on the current session.
	Available() bool
	// Enable applies settings and returns the previous configuration.
	Enable(settings Settings) (Snapshot, error)
	// Disable restores a snapshot taken by Enable.
	Disable(snapshot Snapshot) error
}

// ErrUnavailable is returned when no backend can configure this session.
type ErrUnavailable struct{ Reason string }

func (e *ErrUnavailable) Error() string {
	if e.Reason == "" {
		return "Системный прокси недоступен в этой среде"
	}
	return fmt.Sprintf("Системный прокси недоступен: %s", e.Reason)
}

// Detect picks the first backend that can act on the current session.
func Detect() (Controller, error) {
	for _, controller := range controllers() {
		if controller.Available() {
			return controller, nil
		}
	}
	return nil, &ErrUnavailable{}
}
