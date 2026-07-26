//go:build !linux

package netbypass

import "syscall"

// control is a no-op outside Linux. Windows has no fwmark: the bypass there is host
// routes to the server addresses, installed by the helper, so the socket itself needs no
// special treatment.
func control(network, address string, connection syscall.RawConn) error { return nil }
