package main

import (
	"context"
	"fmt"
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

// start attaches the tray to the already running GUI event loop. On Linux the GTK loop
// must drive systray through RunWithExternalLoop; on Windows the tray HWND and its
// message pump must live on the same thread, so we run systray.Run in its own goroutine.
func (t *tray) start(ctx context.Context) {
	if runtime.GOOS == "windows" {
		go systray.Run(t.onReady, func() {})
	} else {
		begin, end := systray.RunWithExternalLoop(t.onReady, func() {})
		t.stopFn = end
		begin()
	}

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
		return
	}
	systray.Quit()
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
	tooltip := "SA05 — " + presentation.Description
	if snapshot.Status == state.StatusConnected {
		tooltip += fmt.Sprintf("\n↓ %s/с  ↑ %s/с",
			humanBytes(snapshot.RateDown), humanBytes(snapshot.RateUp))
	}
	systray.SetTooltip(tooltip)

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

// humanBytes renders a byte count for the tooltip, matching what the window shows.
func humanBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d Б", value)
	}
	units := []string{"КБ", "МБ", "ГБ", "ТБ"}
	amount := float64(value) / 1024
	index := 0
	for amount >= 1024 && index < len(units)-1 {
		amount /= 1024
		index++
	}
	if amount < 10 {
		return fmt.Sprintf("%.1f %s", amount, units[index])
	}
	return fmt.Sprintf("%.0f %s", amount, units[index])
}

func (t *tray) setIcon(iconState trayicon.State) {
	if runtime.GOOS == "windows" {
		systray.SetIcon(trayicon.ICO(iconState))
		return
	}
	systray.SetIcon(trayicon.PNG(iconState))
}
