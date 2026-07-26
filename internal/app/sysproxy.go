package app

import (
	"errors"
	"fmt"

	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/storage"
	"github.com/fife/sa05-desktop/internal/sysproxy"
)

// enableSystemProxy points the desktop at the running core's inbounds.
//
// The previous configuration is snapshotted and persisted before anything changes: this
// machine may already be pointed at another client, and turning SA05 off has to give that
// client its settings back — even after a crash.
func (a *App) enableSystemProxy() error {
	snapshot := a.states.Snapshot()
	if snapshot.Status != state.StatusConnected {
		return errors.New("Сначала подключитесь: системному прокси нужны порты ядра")
	}
	if snapshot.HTTPPort == 0 || snapshot.SocksPort == 0 {
		return errors.New("Ядро не опубликовало порты")
	}
	controller, err := a.proxyController()
	if err != nil {
		return err
	}

	previous, err := controller.Enable(sysproxy.Settings{
		HTTPPort:  snapshot.HTTPPort,
		SocksPort: snapshot.SocksPort,
	})
	if err != nil {
		return err
	}
	if _, err := a.store.Update(func(next *storage.State) {
		next.Toggles.SystemProxy = true
		next.SysProxy = &previous
	}); err != nil {
		// Persisting failed, so nothing would remember how to undo this — revert now.
		_ = controller.Disable(previous)
		return err
	}
	a.states.Update(func(next *state.Snapshot) {
		next.SystemProxyOn = true
		next.Components = upsertComponent(next.Components,
			state.ComponentSysProxy, state.ComponentRunning)
	})
	return nil
}

// disableSystemProxy restores the configuration that existed before SA05 touched it.
func (a *App) disableSystemProxy() error {
	stored, err := a.store.Load()
	if err != nil {
		return err
	}
	if stored.SysProxy != nil {
		controller, err := a.proxyControllerNamed(stored.SysProxy.Backend)
		if err != nil {
			return err
		}
		if err := controller.Disable(*stored.SysProxy); err != nil {
			return err
		}
	}
	if _, err := a.store.Update(func(next *storage.State) {
		next.Toggles.SystemProxy = false
		next.SysProxy = nil
	}); err != nil {
		return err
	}
	a.states.Update(func(next *state.Snapshot) {
		next.SystemProxyOn = false
		next.Components = removeComponent(next.Components, state.ComponentSysProxy)
	})
	return nil
}

// restoreSystemProxy is called at startup: a client that died with the proxy applied
// would otherwise leave the desktop pointing at a port nothing listens on.
func (a *App) restoreSystemProxy() error {
	stored, err := a.store.Load()
	if err != nil {
		return err
	}
	if stored.SysProxy == nil && !stored.Toggles.SystemProxy {
		return nil
	}
	return a.disableSystemProxy()
}

func (a *App) proxyController() (sysproxy.Controller, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.proxy != nil {
		return a.proxy, nil
	}
	controller, err := sysproxy.Detect()
	if err != nil {
		return nil, err
	}
	a.proxy = controller
	return controller, nil
}

// proxyControllerNamed returns the backend that produced a snapshot. Restoring GNOME
// settings through the KDE backend would write nonsense, so a mismatch is an error.
func (a *App) proxyControllerNamed(name string) (sysproxy.Controller, error) {
	controller, err := a.proxyController()
	if err != nil {
		return nil, err
	}
	if name != "" && controller.Name() != name {
		return nil, fmt.Errorf(
			"настройки прокси сохранены для %s, а сейчас доступен %s", name, controller.Name())
	}
	return controller, nil
}

func upsertComponent(
	components []state.ComponentSnapshot,
	component state.Component,
	status state.ComponentStatus,
) []state.ComponentSnapshot {
	for index := range components {
		if components[index].Component == component {
			components[index].Status = status
			return components
		}
	}
	return append(components, state.ComponentSnapshot{Component: component, Status: status})
}

func removeComponent(
	components []state.ComponentSnapshot,
	component state.Component,
) []state.ComponentSnapshot {
	result := components[:0]
	for _, item := range components {
		if item.Component != component {
			result = append(result, item)
		}
	}
	return result
}
