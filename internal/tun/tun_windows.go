//go:build windows

package tun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"
	"github.com/xjasonlyu/tun2socks/v2/dialer"
	tunlog "github.com/xjasonlyu/tun2socks/v2/log"
	"github.com/xjasonlyu/tun2socks/v2/proxy/socks5"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	gvstack "gvisor.dev/gvisor/pkg/tcpip/stack"
)

// Windows has no SO_MARK, so the bypass works differently from Linux end to end:
//   - the core's server addresses get direct host routes through the previous
//     default gateway (see Config.BypassIPs),
//   - LANs stay direct automatically: they are more specific than the split
//     defaults below, and the original routes are never deleted,
//   - everything else is captured by two split defaults (0.0.0.0/1 + 128.0.0.0/1,
//     plus ::/1 + 8000::/1 when IPv6 is captured) pointing at the TUN device.
//     Split defaults are used instead of replacing 0.0.0.0/0 so a crash leaves the
//     original default in place and the machine routable.
//
// All system changes go through netsh/route/powershell, which exist on every
// supported Windows out of the box and need no extra driver beyond wintun.dll
// next to the helper binary.

const (
	// tunPeerV4 is the far end of the /30: packets for the split defaults are
	// sent there, and the Wintun driver hands them to the netstack.
	tunPeerV4 = "10.10.10.2"
	tunPeerV6 = "fd00::2"

	// nullGatewayV4/V6 are unroutable sinks for the persistent kill-switch
	// routes: packets sent there are dropped instead of falling back to direct.
	nullGatewayV4 = "10.255.255.1"
	nullGatewayV6 = "2001:db8::1"

	// lowMetric wins over DHCP defaults (usually 25+) but stays above loopback.
	lowMetric = 5
	// killMetric keeps the persistent block below any real default.
	killMetric = 250

	ifaceWaitTimeout = 10 * time.Second
	cmdTimeout       = 15 * time.Second
)

// Tunnel owns the device, the netstack and every routing change made for it.
type Tunnel struct {
	mutex   sync.Mutex
	device  device.Device
	stack   *gvstack.Stack
	config  Config
	up      bool
	message string

	tunIfIndex int
	origGateV4 net.IP
	origIfV4   int
	origGateV6 net.IP
	origIfV6   int
	v4Routes   []routeSpec
	v6Routes   []string
	hostRoutes []routeSpec
	hostV6     []string
	dnsSet     bool
}

// routeSpec is one IPv4 route added with route.exe.
type routeSpec struct {
	dst     string
	mask    string
	gateway string
	metric  int
	ifIndex int
	persist bool
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

	// A previous run with the kill-switch leaves persistent block routes behind;
	// they must go before the tunnel routes, or the tunnel captures nothing.
	removeKillSwitchRoutes()

	if err := t.startStack(config); err != nil {
		t.downLocked()
		return State{}, err
	}
	iface, err := waitForInterface(DeviceName, ifaceWaitTimeout)
	if err != nil {
		t.downLocked()
		return State{}, err
	}
	t.tunIfIndex = iface.Index
	if err := t.configureInterface(iface, config); err != nil {
		t.downLocked()
		return State{}, err
	}

	t.config = config
	t.up = true
	t.message = ""
	if config.KillSwitch {
		t.message = "Kill-switch активен: без туннеля трафик блокируется"
	}
	if !t.dnsSet {
		if t.message != "" {
			t.message += "; "
		}
		t.message += "DNS на туннеле не выставлен, возможен прямой резолв"
	}
	return t.stateLocked(), nil
}

