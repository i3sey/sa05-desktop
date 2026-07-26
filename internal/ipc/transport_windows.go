//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// pipeSecurity restricts the control pipe: full access for SYSTEM and Administrators,
// read/write for interactive users. Without an explicit descriptor a named pipe is
// world-writable, which would hand tunnel control to any process on the machine.
const pipeSecurity = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)"

// DefaultEndpoint is the helper's named pipe.
func DefaultEndpoint() string { return `\\.\pipe\sa05-helper` }

// Listen creates the control pipe with the restrictive descriptor above.
func Listen(path string) (net.Listener, error) {
	listener, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurity,
		MessageMode:        false,
		InputBufferSize:    requestLimit,
		OutputBufferSize:   requestLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("канал не создан: %w", err)
	}
	return listener, nil
}

func dial(ctx context.Context, address string, timeout time.Duration) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return winio.DialPipeContext(dialCtx, address)
}

// peerCredentials has no Windows equivalent of SO_PEERCRED. Access is enforced by the
// pipe's security descriptor instead, so the authorizer sees a caller it cannot identify
// and must not make uid-based decisions here.
func peerCredentials(net.Conn) (PeerCredentials, error) {
	return PeerCredentials{}, nil
}
