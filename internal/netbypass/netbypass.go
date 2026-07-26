// Package netbypass keeps the client's own sockets out of its own tunnel.
//
// Once the TUN device carries the default route, every connection the client makes —
// Xray's outbound, the Telegram proxy's upstream, the subscription fetch — would be
// routed back into the tunnel it is supposed to feed. Marking those sockets (Linux) or
// binding them to the physical interface (Windows) is what breaks the loop.
package netbypass

import (
	"context"
	"net"
)

// Mark is the fwmark the helper's routing policy treats as "leave directly". It must
// match tun.DefaultMark.
const Mark = 0x5a05

// DialFunc is the shape both the Telegram proxy and the probes accept.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Dialer returns a dialer whose connections bypass the tunnel. On platforms where no
// bypass is needed (or possible) it returns a plain dialer, so callers never branch.
func Dialer() DialFunc {
	dialer := &net.Dialer{Control: control}
	return dialer.DialContext
}
