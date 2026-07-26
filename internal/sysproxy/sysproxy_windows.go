//go:build windows

package sysproxy

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const settingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// controllers lists Windows backends. WinINET settings are what browsers and most
// applications read.
func controllers() []Controller { return []Controller{&wininetController{}} }

type wininetController struct{}

func (w *wininetController) Name() string { return "wininet" }

func (w *wininetController) Available() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, settingsKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	key.Close()
	return true
}

var wininetValues = []string{"ProxyEnable", "ProxyServer", "ProxyOverride", "AutoConfigURL"}

func (w *wininetController) Enable(settings Settings) (Snapshot, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, settingsKey,
		registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return Snapshot{}, fmt.Errorf("настройки интернета не открыты: %w", err)
	}
	defer key.Close()

	snapshot := Snapshot{Backend: w.Name(), Values: map[string]string{}}
	for _, name := range wininetValues {
		snapshot.Values[name] = readValue(key, name)
	}

	host := settings.host()
	// WinINET takes one string for every scheme; SOCKS is listed separately so
	// applications that speak it can use the faster path.
	server := fmt.Sprintf("http=%s:%d;https=%s:%d;socks=%s:%d",
		host, settings.HTTPPort, host, settings.HTTPPort, host, settings.SocksPort)
	override := strings.Join(append(settings.bypass(), "<local>"), ";")

	if err := key.SetStringValue("ProxyServer", server); err != nil {
		return Snapshot{}, fmt.Errorf("адрес прокси не записан: %w", err)
	}
	if err := key.SetStringValue("ProxyOverride", override); err != nil {
		return Snapshot{}, fmt.Errorf("исключения прокси не записаны: %w", err)
	}
	// An auto-config URL wins over the manual settings, so it must go while SA05 is on.
	if err := key.DeleteValue("AutoConfigURL"); err != nil && err != registry.ErrNotExist {
		return Snapshot{}, fmt.Errorf("автонастройка прокси не отключена: %w", err)
	}
	// Enable last: until this flips, the session keeps its previous configuration.
	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		_ = w.Disable(snapshot)
		return Snapshot{}, fmt.Errorf("прокси не включён: %w", err)
	}
	refresh()
	return snapshot, nil
}

func (w *wininetController) Disable(snapshot Snapshot) error {
	if snapshot.Backend != "" && snapshot.Backend != w.Name() {
		return fmt.Errorf("снимок настроек сделан другим бэкендом (%s)", snapshot.Backend)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, settingsKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("настройки интернета не открыты: %w", err)
	}
	defer key.Close()

	var failure error
	// Turn proxying off first, then restore the previous values verbatim.
	if previous, ok := snapshot.Values["ProxyEnable"]; ok && previous == "1" {
		failure = key.SetDWordValue("ProxyEnable", 1)
	} else {
		failure = key.SetDWordValue("ProxyEnable", 0)
	}
	for _, name := range []string{"ProxyServer", "ProxyOverride", "AutoConfigURL"} {
		value, ok := snapshot.Values[name]
		if !ok || value == "" {
			if err := key.DeleteValue(name); err != nil && err != registry.ErrNotExist && failure == nil {
				failure = err
			}
			continue
		}
		if err := key.SetStringValue(name, value); err != nil && failure == nil {
			failure = err
		}
	}
	refresh()
	return failure
}

func readValue(key registry.Key, name string) string {
	if value, _, err := key.GetStringValue(name); err == nil {
		return value
	}
	if value, _, err := key.GetIntegerValue(name); err == nil {
		return fmt.Sprint(value)
	}
	return ""
}

// refresh tells running applications to re-read the settings; without it browsers keep
// using the old proxy until they restart.
func refresh() {
	const (
		internetOptionSettingsChanged = 39
		internetOptionRefresh         = 37
	)
	wininet := syscall.NewLazyDLL("wininet.dll")
	setOption := wininet.NewProc("InternetSetOptionW")
	setOption.Call(0, internetOptionSettingsChanged, uintptr(unsafe.Pointer(nil)), 0)
	setOption.Call(0, internetOptionRefresh, uintptr(unsafe.Pointer(nil)), 0)
}
