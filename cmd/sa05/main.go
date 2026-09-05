// Command sa05 is the desktop client window.
//
// The window is a thin shell: every decision lives in internal/app, which the frontend
// reaches through the bound methods below. State changes are pushed as "state" events so
// the UI reflects a reconnect it did not initiate.
package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/fife/sa05-desktop/internal/app"
	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/desktop"
	"github.com/fife/sa05-desktop/internal/storage"
)

//go:embed all:dist
var assets embed.FS

// The window icon is the product logo; the tray keeps its own state glyph, because a
// 512-px logo scaled to a 22-px panel is an unreadable smudge.
//
//go:embed icon.png
var windowIcon []byte

// version is set at link time by the release workflow; a local build stays "dev", which
// is what tells the updater there is nothing to compare against.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ошибка: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// WebKitGTK + Wayland + NVIDIA (Hyprland): DMABUF-рендерер падает с
	// "Error 71 dispatching to Wayland display" сразу при старте.
	// Отключаем его до инициализации WebKit, если пользователь явно не задал иначе.
	if os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") == "" {
		os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
	app.Version = version

	store, err := storage.Open("")
	if err != nil {
		return err
	}
	assetDir, err := storage.DefaultAssetDir()
	if err != nil {
		return err
	}
	controller := app.New(store, assetDir)
	defer controller.Shutdown()

	binding := &App{controller: controller}
	// Arguments: a deep link (sa05://add/<encoded-https-url>) and/or --tray, which is how
	// the autostart entry launches the client without stealing focus at login.
	for _, argument := range os.Args[1:] {
		if argument == "--tray" {
			binding.startHidden = true
			continue
		}
		binding.pendingLink = argument
	}

	if binding.startHidden {
		if err := desktop.InitBootLog(); err != nil {
			log.Printf("журнал автозапуска не открыт: %v", err)
		}
		desktop.BootLog("запуск с --tray")
		shellCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		desktop.WaitForShellReady(shellCtx)
		cancel()
		desktop.BootLog("оболочка готова, открываем GUI")
	}

	return wails.Run(&options.App{
		Title:  "SA05",
		Width:  420,
		Height: 620,
		// Compact panel, not a resizable dashboard: everything fits without scrolling.
		MinWidth:          380,
		MinHeight:         520,
		StartHidden:       binding.startHidden,
		HideWindowOnClose: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  binding.startup,
		OnShutdown: binding.shutdown,
		Bind:       []any{binding},
		Linux: &linux.Options{
			ProgramName: "sa05",
			Icon:        windowIcon,
		},
		BackgroundColour: &options.RGBA{R: 250, G: 248, B: 242, A: 1},
	})
}

// App is the object Wails exposes to the frontend as window.go.main.App.
type App struct {
	controller  *app.App
	ctx         context.Context
	unsubscribe func()
	pendingLink string
	startHidden bool
	tray        *tray
}

func (b *App) startup(ctx context.Context) {
	b.ctx = ctx
	// Push every state transition; the frontend re-reads the full view on each one.
	b.unsubscribe = b.controller.Subscribe(func(snapshot state.Snapshot) {
		wailsruntime.EventsEmit(ctx, "state", snapshot)
	})
	// Best effort: an unregistered scheme only costs deep links, not the client.
	if err := b.controller.RegisterURLScheme(); err != nil {
		log.Printf("схема sa05:// не зарегистрирована: %v", err)
	}
	b.tray = &tray{
		controller: b.controller,
		show: func() {
			wailsruntime.WindowShow(ctx)
			wailsruntime.WindowUnminimise(ctx)
		},
		quit: func() { wailsruntime.Quit(ctx) },
	}
	b.tray.start(ctx)
	// Background work starts only with the GUI: the CLI and the tests drive the controller
	// directly and must not get a network watcher they did not ask for.
	b.controller.Start(ctx)
	if b.pendingLink != "" {
		link := b.pendingLink
		b.pendingLink = ""
		go func() {
			if err := b.controller.ImportSubscription(ctx, link); err != nil {
				log.Printf("не удалось импортировать ссылку: %v", err)
			}
		}()
	}
}

func (b *App) shutdown(context.Context) {
	if b.tray != nil {
		b.tray.stop()
	}
	if b.unsubscribe != nil {
		b.unsubscribe()
	}
	b.controller.Shutdown()
}

// View returns the whole UI model.
func (b *App) View() (app.View, error) { return b.controller.View() }

// Import adds or refreshes the subscription.
func (b *App) Import(url string) error {
	return b.controller.ImportSubscription(b.context(), url)
}

// Connect starts the core on the active profile.
func (b *App) Connect() error { return b.controller.Connect(b.context()) }

// Disconnect stops the core.
func (b *App) Disconnect() { b.controller.Disconnect() }

// SelectProfile switches the active server.
func (b *App) SelectProfile(id string) error {
	return b.controller.SelectProfile(b.context(), id)
}

// SelectFastest measures every profile and switches to the quickest.
func (b *App) SelectFastest() (string, error) {
	return b.controller.SelectFastest(b.context())
}

// PingProfiles measures every profile.
func (b *App) PingProfiles() ([]app.ProfileView, error) {
	return b.controller.PingProfiles(b.context())
}

// Toggle flips one of the switches.
func (b *App) Toggle(name string, enabled bool) error {
	return b.controller.Toggle(b.context(), name, enabled)
}

// CheckUpdate asks the release feed for a newer version.
func (b *App) CheckUpdate() (app.UpdateView, error) {
	return b.controller.CheckUpdate(b.context())
}

// InstallUpdate installs the update found by the last check.
func (b *App) InstallUpdate() (app.UpdateView, error) {
	return b.controller.InstallUpdate(b.context())
}

// Diagnose runs the connectivity checks and returns their verdict.
func (b *App) Diagnose() (app.DiagnosticsReport, error) {
	return b.controller.Diagnose(b.context())
}

// TelegramLink returns the tg:// link for the built-in MTProto proxy.
func (b *App) TelegramLink() (string, error) { return b.controller.TelegramLink() }

// SetTelegramTransport records the upstream the MTProto proxy should use.
func (b *App) SetTelegramTransport(value string) error {
	return b.controller.SetTelegramTransport(b.context(), value)
}

// SetTheme records the UI appearance: auto, light or dark.
func (b *App) SetTheme(value string) error {
	return b.controller.SetTheme(b.context(), value)
}

// OpenURL hands a link to the desktop's default handler.
func (b *App) OpenURL(url string) {
	wailsruntime.BrowserOpenURL(b.context(), url)
}

// Quit closes the application.
func (b *App) Quit() { wailsruntime.Quit(b.context()) }

func (b *App) context() context.Context {
	if b.ctx != nil {
		return b.ctx
	}
	return context.Background()
}
