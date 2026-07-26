// Package ipc is the contract between the unprivileged client and the privileged helper.
//
// The helper runs as root, so this protocol is a security boundary: it accepts a fixed
// set of methods with typed parameters and never takes a path, a command line or a shell
// string from the client. Anything the helper can be asked to do is enumerated here.
package ipc

import "fmt"

// ProtocolVersion is bumped whenever the request or response shape changes. Both sides
// refuse to talk across versions, so a stale helper can never half-apply a new request.
const ProtocolVersion = 1

// Method names the operation a request performs.
type Method string

const (
	// MethodHello negotiates the protocol version and reports helper state.
	MethodHello Method = "hello"
	// MethodStatus reports whether the tunnel device is up.
	MethodStatus Method = "status"
	// MethodTunUp creates the TUN device and routes traffic into the client's SOCKS port.
	MethodTunUp Method = "tun_up"
	// MethodTunDown removes the device and restores routing.
	MethodTunDown Method = "tun_down"
)

// Request is one client call.
type Request struct {
	ID     uint64 `json:"id"`
	Method Method `json:"method"`
	TunUp  *TunUp `json:"tunUp,omitempty"`
	Hello  *Hello `json:"hello,omitempty"`
}

// Hello carries the client's protocol version.
type Hello struct {
	Version int `json:"version"`
}

// TunUp describes the tunnel the helper must set up.
//
// Note what is absent: no binary path, no arguments, no interface name of the client's
// choosing. The helper decides those, so a compromised client cannot turn the helper into
// a way to run code as root.
type TunUp struct {
	// SocksPort is the client's loopback SOCKS inbound; the helper only ever dials
	// 127.0.0.1 on this port.
	SocksPort int `json:"socksPort"`
	// DNS is the resolver advertised on the tunnel interface.
	DNS string `json:"dns"`
	// AllowIPv6Bypass hands IPv6 back to the system instead of blackholing it. Off means
	// AAAA traffic enters the tunnel and is dropped, which is what stops the leak.
	AllowIPv6Bypass bool `json:"allowIpv6Bypass"`
	// KillSwitch keeps traffic blocked when the client's core stops answering.
	KillSwitch bool `json:"killSwitch"`
	// BypassMark is the fwmark the client sets on its own sockets so they leave the
	// machine directly instead of looping into the tunnel.
	BypassMark uint32 `json:"bypassMark"`
}

// Response is one helper reply.
type Response struct {
	ID     uint64  `json:"id"`
	OK     bool    `json:"ok"`
	Error  string  `json:"error,omitempty"`
	Status *Status `json:"status,omitempty"`
}

// Status is the helper's view of the tunnel.
type Status struct {
	Version   int    `json:"version"`
	TunUp     bool   `json:"tunUp"`
	Interface string `json:"interface"`
	SocksPort int    `json:"socksPort"`
	// Message explains a degraded state, e.g. a kill-switch that is currently blocking.
	Message string `json:"message"`
}

// Validate rejects malformed requests before the helper acts on them.
func (r Request) Validate() error {
	switch r.Method {
	case MethodHello:
		if r.Hello == nil {
			return fmt.Errorf("метод %s без параметров", r.Method)
		}
		if r.Hello.Version <= 0 {
			return fmt.Errorf("некорректная версия протокола: %d", r.Hello.Version)
		}
	case MethodStatus, MethodTunDown:
		// No parameters.
	case MethodTunUp:
		if r.TunUp == nil {
			return fmt.Errorf("метод %s без параметров", r.Method)
		}
		return r.TunUp.Validate()
	default:
		return fmt.Errorf("неизвестный метод %q", r.Method)
	}
	return nil
}

// Validate checks the tunnel parameters. The helper runs as root, so every field is
// bounded here rather than trusted.
func (t TunUp) Validate() error {
	if t.SocksPort < 1 || t.SocksPort > 65535 {
		return fmt.Errorf("некорректный порт SOCKS: %d", t.SocksPort)
	}
	if t.DNS != "" && !isIPv4(t.DNS) {
		return fmt.Errorf("некорректный адрес DNS: %q", t.DNS)
	}
	return nil
}

// isIPv4 accepts only a dotted-quad literal: the DNS value ends up in a routing table, so
// a hostname or a shell-looking string must never reach it.
func isIPv4(value string) bool {
	octets := 0
	number := -1
	for index := 0; index <= len(value); index++ {
		if index == len(value) || value[index] == '.' {
			if number < 0 || number > 255 {
				return false
			}
			octets++
			number = -1
			continue
		}
		digit := value[index]
		if digit < '0' || digit > '9' {
			return false
		}
		if number < 0 {
			number = 0
		}
		number = number*10 + int(digit-'0')
		if number > 255 {
			return false
		}
	}
	return octets == 4
}
