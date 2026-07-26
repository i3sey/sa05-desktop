package main

import (
	"context"
	"log"
	"runtime"

	"fyne.io/systray"

	"github.com/fife/sa05-desktop/internal/app"
	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/trayicon"
)

// tray is the status indicator: it mirrors the connection state and offers the two
// actions worth having without opening the window.
type tray struct {
	controller *app.App
	show       func()
	quit       func()

	connect *systray.MenuItem
	status  *systray.MenuItem
	stopFn  func()
	cancel  func()
}

// start attaches the tray to the already running GUI event loop. systray owns a platform
// menu, so it must be driven by the same loop Wails started rather than its own.
func (t *tray) start(ctx context.Context) {
	begin, end := systray.RunWithExternalLoop(t.onReady, func() {})
	t.stopFn = end
	begin()

	watchCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	unsubscribe := t.controller.Subscribe(func(snapshot state.Snapshot) {
		t.render(snapshot)
	})
	go func() {
		<-watchCtx.Done()
		unsubscribe()
	}()
}

func (t *tray) stop() {
	if t.cancel != nil {
		t.cancel()
	}
	if t.stopFn != nil {
		t.stopFn()
	}
}

func (t *tray) onReady() {
	t.setIcon(trayicon.StateIdle)
	systray.SetTitle("SA05")
	systray.SetTooltip("SA05 — отключено")

	t.status = systray.AddMenuItem("Отключено", "")
	t.status.Disable()
	systray.AddSeparator()
	t.connect = systray.AddMenuItem("Подключить", "")
	open := systray.AddMenuItem("Открыть окно", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Выход", "")

	go func() {
		for {
			select {
			case <-t.connect.ClickedCh:
				t.toggleConnection()
			case <-open.ClickedCh:
				t.show()
			case <-quit.ClickedCh:
				t.quit()
				return
			}
		}
	}()
	t.render(t.controller.Snapshot())
}

func (t *tray) toggleConnection() {
	if t.controller.Snapshot().Requested() {
		t.controller.Disconnect()
		return
	}
	if err := t.controller.Connect(context.Background()); err != nil {
		log.Printf("подключение из трея не удалось: %v", err)
	}
}

func (t *tray) render(snapshot state.Snapshot) {
	if t.status == nil || t.connect == nil {
		return
	}
	presentation := state.Present(snapshot)
	t.status.SetTitle(presentation.Title)
	systray.SetTooltip("SA05 — " + presentation.Description)

	switch snapshot.Status {
	case state.StatusConnected:
		t.setIcon(trayicon.StateActive)
		t.connect.SetTitle("Отключить")
	case state.StatusError, state.StatusWaitingForNetwork:
		t.setIcon(trayicon.StateError)
		t.connect.SetTitle("Повторить")
	case state.StatusConnecting, state.StatusRecovering:
		t.setIcon(trayicon.StateActive)
		t.connect.SetTitle("Отключить")
	default:
		t.setIcon(trayicon.StateIdle)
		t.connect.SetTitle("Подключить")
	}
}

func (t *tray) setIcon(iconState trayicon.State) {
	if runtime.GOOS == "windows" {
		systray.SetIcon(trayicon.ICO(iconState))
		return
	}
	systray.SetIcon(trayicon.PNG(iconState))
}
