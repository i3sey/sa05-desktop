//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/fife/sa05-desktop/internal/ipc"
	"github.com/fife/sa05-desktop/internal/tun"
)

// serviceName is the SCM name. It is fixed so install, uninstall and the GUI's
// one-click setup all address the same service.
const serviceName = "sa05-helper"

var (
	installServiceFlag   = flag.Bool("install-service", false, "установить службу Windows и выйти")
	uninstallServiceFlag = flag.Bool("uninstall-service", false, "удалить службу Windows и выйти")
)

// runServiceIfRequested handles the Windows-only entry points: explicit
// install/uninstall and running under the Service Control Manager. It reports
// whether main should return afterwards.
func runServiceIfRequested(logger *log.Logger, socketPath, allowUsers *string) bool {
	isService, err := svc.IsWindowsService()
	if err != nil {
		logger.Fatalf("не удалось определить режим запуска: %v", err)
	}
	switch {
	case *installServiceFlag:
		if err := installService(); err != nil {
			logger.Fatalf("служба не установлена: %v", err)
		}
		logger.Println("служба установлена и запущена")
		return true
	case *uninstallServiceFlag:
		if err := uninstallService(); err != nil {
			logger.Fatalf("служба не удалена: %v", err)
		}
		logger.Println("служба остановлена и удалена")
		return true
	case isService:
		if err := svc.Run(serviceName, &windowsService{
			socketPath: *socketPath,
			allowUsers: *allowUsers,
		}); err != nil {
			logger.Fatalf("служба остановлена с ошибкой: %v", err)
		}
		return true
	}
	return false
}

// isElevated reports whether the process can create a TUN device and edit
// routing, i.e. runs as Administrator (service or elevated terminal).
func isElevated() bool {
	// Opening the SCM with create rights is what install needs; the TUN asserts
	// below need the same Administrator token.
	manager, err := mgr.Connect()
	if err != nil {
		return false
	}
	manager.Disconnect()
	return true
}

func executablePath() string {
	path, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

func installService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("диспетчер служб недоступен (нужны права администратора): %w", err)
	}
	defer manager.Disconnect()

	exists, err := serviceExists(manager)
	if err != nil {
		return err
	}
	if exists {
		if err := startService(manager); err != nil {
			return err
		}
		return nil
	}
	service, err := manager.CreateService(serviceName, executablePath(), mgr.Config{
		DisplayName: "SA05 Helper",
		Description: "Привилегированный компонент SA05: TUN-устройство, маршрутизация, DNS и kill-switch.",
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return fmt.Errorf("служба не создана: %w", err)
	}
	defer service.Close()
	if err := startService(manager); err != nil {
		return err
	}
	return nil
}

func uninstallService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("диспетчер служб недоступен (нужны права администратора): %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("служба не найдена: %w", err)
	}
	defer service.Close()
	if _, err := service.Control(svc.Stop); err == nil {
		time.Sleep(2 * time.Second)
	}
	if err := service.Delete(); err != nil {
		return fmt.Errorf("служба не удалена: %w", err)
	}
	return nil
}

func serviceExists(manager *mgr.Mgr) (bool, error) {
	service, err := manager.OpenService(serviceName)
	if err != nil {
		return false, nil
	}
	service.Close()
	return true, nil
}

func startService(manager *mgr.Mgr) error {
	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("служба не найдена: %w", err)
	}
	defer service.Close()
	if err := service.Start(); err != nil {
		// Already running is fine: the goal (a live service) is achieved.
		if status, queryErr := service.Query(); queryErr == nil && status.State == svc.Running {
			return nil
		}
		return fmt.Errorf("служба не запущена: %w", err)
	}
	return nil
}

// windowsService runs the same IPC server as the interactive mode, translating
// SCM stop/shutdown into context cancellation.
type windowsService struct {
	socketPath string
	allowUsers string
}

func (w *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	logger := log.New(os.Stderr, "sa05-helper: ", log.LstdFlags)
	tun.CleanupStale()

	tunnel := &tun.Tunnel{}
	handler := &helper{tunnel: tunnel, logger: logger}
	authorize, err := buildAuthorizer(w.allowUsers)
	if err != nil {
		logger.Printf("авторизация не настроена: %v", err)
		changes <- svc.Status{State: svc.Stopped}
		return false, 1
	}
	listener, err := ipc.Listen(w.socketPath)
	if err != nil {
		logger.Printf("канал не создан: %v", err)
		changes <- svc.Status{State: svc.Stopped}
		return false, 1
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		for request := range requests {
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				stop()
			}
		}
	}()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}
	logger.Printf("слушаю %s (протокол %d)", w.socketPath, ipc.ProtocolVersion)
	server := &ipc.Server{Handler: handler, Authorize: authorize, Logger: logger}
	serveErr := server.Serve(ctx, listener)
	if _, err := tunnel.Down(); err != nil {
		logger.Printf("маршрутизация не восстановлена: %v", err)
	}
	if serveErr != nil {
		logger.Printf("сервер остановлен: %v", serveErr)
	}
	changes <- svc.Status{State: svc.Stopped}
	return false, 0
}
