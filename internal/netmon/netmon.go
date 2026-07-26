// Package netmon reports when the network under the tunnel changes.
//
// A dead core is easy to notice — its SOCKS port stops answering. A changed network is
// not: after a Wi-Fi to LTE switch the core is alive and still listening, but its
// connection to the server is gone. Without watching the routes, the client keeps showing
// "connected" over a tunnel that carries nothing.
package netmon

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Debounce is how long to wait after a change before acting. A single Wi-Fi handover
// produces a burst of link, address and route events; reacting to each one would restart
// the tunnel several times over.
const Debounce = 1500 * time.Millisecond

// Fingerprint identifies the network the machine is currently on. Two fingerprints that
// differ mean the route to the server may have changed.
type Fingerprint string

// None is the fingerprint of a machine with no usable network.
const None Fingerprint = ""

// Watcher reports network changes on its channel.
type Watcher interface {
	// Changes emits whenever the routing environment changes. It never blocks the sender:
	// a listener that is busy simply misses intermediate events, which is harmless because
	// the current fingerprint is always re-read afterwards.
	Changes() <-chan struct{}
	// Close stops the watcher.
	Close()
}

// Watch starts the platform watcher. Callers must treat a nil watcher as "no monitoring
// available" and fall back to their own polling.
func Watch(ctx context.Context) (Watcher, error) { return watch(ctx) }

// Current reads the current fingerprint: the default-route interface plus the addresses
// it carries. The interface alone is not enough — a DHCP renew on the same interface can
// hand out a different address and gateway.
func Current() Fingerprint {
	interfaces, err := net.Interfaces()
	if err != nil {
		return None
	}
	parts := []string{}
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := item.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.IsLinkLocalUnicast() {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%s", item.Name, ip))
		}
	}
	if len(parts) == 0 {
		return None
	}
	// Interface enumeration order is not stable, and the fingerprint must be.
	sort.Strings(parts)
	return Fingerprint(strings.Join(parts, ","))
}

// Online reports whether any usable network exists.
func Online() bool { return Current() != None }
