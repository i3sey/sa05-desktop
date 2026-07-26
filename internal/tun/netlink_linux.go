//go:build linux

package tun

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const (
	// mainTable is the kernel's main routing table, where non-tunnelled traffic stays.
	mainTable = unix.RT_TABLE_MAIN
	// unixRouteBlackhole drops packets instead of routing them; it backs the kill-switch.
	unixRouteBlackhole = unix.RTN_BLACKHOLE
)

// fullMask matches every bit of the fwmark, so the bypass rule reacts to exactly the
// client's mark and not to a coincidental overlap with another tool's marks.
var fullMask uint32 = 0xffffffff

// errExists lets the caller ignore a rule that is already installed.
var errExists = os.ErrExist

// isExist reports whether err means "already there", which is not a failure when a
// previous run left its policy behind.
func isExist(err error) bool {
	return errors.Is(err, os.ErrExist) || errors.Is(err, unix.EEXIST)
}