// Down removes the tunnel routes and stops the netstack. With the kill-switch it
// installs persistent block routes instead, so traffic stops rather than falling
// back to the direct path once the core is gone.
func (t *Tunnel) Down() (State, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	kill := t.config.KillSwitch
	t.downLocked()
	if kill {
		installKillSwitchRoutes()
	}
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

// CleanupStale removes leftover tunnel policy from a crashed run. Split defaults
// pointing at a dead reader only blackhole traffic, so they are reaped; the
// kill-switch block is kept (it is the documented fail-closed state).
func CleanupStale() {
	for _, route := range plannedSplitV4(0) {
		_ = deleteRouteV4(route.dst, route.mask)
	}
	for _, prefix := range plannedSplitV6() {
		_ = deleteRouteV6(prefix, DeviceName)
	}
}

// startStack wires the Wintun device to the client's SOCKS inbound.
//
// Like on Linux, the tun2socks engine package is not used: its Start/Stop call
// log.Fatalf on failure, which would take the whole helper down. Unlike Linux,
// no routing mark is set — Windows sockets cannot carry one, the bypass is done
// with host routes (see Up).
func (t *Tunnel) startStack(config Config) error {
	tunlog.SetLogger(tunlog.Must(tunlog.NewLeveled(tunlog.WarnLevel)))

	dialer.Reset()

	proxy, err := socks5.New(
		net.JoinHostPort("127.0.0.1", fmt.Sprint(config.SocksPort)), "", "")
	if err != nil {
		return fmt.Errorf("SOCKS-клиент не создан: %w", err)
	}
	tunnel.T().SetProxy(proxy)
	tunnel.T().SetUDPTimeout(udpTimeoutSec * time.Second)

	tunDevice, err := tun.Open(DeviceName, MTU)
	if err != nil {
		t.device = nil
		return fmt.Errorf("TUN-устройство не создано: %w — нужен wintun.dll рядом с sa05-helper.exe (https://www.wintun.net)", err)
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

// configureInterface assigns addresses, DNS and routes to the Wintun adapter.
func (t *Tunnel) configureInterface(iface *net.Interface, config Config) error {
	ctx := context.Background()

	gateV4, ifV4, err := defaultRouteV4(ctx)
	if err != nil {
		return fmt.Errorf("основной маршрут не найден: %w", err)
	}
	t.origGateV4, t.origIfV4 = gateV4, ifV4

	if err := setInterfaceAddress(DeviceName, IPv4Address); err != nil {
		return err
	}
	if !config.AllowIPv6Bypass {
		if err := addInterfaceAddressV6(DeviceName, IPv6Address); err != nil {
			return err
		}
		if gateV6, ifV6, err := defaultRouteV6(ctx); err == nil {
			t.origGateV6, t.origIfV6 = gateV6, ifV6
		}
	}
	if err := setInterfaceMTU(DeviceName, MTU); err != nil {
		return err
	}
	// The TUN interface must win DNS and forwarding decisions over the physical one.
	if err := setInterfaceMetric(DeviceName, lowMetric); err != nil {
		return err
	}

	// Host routes first: once the split defaults land, the server addresses must
	// already have their direct path, or the core's own handshake loops into the
	// tunnel it is trying to build.
	bypass := resolveBypassIPs(ctx, config.normalizedBypassIPs())
	for _, address := range bypass {
		if address.To4() != nil {
			if t.origGateV4 == nil {
				continue
			}
			spec := routeSpec{
				dst: address.String(), mask: "255.255.255.255",
				gateway: t.origGateV4.String(), metric: 1, ifIndex: t.origIfV4,
			}
			if err := addRouteV4(spec); err != nil {
				return fmt.Errorf("bypass-маршрут %s не добавлен: %w", spec.dst, err)
			}
			t.hostRoutes = append(t.hostRoutes, spec)
			continue
		}
		if t.origGateV6 == nil {
			continue
		}
		prefix := address.String() + "/128"
		if err := addRouteV6(prefix, interfaceNameByIndex(t.origIfV6), t.origGateV6.String(), 1); err != nil {
			return fmt.Errorf("bypass-маршрут %s не добавлен: %w", prefix, err)
		}
		t.hostV6 = append(t.hostV6, prefix)
	}

	for _, spec := range plannedSplitV4(t.tunIfIndex) {
		if err := addRouteV4(spec); err != nil {
			return fmt.Errorf("маршрут %s не добавлен: %w", spec.dst, err)
		}
		t.v4Routes = append(t.v4Routes, spec)
	}
	if !config.AllowIPv6Bypass {
		for _, prefix := range plannedSplitV6() {
			if err := addRouteV6(prefix, DeviceName, tunPeerV6, lowMetric); err != nil {
				return fmt.Errorf("маршрут %s не добавлен: %w", prefix, err)
			}
			t.v6Routes = append(t.v6Routes, prefix)
		}
	}

	// DNS is best-effort: the tunnel carries traffic either way. A failure only
	// clears dnsSet; Up composes the user-facing warning from it, so the
	// kill-switch note set there is never overwritten here.
	if err := setInterfaceDNS(DeviceName, config.dns()); err != nil {
		t.dnsSet = false
		return nil
	}
	t.dnsSet = true
	return nil
}

// plannedSplitV4 is the capture: two halves cover everything except the host
// routes above (more specific) and LANs (more specific, untouched originals).
func plannedSplitV4(tunIfIndex int) []routeSpec {
	return []routeSpec{
		{dst: "0.0.0.0", mask: "128.0.0.0", gateway: tunPeerV4, metric: lowMetric, ifIndex: tunIfIndex},
		{dst: "128.0.0.0", mask: "128.0.0.0", gateway: tunPeerV4, metric: lowMetric, ifIndex: tunIfIndex},
	}
}

// plannedSplitV6 halves ::/0 the same way.
func plannedSplitV6() []string { return []string{"::/1", "8000::/1"} }

// downLocked reverses everything Up did, best-effort and in reverse order.
func (t *Tunnel) downLocked() {
	for index := len(t.v6Routes) - 1; index >= 0; index-- {
		_ = deleteRouteV6(t.v6Routes[index], DeviceName)
	}
	for index := len(t.v4Routes) - 1; index >= 0; index-- {
		_ = deleteRouteV4(t.v4Routes[index].dst, t.v4Routes[index].mask)
	}
	for index := len(t.hostV6) - 1; index >= 0; index-- {
		_ = deleteRouteV6(t.hostV6[index], interfaceNameByIndex(t.origIfV6))
	}
	for index := len(t.hostRoutes) - 1; index >= 0; index-- {
		_ = deleteRouteV4(t.hostRoutes[index].dst, t.hostRoutes[index].mask)
	}
	if t.dnsSet {
		_ = resetInterfaceDNS(DeviceName)
		t.dnsSet = false
	}

	if t.stack != nil {
		t.stack.Close()
		t.stack.Wait()
		t.stack = nil
	}
	if t.device != nil {
		t.device.Close()
		t.device = nil
	}

	// Without the kill-switch the persistent block must go too, or the machine
	// stays offline after the tunnel is switched off.
	if !t.config.KillSwitch {
		removeKillSwitchRoutes()
	}

	t.up = false
	t.message = ""
	t.config = Config{}
	t.tunIfIndex = 0
	t.origGateV4, t.origIfV4 = nil, 0
	t.origGateV6, t.origIfV6 = nil, 0
	t.v4Routes, t.v6Routes = nil, nil
	t.hostRoutes, t.hostV6 = nil, nil
}

func installKillSwitchRoutes() {
	for _, spec := range killSwitchV4() {
		_ = addRouteV4(spec)
	}
	for _, prefix := range plannedSplitV6() {
		_ = addRouteV6Persistent(prefix, nullGatewayV6, killMetric)
	}
}

func removeKillSwitchRoutes() {
	for _, spec := range killSwitchV4() {
		_ = deleteRouteV4(spec.dst, spec.mask)
	}
	for _, prefix := range plannedSplitV6() {
		_ = deleteRouteV6(prefix, "")
	}
}

// killSwitchV4 is the persistent block: split defaults via an unroutable sink,
// kept in the registry (-p) so they survive a reboot without the helper.
func killSwitchV4() []routeSpec {
	return []routeSpec{
		{dst: "0.0.0.0", mask: "128.0.0.0", gateway: nullGatewayV4, metric: killMetric, persist: true},
		{dst: "128.0.0.0", mask: "128.0.0.0", gateway: nullGatewayV4, metric: killMetric, persist: true},
	}
}

// waitForInterface polls for the Wintun adapter: the driver creates it
// asynchronously, so it may not be visible the instant Open returns.
func waitForInterface(name string, timeout time.Duration) (*net.Interface, error) {
	deadline := time.Now().Add(timeout)
	for {
		ifaces, err := net.Interfaces()
		if err == nil {
			for index := range ifaces {
				if strings.EqualFold(ifaces[index].Name, name) {
					iface := ifaces[index]
					return &iface, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("интерфейс %s не появился", name)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func interfaceNameByIndex(index int) string {
	if index == 0 {
		return ""
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return ""
	}
	return iface.Name
}

// defaultRouteV4 discovers the current IPv4 default gateway before the tunnel
// captures traffic. PowerShell's JSON output is locale-independent, unlike
// parsing route print.
func defaultRouteV4(ctx context.Context) (net.IP, int, error) {
	output, err := runPS(ctx,
		`Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction Stop | `+
			`Sort-Object RouteMetric, InterfaceMetric | Select-Object -First 1 -Property NextHop, InterfaceIndex | `+
			`ConvertTo-Json -Compress`)
	if err != nil {
		return nil, 0, err
	}
	route, err := parseNetRoute(output)
	if err != nil {
		return nil, 0, err
	}
	if gateway := net.ParseIP(route.NextHop); gateway != nil && !gateway.IsUnspecified() {
		return gateway, route.InterfaceIndex, nil
	}
	// On-link default (no gateway): use the interface's own address as the
	// next hop for host routes, which keeps them on that link.
	if address := firstUnicastV4(route.InterfaceIndex); address != "" {
		return net.ParseIP(address), route.InterfaceIndex, nil
	}
	return nil, 0, errors.New("у маршрута по умолчанию нет шлюза")
}

func defaultRouteV6(ctx context.Context) (net.IP, int, error) {
	output, err := runPS(ctx,
		`Get-NetRoute -DestinationPrefix '::/0' -ErrorAction Stop | `+
			`Sort-Object RouteMetric, InterfaceMetric | Select-Object -First 1 -Property NextHop, InterfaceIndex | `+
			`ConvertTo-Json -Compress`)
	if err != nil {
		return nil, 0, err
	}
	route, err := parseNetRoute(output)
	if err != nil {
		return nil, 0, err
	}
	if gateway := net.ParseIP(route.NextHop); gateway != nil && !gateway.IsUnspecified() {
		return gateway, route.InterfaceIndex, nil
	}
	return nil, 0, errors.New("у маршрута IPv6 по умолчанию нет шлюза")
}

type netRouteJSON struct {
	NextHop        string `json:"NextHop"`
	InterfaceIndex int    `json:"InterfaceIndex"`
}

func parseNetRoute(output []byte) (netRouteJSON, error) {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return netRouteJSON{}, errors.New("пустой ответ Get-NetRoute")
	}
	// A single object unmarshals directly; several rows come back as an array.
	if strings.HasPrefix(trimmed, "[") {
		var rows []netRouteJSON
		if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
			return netRouteJSON{}, fmt.Errorf("разбор Get-NetRoute: %w", err)
		}
		if len(rows) == 0 {
			return netRouteJSON{}, errors.New("маршрут по умолчанию не найден")
		}
		return rows[0], nil
	}
	var route netRouteJSON
	if err := json.Unmarshal([]byte(trimmed), &route); err != nil {
		return netRouteJSON{}, fmt.Errorf("разбор Get-NetRoute: %w", err)
	}
	if route.InterfaceIndex == 0 {
		return netRouteJSON{}, errors.New("маршрут по умолчанию не найден")
	}
	return route, nil
}

func firstUnicastV4(ifIndex int) string {
	iface, err := net.InterfaceByIndex(ifIndex)
	if err != nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip == nil {
			continue
		}
		if v4 := ip.To4(); v4 != nil && ip.IsGlobalUnicast() {
			return v4.String()
		}
	}
	return ""
}

func setInterfaceAddress(name, cidr string) error {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("некорректный адрес %s: %w", cidr, err)
	}
	ones, _ := network.Mask.Size()
	mask := net.CIDRMask(ones, 32)
	maskIP := net.IP(mask)
	_, err = runCmd(cmdTimeout, "netsh", "interface", "ip", "set", "address",
		"name="+name, "source=static",
		"address="+ip.String(), "mask="+maskIP.String(), "gateway=none")
	if err != nil {
		return fmt.Errorf("адрес %s не назначен: %w", cidr, err)
	}
	return nil
}

func addInterfaceAddressV6(name, cidr string) error {
	if _, err := runCmd(cmdTimeout, "netsh", "interface", "ipv6", "add", "address",
		"interface="+name, "address="+cidr); err != nil {
		return fmt.Errorf("адрес %s не назначен: %w", cidr, err)
	}
	return nil
}

func setInterfaceMTU(name string, mtu int) error {
	if _, err := runCmd(cmdTimeout, "netsh", "interface", "ipv4", "set", "subinterface",
		name, "mtu="+strconv.Itoa(mtu), "store=active"); err != nil {
		return fmt.Errorf("MTU не выставлен: %w", err)
	}
	return nil
}

func setInterfaceMetric(name string, metric int) error {
	if _, err := runCmd(cmdTimeout, "netsh", "interface", "ipv4", "set", "interface",
		name, "metric="+strconv.Itoa(metric)); err != nil {
		return fmt.Errorf("метрика интерфейса не выставлена: %w", err)
	}
	return nil
}

func setInterfaceDNS(name, dns string) error {
	if _, err := runCmd(cmdTimeout, "netsh", "interface", "ip", "set", "dnsservers",
		"name="+name, "source=static", "address="+dns, "register=NONE", "validate=no"); err != nil {
		return fmt.Errorf("DNS не выставлен: %w", err)
	}
	return nil
}

func resetInterfaceDNS(name string) error {
	_, err := runCmd(cmdTimeout, "netsh", "interface", "ip", "set", "dnsservers",
		"name="+name, "source=dhcp")
	return err
}

func addRouteV4(spec routeSpec) error {
	args := []string{"add", spec.dst, "mask", spec.mask, spec.gateway,
		"metric", strconv.Itoa(spec.metric)}
	if spec.ifIndex != 0 {
		args = append(args, "if", strconv.Itoa(spec.ifIndex))
	}
	if spec.persist {
		args = append([]string{"-p"}, args...)
	}
	// Re-adding an existing route fails; delete-then-add keeps Up idempotent
	// across retries and stale state from a killed helper.
	_, _ = runCmd(cmdTimeout, "route", deleteArgsV4(spec.dst, spec.mask)...)
	_, err := runCmd(cmdTimeout, "route", args...)
	return err
}

func deleteArgsV4(dst, mask string) []string {
	return []string{"delete", dst, "mask", mask}
}

func deleteRouteV4(dst, mask string) error {
	_, err := runCmd(cmdTimeout, "route", deleteArgsV4(dst, mask)...)
	return err
}

func addRouteV6(prefix, iface, gateway string, metric int) error {
	ifaceArg := iface
	if ifaceArg == "" {
		ifaceArg = DeviceName
	}
	_, _ = runCmd(cmdTimeout, "netsh", "interface", "ipv6", "delete", "route", prefix, ifaceArg)
	_, err := runCmd(cmdTimeout, "netsh", "interface", "ipv6", "add", "route",
		"prefix="+prefix, "interface="+ifaceArg, "nexthop="+gateway, "metric="+strconv.Itoa(metric))
	return err
}

func addRouteV6Persistent(prefix, gateway string, metric int) error {
	_, _ = runCmd(cmdTimeout, "netsh", "interface", "ipv6", "delete", "route", prefix, DeviceName)
	_, err := runCmd(cmdTimeout, "netsh", "interface", "ipv6", "add", "route",
		"prefix="+prefix, "interface="+DeviceName, "nexthop="+gateway,
		"metric="+strconv.Itoa(metric), "store=persistent")
	return err
}

func deleteRouteV6(prefix, iface string) error {
	if iface == "" {
		_, err := runCmd(cmdTimeout, "route", "-6", "delete", prefix)
		return err
	}
	_, err := runCmd(cmdTimeout, "netsh", "interface", "ipv6", "delete", "route", prefix, iface)
	return err
}

func runPS(ctx context.Context, script string) ([]byte, error) {
	output, err := runCmdContext(ctx, cmdTimeout, "powershell",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		return nil, fmt.Errorf("powershell: %w", err)
	}
	return output, nil
}

func runCmd(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runCmdContext(ctx, timeout, name, args...)
}

func runCmdContext(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	trimmed := []byte(strings.TrimSpace(string(output)))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return trimmed, fmt.Errorf("команда %s превысила таймаут: %w", name, err)
		}
		if len(trimmed) > 0 {
			return trimmed, fmt.Errorf("команда %s: %w: %s", name, err, trimmed)
		}
		return trimmed, fmt.Errorf("команда %s: %w", name, err)
	}
	return trimmed, nil
}
