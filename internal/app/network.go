package app

import (
	"context"
	"log"
	"time"

	"github.com/fife/sa05-desktop/internal/core/recovery"
	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/netmon"
	"github.com/fife/sa05-desktop/internal/notify"
)

// Start begins the background work the GUI needs: watching the network and, when the user
// asked for it, bringing the tunnel up at launch.
//
// It is separate from New so the CLI and the tests can use the controller without any
// background activity.
func (a *App) Start(ctx context.Context) {
	a.watchNetwork(ctx)
	a.autoConnect(ctx)
}

// autoConnect honours the "connect at startup" toggle. Without this the toggle was
// nothing but a stored boolean.
func (a *App) autoConnect(ctx context.Context) {
	stored, err := a.store.Load()
	if err != nil {
		log.Printf("настройки не прочитаны: %v", err)
		return
	}
	if !stored.Toggles.AutoConnect || !stored.Subscription.Authorized() {
		return
	}
	go func() {
		if err := a.Connect(ctx); err != nil {
			log.Printf("автоподключение не удалось: %v", err)
			return
		}
		// The toggles the user left on belong to the connection, so they come back with it.
		if stored.Toggles.SystemProxy {
			if err := a.enableSystemProxy(); err != nil {
				log.Printf("системный прокси не восстановлен: %v", err)
			}
		}
		if stored.Toggles.Tun {
			if err := a.enableTun(ctx); err != nil {
				log.Printf("туннель не восстановлен: %v", err)
			}
		}
		if stored.Toggles.Telegram {
			if err := a.enableTelegram(ctx); err != nil {
				log.Printf("Telegram не восстановлен: %v", err)
			}
		}
	}()
}

// watchNetwork reconnects the tunnel when the machine changes network.
//
// A health check cannot see this: after a Wi-Fi to LTE handover the core is alive and its
// SOCKS port still accepts connections, while the route to the server is gone. Only the
// routing environment itself reveals it.
func (a *App) watchNetwork(ctx context.Context) {
	watcher, err := netmon.Watch(ctx)
	if err != nil {
		log.Printf("слежение за сетью недоступно: %v", err)
		return
	}
	go func() {
		defer watcher.Close()
		fingerprint := netmon.Current()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-watcher.Changes():
				if !ok {
					return
				}
				// A single handover emits a burst of events; acting on each one would
				// restart the tunnel several times over.
				if !drain(ctx, watcher.Changes(), netmon.Debounce) {
					return
				}
				fingerprint = a.handleNetworkChange(ctx, fingerprint)
			}
		}
	}()
}

// handleNetworkChange applies the recovery policy and returns the fingerprint to compare
// against next time.
func (a *App) handleNetworkChange(ctx context.Context, previous netmon.Fingerprint) netmon.Fingerprint {
	return a.handleNetworkChangeWith(ctx, previous, netmon.Current())
}

// handleNetworkChangeWith takes the current fingerprint explicitly, so the decisions can
// be exercised for networks the test machine is not actually on.
func (a *App) handleNetworkChangeWith(
	ctx context.Context,
	previous, current netmon.Fingerprint,
) netmon.Fingerprint {
	snapshot := a.states.Snapshot()
	if !snapshot.Requested() {
		return current
	}

	switch recovery.NetworkChanged(string(previous), string(current)) {
	case recovery.DecisionWaitForNetwork:
		a.states.Update(func(next *state.Snapshot) {
			next.Status = state.StatusWaitingForNetwork
			next.FailureKind = state.FailureNetwork
			next.Message = "Сеть пропала, подключение продолжится автоматически"
		})
		a.notifier.Send(notify.KindWarning, "SA05", "Сеть пропала — ждём восстановления")
		return current

	case recovery.DecisionVerifyRoute:
		a.states.Update(func(next *state.Snapshot) {
			next.Status = state.StatusRecovering
			next.Message = "Сеть изменилась, проверяем маршрут"
		})
		// The tunnel may well have survived a benign change (a second interface coming
		// up, a DHCP renew); probing first avoids a needless restart.
		if a.core.Healthy(ctx) && a.routeWorks(ctx) {
			a.states.Update(func(next *state.Snapshot) {
				next.Status = state.StatusConnected
				next.Message = ""
			})
			return current
		}
		if err := a.reconnectAfterNetworkChange(ctx, snapshot); err != nil {
			log.Printf("переподключение после смены сети не удалось: %v", err)
		}
		return current

	default:
		// The tunnel was waiting for a network and one appeared.
		if snapshot.Status == state.StatusWaitingForNetwork && current != netmon.None {
			if err := a.reconnectAfterNetworkChange(ctx, snapshot); err != nil {
				log.Printf("подключение после появления сети не удалось: %v", err)
			}
		}
		return current
	}
}

// reconnectAfterNetworkChange restarts the stack and re-applies the toggles that belong
// to the connection, bounded by the recovery policy.
func (a *App) reconnectAfterNetworkChange(ctx context.Context, previous state.Snapshot) error {
	if recovery.RouteChecked(false, previous.RecoveryAttempt) != recovery.DecisionReconnect {
		a.fail(state.FailureHealthCheck, "Маршрут не восстановился после смены сети")
		a.notifier.Send(notify.KindError, "SA05", "Не удалось восстановить подключение")
		return nil
	}
	stored, err := a.store.Load()
	if err != nil {
		return err
	}

	if err := a.Connect(ctx); err != nil {
		a.notifier.Send(notify.KindError, "SA05", "Не удалось переподключиться: "+err.Error())
		return err
	}
	a.states.Update(func(next *state.Snapshot) {
		next.RecoveryAttempt = previous.RecoveryAttempt + 1
	})

	// Connect() tears the tunnel down with the old core; the routing has to follow the
	// new one or the machine keeps sending traffic to a port that no longer serves it.
	if stored.Toggles.Tun {
		if err := a.enableTun(ctx); err != nil {
			log.Printf("туннель не восстановлен: %v", err)
		}
	}
	if stored.Toggles.SystemProxy {
		if err := a.enableSystemProxy(); err != nil {
			log.Printf("системный прокси не восстановлен: %v", err)
		}
	}
	a.notifier.Send(notify.KindInfo, "SA05", "Подключение восстановлено после смены сети")
	return nil
}

// routeWorks probes the tunnel end to end, which is what distinguishes a live route from
// a core that merely still listens.
func (a *App) routeWorks(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	return a.probeThroughTunnel(probeCtx)
}

// drain waits out a burst of events, returning false if the context ended.
func drain(ctx context.Context, events <-chan struct{}, window time.Duration) bool {
	timer := time.NewTimer(window)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case _, ok := <-events:
			if !ok {
				return false
			}
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(window)
		case <-timer.C:
			return true
		}
	}
}
