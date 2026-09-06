// Package tun owns the tunnel device and the routing that feeds it.
//
// It runs inside the privileged helper. The client only ever asks for a SOCKS port and a
// policy; every interface name, address, table id and route below is chosen here, so a
// compromised client cannot steer the routing table.
package tun

import (
	"context"
	"net"
	"strings"
)

// DeviceName is the tunnel interface. It is fixed so a leftover device from a crashed
// helper is recognisable and reusable instead of accumulating.
const DeviceName = "sa05"

const (
	// TableID is the dedicated routing table holding the tunnel's default routes.
	// Policy routing is used instead of replacing the system default route: the original
	// route stays untouched, so a crash cannot leave the machine unroutable.
	TableID = 5205

	// DefaultMark is the fwmark the client sets on its own sockets. Marked traffic keeps
	// using the main table, which is what stops the client's own connections (Xray
	// outbound, the Telegram proxy, subscription fetches) from looping into the tunnel
	// they feed.
	DefaultMark = 0x5a05

	// MTU leaves room for the tunnel's own overhead on a 1500-byte path.
	MTU = 1500

	// IPv4Address is the tunnel's own address; the /30 keeps it off any real network.
	IPv4Address   = "10.10.10.1/30"
	IPv6Address   = "fd00::1/126"
	udpTimeoutSec = 60
)

// Rule priorities. Lower runs first, so the exceptions are evaluated before the catch-all
// that pushes everything into the tunnel.
const (
	// PriorityLAN keeps local networks on the main table: a tunnelled LAN would break
	// printers, NAS and the router's own web interface.
	PriorityLAN = 95
	// PriorityDefault sends everything else into the tunnel unless it carries the mark.
	PriorityDefault = 100
)

// LocalNetworks stay on the main routing table.
var LocalNetworks = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"224.0.0.0/4",
	"255.255.255.255/32",
}

// Config is what the helper was asked to set up.
type Config struct {
	// SocksPort is the client's loopback SOCKS inbound.
	SocksPort int
	// DNS is advertised as the tunnel's resolver.
	DNS string
	// AllowIPv6Bypass hands IPv6 back to the system. Off means IPv6 enters the tunnel and
	// is dropped there, which is what keeps the real address from leaking.
	AllowIPv6Bypass bool
	// KillSwitch installs a blackhole route that outlives the tunnel device, so traffic
	// stops instead of falling back to the direct route when the core dies.
	KillSwitch bool
	// Mark is the fwmark that bypasses the tunnel; zero means DefaultMark.
	Mark uint32
	// BypassIPs are the VPN server addresses kept outside the tunnel. Linux relies
	// on the fwmark instead and ignores them; Windows (no SO_MARK) installs
	// direct host routes for each, so the core's own sockets cannot loop back
	// into the tunnel they feed.
	BypassIPs []string
}

// State is what the helper reports back.
type State struct {
	Up        bool
	Interface string
	SocksPort int
	Message   string
}

func (c Config) mark() uint32 {
	if c.Mark == 0 {
		return DefaultMark
	}
	return c.Mark
}

func (c Config) dns() string {
	if c.DNS == "" {
		return "1.1.1.1"
	}
	return c.DNS
}

// maxBypassIPs caps how many server addresses one tunnel setup fans out into
// host routes. A subscription holds a handful of outbounds; the cap only stops
// a malformed request from creating hundreds of system routes.
const maxBypassIPs = 64

// normalizedBypassIPs deduplicates the configured server addresses for route
// planning: trims spaces, drops empties, lowercases DNS names (they are
// case-insensitive) and keeps the first-seen order so the result is stable.
func (c Config) normalizedBypassIPs() []string {
	seen := map[string]bool{}
	result := []string{}
	for _, entry := range c.BypassIPs {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, trimmed)
		if len(result) >= maxBypassIPs {
			break
		}
	}
	return result
}

// resolveBypassIPs turns configured server addresses into dialable IPs for host
// routes. IP literals pass through; DNS names are resolved via the system
// resolver (called before the tunnel captures traffic, so the answers come back
// over the real interface). Unresolvable names are skipped rather than failing
// the whole setup: a missing bypass only affects one server, while refusing Up
// would leave the user with no tunnel at all.
func resolveBypassIPs(ctx context.Context, entries []string) []net.IP {
	result := []net.IP{}
	seen := map[string]bool{}
	add := func(address net.IP) {
		if address == nil {
			return
		}
		// Zone-scoped and non-unicast addresses are useless as route targets.
		if !address.IsGlobalUnicast() {
			return
		}
		key := address.String()
		if seen[key] {
			return
		}
		seen[key] = true
		result = append(result, address)
	}
	for _, entry := range entries {
		if address := net.ParseIP(entry); address != nil {
			add(address)
			continue
		}
		resolved, err := net.DefaultResolver.LookupIP(ctx, "ip", entry)
		if err != nil {
			continue
		}
		for _, address := range resolved {
			add(address)
		}
	}
	return result
}
