//go:build linux || darwin

package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// socketDirMode keeps the directory readable only by root and the allowed group.
	socketDirMode = 0o750
	// socketMode allows the owning group to connect; the peer's uid is still checked.
	socketMode = 0o660
)

// DefaultEndpoint is the helper socket path.
func DefaultEndpoint() string { return "/run/sa05/helper.sock" }

// Listen creates the helper socket with restrictive permissions, replacing a stale one
// left by a crash.
func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), socketDirMode); err != nil {
		return nil, fmt.Errorf("каталог сокета не создан: %w", err)
	}
	// A stale socket file from a killed helper would make bind fail; nothing is listening
	// on it by definition, because the socket is bound per-process.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("старый сокет не удалён: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("сокет не создан: %w", err)
	}
	if err := os.Chmod(path, socketMode); err != nil {
		listener.Close()
		return nil, fmt.Errorf("права на сокет не выставлены: %w", err)
	}
	return listener, nil
}

func dial(ctx context.Context, address string, timeout time.Duration) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout}
	return dialer.DialContext(ctx, "unix", address)
}

// peerCredentials reads the kernel's view of who is connected. This is the actual
// authentication: it cannot be spoofed by the client, unlike anything sent in-band.
func peerCredentials(connection net.Conn) (PeerCredentials, error) {
	unixConn, ok := connection.(*net.UnixConn)
	if !ok {
		return PeerCredentials{}, fmt.Errorf("соединение не является unix-сокетом")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return PeerCredentials{}, err
	}
	var (
		credentials *unix.Ucred
		innerErr    error
	)
	if err := raw.Control(func(fd uintptr) {
		credentials, innerErr = unix.GetsockoptUcred(
			int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return PeerCredentials{}, err
	}
	if innerErr != nil {
		return PeerCredentials{}, innerErr
	}
	return PeerCredentials{
		UID: credentials.Uid,
		GID: credentials.Gid,
		PID: credentials.Pid,
	}, nil
}
