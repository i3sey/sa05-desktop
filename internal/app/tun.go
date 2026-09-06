package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/core/xrayconf"
	"github.com/fife/sa05-desktop/internal/ipc"
	"github.com/fife/sa05-desktop/internal/netbypass"
	"github.com/fife/sa05-desktop/internal/storage"
)

// helperTimeout bounds one helper call. Bringing a tunnel up touches netlink, which is
// fast; a longer wait means something is wrong and the user should hear about it.
const helperTimeout = 20 * time.Second

// enableTun asks the privileged helper to route the machine's traffic into the core.
func (a *App) enableTun(ctx context.Context) error {
	snapshot := a.states.Snapshot()
	if snapshot.Status != state.StatusConnected {
		return errors.New("Сначала подключитесь: туннелю нужен работающий сервер")
	}
	if snapshot.SocksPort == 0 {
		return errors.New("Ядро не опубликовало SOCKS-порт")
	}
	stored, err := a.store.Load()
	if err != nil {
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()

	status, err := a.helper.TunUp(callCtx, ipc.TunUp{
		SocksPort:       snapshot.SocksPort,
		DNS:             "1.1.1.1",
		AllowIPv6Bypass: stored.Toggles.AllowIPv6Bypass,
		KillSwitch:      stored.Toggles.KillSwitch,
		BypassMark:      netbypass.Mark,
		// Windows has no SO_MARK, so the helper needs the server addresses to
		// keep them outside the tunnel via host routes. On Linux they are
		// ignored (the fwmark already exempts the core's sockets).
		BypassIPs: tunnelBypassIPs(stored),
	})
	if err != nil {
		a.states.Update(func(next *state.Snapshot) {
			next.TunOn = false
			next.Components = upsertComponent(next.Components,
				state.ComponentTun, state.ComponentFailed)
		})
		return err
	}
	if _, err := a.store.Update(func(next *storage.State) {
		next.Toggles.Tun = true
	}); err != nil {
		_, _ = a.helper.TunDown(callCtx)
		return err
	}
	a.states.Update(func(next *state.Snapshot) {
		next.TunOn = true
		next.Message = status.Message
		next.Components = upsertComponent(next.Components,
			state.ComponentTun, state.ComponentRunning)
	})
	return nil
}

// disableTun restores the machine's routing. It is called on every path that stops the
// tunnel, including Disconnect and Shutdown: routing must never outlive the core.
func (a *App) disableTun(ctx context.Context) error {
	stored, err := a.store.Load()
	if err != nil {
		return err
	}
	if !stored.Toggles.Tun && !a.states.Snapshot().TunOn {
		return nil
	}

	callCtx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()

	if _, err := a.helper.TunDown(callCtx); err != nil {
		return err
	}
	if _, err := a.store.Update(func(next *storage.State) {
		next.Toggles.Tun = false
	}); err != nil {
		return err
	}
	a.states.Update(func(next *state.Snapshot) {
		next.TunOn = false
		next.Components = removeComponent(next.Components, state.ComponentTun)
	})
	return nil
}

// tunnelBypassIPs lists the active profile's server addresses for the helper's
// host routes (Windows). A profile that fails to parse contributes nothing: the
// tunnel still comes up, it just cannot exempt that server on mark-less systems.
func tunnelBypassIPs(stored storage.State) []string {
	profile := stored.Subscription.ActiveProfile()
	if profile == nil {
		return nil
	}
	hosts, err := xrayconf.ExtractHosts(profile.JSON)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	result := []string{}
	for _, host := range hosts {
		if host.Address == "" || seen[host.Address] {
			continue
		}
		seen[host.Address] = true
		result = append(result, host.Address)
	}
	return result
}

// HelperAvailable reports whether the privileged component is installed and reachable, so
// the UI can explain a disabled TUN toggle instead of failing on click.
func (a *App) HelperAvailable(ctx context.Context) bool {
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return a.helper.Available(callCtx)
}

// installWaitTimeout bounds waiting for the helper after an elevated install:
// service registration plus first listen is seconds, a minute means it failed.
const installWaitTimeout = 90 * time.Second

// InstallHelper installs and starts the privileged component, asking the OS for
// elevation once (UAC on Windows, pkexec on Linux), then waits until it answers.
// A failure keeps the manual instruction from View.HelperHint as the fallback.
func (a *App) InstallHelper(ctx context.Context) error {
	if a.HelperAvailable(ctx) {
		return nil
	}
	stepCtx, cancel := context.WithTimeout(ctx, installWaitTimeout)
	defer cancel()
	if err := installHelperStep(stepCtx); err != nil {
		return err
	}
	deadline := time.Now().Add(installWaitTimeout)
	for {
		if a.HelperAvailable(ctx) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("системный компонент не отвечает после установки")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
