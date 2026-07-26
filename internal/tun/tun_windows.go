//go:build windows

package tun

import (
	"errors"
	"sync"
)

// ErrNotImplemented is returned until the Wintun backend lands.
//
// The Windows tunnel needs a different mechanism from Linux end to end: a Wintun adapter
// instead of /dev/net/tun, IP Helper calls instead of netlink, and host routes to the
// server addresses instead of an fwmark (Windows has no SO_MARK). Rather than ship a
// half-working version, the helper builds and runs on Windows and refuses this one
// operation explicitly — everything else (core, system proxy, Telegram) works there.
var ErrNotImplemented = errors.New(
	"TUN на Windows ещё не реализован: используйте системный прокси")

// Tunnel keeps the same shape as the Linux implementation so the helper is
// platform-independent.
type Tunnel struct {
	mutex sync.Mutex
}

// Up always fails on Windows; see ErrNotImplemented.
func (t *Tunnel) Up(Config) (State, error) {
	return State{}, ErrNotImplemented
}

// Down is a no-op: nothing was ever set up, so there is nothing to restore.
func (t *Tunnel) Down() (State, error) {
	return State{}, nil
}

// State reports a tunnel that is never up.
func (t *Tunnel) State() State { return State{} }
