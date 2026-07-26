package app

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/fife/sa05-desktop/internal/notify"
	"github.com/fife/sa05-desktop/internal/update"
)

// UpdateView is what the UI shows about updates.
type UpdateView struct {
	// Current is the running version, "dev" for a local build.
	Current string `json:"current"`
	// Available is the version offered, empty when the client is current.
	Available string `json:"available"`
	Notes     string `json:"notes"`
	// Installed means the new binaries are in place and a restart applies them.
	Installed bool `json:"installed"`
	// Error explains why a check or an install did not happen.
	Error string `json:"error"`
	// Checking is true while a check is in flight.
	Checking bool `json:"checking"`
}

// Version is the build's version, set at link time by the release workflow. A development
// build keeps "dev", which disables updates: there is nothing to compare against.
var Version = "dev"

// CheckUpdate asks the release feed whether a newer version exists.
func (a *App) CheckUpdate(ctx context.Context) (UpdateView, error) {
	view := UpdateView{Current: Version}
	if Version == "dev" {
		view.Error = "Сборка для разработки: обновления не проверяются"
		return view, nil
	}

	checker := &update.Checker{Current: Version}
	available, ok, err := checker.Check(ctx)
	if err != nil {
		view.Error = err.Error()
		return view, err
	}
	if !ok {
		return view, nil
	}

	a.mutex.Lock()
	a.pendingUpdate = &available
	a.mutex.Unlock()

	view.Available = available.Version.String()
	view.Notes = available.Notes
	return view, nil
}

// InstallUpdate downloads and installs the update found by the last check.
//
// The running process keeps executing the old image, so the client reports that a restart
// is needed rather than pretending the new version is live.
func (a *App) InstallUpdate(ctx context.Context) (UpdateView, error) {
	a.mutex.Lock()
	pending := a.pendingUpdate
	a.mutex.Unlock()

	view := UpdateView{Current: Version}
	if pending == nil {
		view.Error = "Сначала проверьте обновления"
		return view, errors.New(view.Error)
	}
	view.Available = pending.Version.String()

	key, err := update.PublicKey()
	if err != nil {
		view.Error = err.Error()
		return view, err
	}
	installer := &update.Installer{PublicKey: key}
	if err := installer.Install(ctx, *pending); err != nil {
		view.Error = err.Error()
		return view, err
	}

	view.Installed = true
	a.notifier.Send(notify.KindInfo, "SA05",
		"Обновление "+view.Available+" установлено, перезапустите клиент")
	return view, nil
}

// checkUpdateInBackground looks for a new version at startup when the user allowed it.
// A failure is logged and forgotten: an unreachable feed must not disturb anything.
func (a *App) checkUpdateInBackground(ctx context.Context) {
	stored, err := a.store.Load()
	if err != nil || !stored.Toggles.AutoUpdate || Version == "dev" {
		return
	}
	go func() {
		// Not at the very first second: the tunnel and the UI matter more than a check.
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}
		view, err := a.CheckUpdate(ctx)
		if err != nil {
			log.Printf("проверка обновлений не удалась: %v", err)
			return
		}
		if view.Available != "" {
			a.notifier.Send(notify.KindInfo, "SA05",
				"Доступна версия "+view.Available+" — обновите в настройках")
		}
	}()
}
