//go:build linux

package tun

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"
	"github.com/xjasonlyu/tun2socks/v2/dialer"
	tunlog "github.com/xjasonlyu/tun2socks/v2/log"
	"github.com/xjasonlyu/tun2socks/v2/proxy/socks5"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	gvstack "gvisor.dev/gvisor/pkg/tcpip/stack"
)

// linkWaitTimeout bounds how long the device may take to appear after creation.
const linkWaitTimeout = 3 * time.Second

// Tunnel owns the device, the netstack and every routing change made for it.
type Tunnel struct {
	mutex   sync.Mutex
	device  device.Device
	stack   *gvstack.Stack
	config  Config
	up      bool
	message string
}

// Up creates the device and points the machine's traffic at the client's SOCKS port.
// Calling it while up re-applies the configuration.
func (t *Tunnel) Up(config Config) (State, error) {
	if config.SocksPort < 1 || config.SocksPort > 65535 {
		return State{}, fmt.Errorf("некорректный порт SOCKS: %d", config.SocksPort)
	}

	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.downLocked()

	if err := t.startStack(config); err != nil {
		t.downLocked()
		return State{}, err
	}
	link, err := waitForLink(DeviceName, linkWaitTimeout)
	if err != nil {
		t.downLocked()
		return State{}, err
	}
	if err := t.configureLink(link, config); err != nil {
		t.downLocked()
		return State{}, err
	}

	t.config = config
	t.up = true
	t.message = ""
	if config.KillSwitch {
		t.message = "Kill-switch активен: без туннеля трафик блокируется"
	}
	return t.stateLocked(), nil
}

// Down removes the routing policy and the device. Routing is restored even if the device
// is already gone, so a crashed core cannot leave the machine half-tunnelled.
func (t *Tunnel) Down() (State, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.downLocked()
	return t.stateLocked(), nil
}

// State reports what the helper currently has set up.
func (t *Tunnel) State() State {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.stateLocked()
}

func (t *Tunnel) stateLocked() State {
	if !t.up {
		return State{}
	}
	return State{
		Up:        true,
		Interface: DeviceName,
		SocksPort: t.config.SocksPort,
		Message:   t.message,
	}
}

// startStack wires the tunnel device to the client's SOCKS inbound.
//
// The tun2socks engine package is deliberately not used: its Start/Stop call log.Fatalf
// on failure, which would take the whole helper down instead of returning an error.
func (t *Tunnel) startStack(config Config) error {
	tunlog.SetLogger(tunlog.Must(tunlog.NewLeveled(tunlog.WarnLevel)))

	// tun2socks dials the client's SOCKS port; marking those sockets keeps them on the
	// main table, so the tunnel never feeds itself.
	dialer.Reset()
	dialer.RegisterSockOpt(dialer.WithRoutingMark(int(config.mark())))

	proxy, err := socks5.New(
		net.JoinHostPort("127.0.0.1", fmt.Sprint(config.SocksPort)), "", "")
	if err != nil {
		return fmt.Errorf("SOCKS-клиент не создан: %w", err)
	}
	tunnel.T().SetProxy(proxy)
	tunnel.T().SetUDPTimeout(udpTimeoutSec * time.Second)

	tunDevice, err := tun.Open(DeviceName, MTU)
	if err != nil {
		return fmt.Errorf("TUN-устройство не создано: %w", err)
	}
	t.device = tunDevice

	networkStack, err := core.CreateStack(&core.Config{
		LinkEndpoint:     tunDevice,
		TransportHandler: tunnel.T(),
	})
	if err != nil {
		return fmt.Errorf("сетевой стек не создан: %w", err)
	}
	t.stack = networkStack
	return nil
}

// configureLink assigns addresses and installs the policy routing.
func (t *Tunnel) configureLink(link netlink.Link, config Config) error {
	if err := netlink.LinkSetMTU(link, MTU); err != nil {
		return fmt.Errorf("MTU не выставлен: %w", err)
	}
	if err := addAddress(link, IPv4Address); err != nil {
		return err
	}
	if !config.AllowIPv6Bypass {
		// IPv6 is captured rather than left to the system: tun2socks answers it inside the
		// tunnel, and an uncaptured ::/0 would carry the real address straight out.
		if err := addAddress(link, IPv6Address); err != nil {
			return err
		}
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("интерфейс не поднят: %w", err)
	}

	if err := t.installRoutes(link, config); err != nil {
		return err
	}
	return t.installRules(config)
}

