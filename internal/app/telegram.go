package app

import (
	"context"

	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/netbypass"
	"github.com/fife/sa05-desktop/internal/storage"
	"github.com/fife/sa05-desktop/internal/tgws"
)

// enableTelegram starts the built-in MTProto proxy.
//
// It is deliberately independent of the tunnel: Telegram works without a subscription and
// without the VPN being up, which is the one thing users need most when everything else
// is blocked.
func (a *App) enableTelegram(ctx context.Context) error {
	stored, err := a.store.Load()
	if err != nil {
		return err
	}
	secret, err := tgws.EnsureSecret(stored.Telegram.Secret)
	if err != nil {
		return err
	}
	if secret != stored.Telegram.Secret {
		if _, err := a.store.Update(func(next *storage.State) {
			next.Telegram.Secret = secret
		}); err != nil {
			return err
		}
	}
	cacheDir, err := storage.DefaultCacheDir()
	if err != nil {
		return err
	}

	// Not ctx: the proxy outlives the UI call that started it.
	if err := a.telegram.Start(a.rootCtx, tgws.Settings{
		Port:             a.telegramPort,
		Secret:           secret,
		Transport:        tgws.ParseTransport(stored.Telegram.Transport),
		CloudflareDomain: stored.Telegram.CloudflareDomain,
		CacheDir:         cacheDir,
		// The proxy's upstream must leave through the real interface: routing it into the
		// SA05 tunnel would defeat the point of having a separate Telegram path.
		Dial: netbypass.Dialer(),
	}); err != nil {
		a.states.Update(func(next *state.Snapshot) {
			next.TelegramOn = false
			next.Components = upsertComponent(next.Components,
				state.ComponentTelegram, state.ComponentFailed)
		})
		return err
	}
	if _, err := a.store.Update(func(next *storage.State) {
		next.Toggles.Telegram = true
	}); err != nil {
		a.telegram.Stop()
		return err
	}
	a.states.Update(func(next *state.Snapshot) {
		next.TelegramOn = true
		next.Components = upsertComponent(next.Components,
			state.ComponentTelegram, state.ComponentRunning)
	})
	return nil
}

// disableTelegram stops the proxy and records the toggle.
func (a *App) disableTelegram() error {
	a.telegram.Stop()
	if _, err := a.store.Update(func(next *storage.State) {
		next.Toggles.Telegram = false
	}); err != nil {
		return err
	}
	a.states.Update(func(next *state.Snapshot) {
		next.TelegramOn = false
		next.Components = removeComponent(next.Components, state.ComponentTelegram)
	})
	return nil
}

// restartTelegramIfRunning re-applies settings without changing the port or the secret, so
// Telegram itself never needs reconfiguring after a transport change.
func (a *App) restartTelegramIfRunning(ctx context.Context) error {
	if !a.telegram.Running() {
		return nil
	}
	return a.enableTelegram(ctx)
}
