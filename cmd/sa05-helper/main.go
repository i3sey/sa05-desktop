// Command sa05-helper owns the parts of SA05 that need root: the tunnel device, the
// routing policy and the kill-switch.
//
// It exposes one small IPC surface (see internal/ipc) and nothing else. The GUI runs
// unprivileged and can only ask for the operations enumerated there, with typed
// parameters — never a path, a command line or an interface name of its choosing.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/fife/sa05-desktop/internal/ipc"
	"github.com/fife/sa05-desktop/internal/tun"
)

func main() {
	socketPath := flag.String("socket", ipc.DefaultEndpoint(), "путь к управляющему сокету")
	allowUsers := flag.String("allow", "",
		"пользователи с доступом (имена или uid через запятую); по умолчанию — все обычные учётные записи")
	flag.Parse()

	logger := log.New(os.Stderr, "sa05-helper: ", log.LstdFlags)

	if runServiceIfRequested(logger, socketPath, allowUsers) {
		return
	}

	if !isElevated() {
		logger.Println("предупреждение: хелпер запущен без прав администратора, создание TUN, скорее всего, не удастся")
	}

	authorize, err := buildAuthorizer(*allowUsers)
	if err != nil {
		logger.Fatalf("%v", err)
	}

	// A previous helper may have died with its policy still installed. Reap it
	// now, while no tunnel is supposed to be up, rather than stacking new
	// routes on top of stale ones.
	tun.CleanupStale()

	tunnel := &tun.Tunnel{}
	handler := &helper{tunnel: tunnel, logger: logger}

	listener, err := ipc.Listen(*socketPath)
	if err != nil {
		logger.Fatalf("%v", err)
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &ipc.Server{Handler: handler, Authorize: authorize, Logger: logger}
	logger.Printf("слушаю %s (протокол %d)", *socketPath, ipc.ProtocolVersion)

	serveErr := server.Serve(ctx, listener)

	// Routing must never outlive the helper: a leftover policy would send the machine's
	// traffic into a device that no longer exists.
	if _, err := tunnel.Down(); err != nil {
		logger.Printf("маршрутизация не восстановлена: %v", err)
	}
	if serveErr != nil {
		logger.Fatalf("%v", serveErr)
	}
	logger.Println("остановлен")
}

func buildAuthorizer(allow string) (ipc.Authorizer, error) {
	if allow == "" {
		return ipc.AllowLoginUsers(), nil
	}
	values := []string{}
	for _, item := range splitList(allow) {
		if item != "" {
			values = append(values, item)
		}
	}
	uids, err := ipc.ParseUIDs(values)
	if err != nil {
		return nil, err
	}
	return ipc.AllowUIDs(uids...), nil
}

func splitList(value string) []string {
	result := []string{}
	current := ""
	for _, symbol := range value {
		if symbol == ',' || symbol == ' ' {
			result = append(result, current)
			current = ""
			continue
		}
		current += string(symbol)
	}
	return append(result, current)
}

// helper adapts the tunnel to the IPC handler contract.
type helper struct {
	tunnel *tun.Tunnel
	logger *log.Logger
}

func (h *helper) Status(context.Context) (ipc.Status, error) {
	return toStatus(h.tunnel.State()), nil
}

func (h *helper) TunUp(_ context.Context, request ipc.TunUp) (ipc.Status, error) {
	state, err := h.tunnel.Up(tun.Config{
		SocksPort:       request.SocksPort,
		DNS:             request.DNS,
		AllowIPv6Bypass: request.AllowIPv6Bypass,
		KillSwitch:      request.KillSwitch,
		Mark:            request.BypassMark,
		BypassIPs:       request.BypassIPs,
	})
	if err != nil {
		h.logger.Printf("туннель не поднят: %v", err)
		return ipc.Status{}, fmt.Errorf("туннель не поднят: %w", err)
	}
	h.logger.Printf("туннель поднят: %s -> 127.0.0.1:%d", state.Interface, state.SocksPort)
	return toStatus(state), nil
}

func (h *helper) TunDown(context.Context) (ipc.Status, error) {
	state, err := h.tunnel.Down()
	if err != nil {
		return ipc.Status{}, fmt.Errorf("туннель не снят: %w", err)
	}
	h.logger.Println("туннель снят")
	return toStatus(state), nil
}

func toStatus(state tun.State) ipc.Status {
	return ipc.Status{
		TunUp:     state.Up,
		Interface: state.Interface,
		SocksPort: state.SocksPort,
		Message:   state.Message,
	}
}