func (t *Tunnel) installRoutes(link netlink.Link, config Config) error {
	routes := []*netlink.Route{{
		LinkIndex: link.Attrs().Index,
		Table:     TableID,
		Dst:       mustCIDR("0.0.0.0/0"),
		Priority:  10,
	}}
	if !config.AllowIPv6Bypass {
		routes = append(routes, &netlink.Route{
			LinkIndex: link.Attrs().Index,
			Table:     TableID,
			Dst:       mustCIDR("::/0"),
			Priority:  10,
			Family:    netlink.FAMILY_V6,
		})
	}
	if config.KillSwitch {
		// A blackhole with a worse metric sits behind the tunnel route. When the device
		// disappears its route goes with it and this one takes over, so traffic stops
		// instead of silently falling back to the direct path.
		routes = append(routes,
			&netlink.Route{
				Table:    TableID,
				Dst:      mustCIDR("0.0.0.0/0"),
				Type:     unixRouteBlackhole,
				Priority: 100,
			},
			&netlink.Route{
				Table:    TableID,
				Dst:      mustCIDR("::/0"),
				Type:     unixRouteBlackhole,
				Priority: 100,
				Family:   netlink.FAMILY_V6,
			})
	}
	for _, route := range routes {
		if err := netlink.RouteReplace(route); err != nil {
			return fmt.Errorf("маршрут %s не добавлен: %w", route.Dst, err)
		}
	}
	return nil
}

func (t *Tunnel) installRules(config Config) error {
	for _, rule := range t.plannedRules(config) {
		if err := netlink.RuleAdd(rule); err != nil && !errors.Is(err, errExists) {
			// RuleAdd returns EEXIST for a duplicate; anything else is fatal because the
			// policy would be incomplete and traffic could leak around the tunnel.
			if !isExist(err) {
				return fmt.Errorf("правило маршрутизации не добавлено: %w", err)
			}
		}
	}
	return nil
}

// plannedRules is the policy: local networks stay direct, marked traffic stays direct,
// everything else goes into the tunnel table.
func (t *Tunnel) plannedRules(config Config) []*netlink.Rule {
	rules := []*netlink.Rule{}
	families := []int{netlink.FAMILY_V4}
	if !config.AllowIPv6Bypass {
		families = append(families, netlink.FAMILY_V6)
	}

	for _, network := range LocalNetworks {
		rule := netlink.NewRule()
		rule.Priority = PriorityLAN
		rule.Dst = mustCIDR(network)
		rule.Table = mainTable
		rule.Family = netlink.FAMILY_V4
		rules = append(rules, rule)
	}
	for _, family := range families {
		rule := netlink.NewRule()
		rule.Priority = PriorityDefault
		rule.Mark = config.mark()
		rule.Mask = &fullMask
		rule.Invert = true
		rule.Table = TableID
		rule.Family = family
		rules = append(rules, rule)
	}
	return rules
}

// downLocked reverses everything Up did, in reverse order and best-effort: a partially
// applied setup must still be fully removed.
func (t *Tunnel) downLocked() {
	config := t.config
	if config.SocksPort == 0 {
		config = Config{}
	}
	for _, rule := range t.plannedRules(config) {
		_ = netlink.RuleDel(rule)
	}
	// Also drop the IPv6 variants in case the last run captured IPv6 and this one does not.
	ipv6Config := config
	ipv6Config.AllowIPv6Bypass = false
	for _, rule := range t.plannedRules(ipv6Config) {
		_ = netlink.RuleDel(rule)
	}
	flushTable(TableID)

	if t.stack != nil {
		t.stack.Close()
		t.stack.Wait()
		t.stack = nil
	}
	if t.device != nil {
		t.device.Close()
		t.device = nil
	}
	if link, err := netlink.LinkByName(DeviceName); err == nil {
		_ = netlink.LinkDel(link)
	}
	t.up = false
	t.message = ""
	t.config = Config{}
}

func addAddress(link netlink.Link, cidr string) error {
	address, err := netlink.ParseAddr(cidr)
	if err != nil {
		return fmt.Errorf("некорректный адрес %s: %w", cidr, err)
	}
	if err := netlink.AddrReplace(link, address); err != nil {
		return fmt.Errorf("адрес %s не назначен: %w", cidr, err)
	}
	return nil
}

// waitForLink polls for the interface: the device is created asynchronously by the
// netstack, so it may not be visible to netlink the instant Open returns.
func waitForLink(name string, timeout time.Duration) (netlink.Link, error) {
	deadline := time.Now().Add(timeout)
	for {
		link, err := netlink.LinkByName(name)
		if err == nil {
			return link, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("интерфейс %s не появился: %w", name, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func flushTable(table int) {
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		routes, err := netlink.RouteListFiltered(family,
			&netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
		if err != nil {
			continue
		}
		for index := range routes {
			_ = netlink.RouteDel(&routes[index])
		}
	}
}

func mustCIDR(value string) *net.IPNet {
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		panic("некорректная константа сети: " + value)
	}
	return network
}
