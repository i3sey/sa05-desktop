//go:build linux

package netbypass

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// control stamps the fwmark on every socket before it connects. The helper's routing
// rule sends marked traffic to the main table, i.e. out through the real interface.
//
// Setting the mark requires CAP_NET_ADMIN on old kernels; on current ones an unprivileged
// process may set SO_MARK for its own sockets. A failure is deliberately ignored: without
// the mark the connection still works (it just goes through the tunnel), and refusing to
// dial would be worse than a suboptimal route.
func control(network, address string, connection syscall.RawConn) error {
	var setErr error
	if err := connection.Control(func(fd uintptr) {
		setErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, Mark)
	}); err != nil {
		return err
	}
	_ = setErr
	return nil
}
