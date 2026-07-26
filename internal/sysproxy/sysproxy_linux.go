//go:build linux

package sysproxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// controllers lists Linux backends in priority order: the desktop's own settings first,
// the login-environment fallback last.
func controllers() []Controller {
	return []Controller{&kdeController{}, &gnomeController{}, &environmentController{}}
}

// runner is swapped in tests so the backends can be exercised without a real desktop.
type runner func(name string, args ...string) (string, error)

func run(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// ---------------------------------------------------------------------------
// GNOME / GLib (gsettings) — also used by Cinnamon, Budgie, XFCE with gsettings.
// ---------------------------------------------------------------------------

type gnomeController struct {
	exec runner
}

var gnomeKeys = []struct{ schema, key string }{
	{"org.gnome.system.proxy", "mode"},
	{"org.gnome.system.proxy", "ignore-hosts"},
	{"org.gnome.system.proxy.http", "host"},
	{"org.gnome.system.proxy.http", "port"},
	{"org.gnome.system.proxy.https", "host"},
	{"org.gnome.system.proxy.https", "port"},
	{"org.gnome.system.proxy.socks", "host"},
	{"org.gnome.system.proxy.socks", "port"},
}

func (g *gnomeController) Name() string { return "gnome" }

func (g *gnomeController) runner() runner {
	if g.exec != nil {
		return g.exec
	}
	return run
}

func (g *gnomeController) Available() bool {
	if _, err := exec.LookPath("gsettings"); err != nil && g.exec == nil {
		return false
	}
	_, err := g.runner()("gsettings", "get", "org.gnome.system.proxy", "mode")
	return err == nil
}

func (g *gnomeController) Enable(settings Settings) (Snapshot, error) {
	snapshot := Snapshot{Backend: g.Name(), Values: map[string]string{}}
	for _, item := range gnomeKeys {
		value, err := g.runner()("gsettings", "get", item.schema, item.key)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Values[item.schema+"/"+item.key] = value
	}

	host := settings.host()
	assignments := [][3]string{
		{"org.gnome.system.proxy.http", "host", quoteGVariant(host)},
		{"org.gnome.system.proxy.http", "port", strconv.Itoa(settings.HTTPPort)},
		{"org.gnome.system.proxy.https", "host", quoteGVariant(host)},
		{"org.gnome.system.proxy.https", "port", strconv.Itoa(settings.HTTPPort)},
		{"org.gnome.system.proxy.socks", "host", quoteGVariant(host)},
		{"org.gnome.system.proxy.socks", "port", strconv.Itoa(settings.SocksPort)},
		{"org.gnome.system.proxy", "ignore-hosts", gvariantList(settings.bypass())},
		// Mode goes last: until it flips to manual the other keys are inert, so a failure
		// half-way through leaves the session on its previous proxy.
		{"org.gnome.system.proxy", "mode", "'manual'"},
	}
	for _, assignment := range assignments {
		if _, err := g.runner()("gsettings", "set", assignment[0], assignment[1], assignment[2]); err != nil {
			// Roll back whatever was already applied so the desktop is never left in a
			// half-configured state.
			_ = g.Disable(snapshot)
			return Snapshot{}, err
		}
	}
	return snapshot, nil
}

func (g *gnomeController) Disable(snapshot Snapshot) error {
	if snapshot.Backend != "" && snapshot.Backend != g.Name() {
		return fmt.Errorf("снимок настроек сделан другим бэкендом (%s)", snapshot.Backend)
	}
	var failure error
	// Restore mode first: the session stops using SA05's ports immediately.
	for _, item := range append([]struct{ schema, key string }{
		{"org.gnome.system.proxy", "mode"},
	}, gnomeKeys...) {
		value, ok := snapshot.Values[item.schema+"/"+item.key]
		if !ok {
			continue
		}
		if _, err := g.runner()("gsettings", "set", item.schema, item.key, value); err != nil && failure == nil {
			failure = err
		}
	}
	return failure
}

// ---------------------------------------------------------------------------
// KDE Plasma (kioslaverc)
// ---------------------------------------------------------------------------

type kdeController struct {
	exec       runner
	configHome string
}

func (k *kdeController) Name() string { return "kde" }

func (k *kdeController) runner() runner {
	if k.exec != nil {
		return k.exec
	}
	return run
}

func (k *kdeController) tool() (string, bool) {
	for _, candidate := range []string{"kwriteconfig6", "kwriteconfig5"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

func (k *kdeController) readTool() (string, bool) {
	for _, candidate := range []string{"kreadconfig6", "kreadconfig5"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

func (k *kdeController) Available() bool {
	if k.exec != nil {
		return true
	}
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + os.Getenv("KDE_FULL_SESSION"))
	if !strings.Contains(desktop, "kde") && !strings.Contains(desktop, "plasma") {
		return false
	}
	_, writable := k.tool()
	return writable
}

var kdeKeys = []string{"ProxyType", "httpProxy", "httpsProxy", "socksProxy", "NoProxyFor"}

func (k *kdeController) Enable(settings Settings) (Snapshot, error) {
	writeTool, ok := k.tool()
	if !ok && k.exec == nil {
		return Snapshot{}, &ErrUnavailable{Reason: "не найден kwriteconfig"}
	}
	if k.exec != nil {
		writeTool = "kwriteconfig6"
	}
	readTool, hasRead := k.readTool()
	if k.exec != nil {
		readTool, hasRead = "kreadconfig6", true
	}

	snapshot := Snapshot{Backend: k.Name(), Values: map[string]string{}}
	if hasRead {
		for _, key := range kdeKeys {
			value, err := k.runner()(readTool, "--file", "kioslaverc",
				"--group", "Proxy Settings", "--key", key)
			if err != nil {
				// A key that was never set reads as an error; treat it as empty so
				// disabling clears it again.
				value = ""
			}
			snapshot.Values[key] = value
		}
	}

	host := settings.host()
	// KDE stores "scheme://host port" — the space is the port separator, not a typo.
	assignments := [][2]string{
		{"httpProxy", fmt.Sprintf("http://%s %d", host, settings.HTTPPort)},
		{"httpsProxy", fmt.Sprintf("http://%s %d", host, settings.HTTPPort)},
		{"socksProxy", fmt.Sprintf("socks://%s %d", host, settings.SocksPort)},
		{"NoProxyFor", strings.Join(settings.bypass(), ",")},
		{"ProxyType", "1"},
	}
	for _, assignment := range assignments {
		if _, err := k.runner()(writeTool, "--file", "kioslaverc",
			"--group", "Proxy Settings", "--key", assignment[0], assignment[1]); err != nil {
			_ = k.Disable(snapshot)
			return Snapshot{}, err
		}
	}
	k.notify()
	return snapshot, nil
}

func (k *kdeController) Disable(snapshot Snapshot) error {
	if snapshot.Backend != "" && snapshot.Backend != k.Name() {
		return fmt.Errorf("снимок настроек сделан другим бэкендом (%s)", snapshot.Backend)
	}
	writeTool, ok := k.tool()
	if !ok && k.exec == nil {
		return &ErrUnavailable{Reason: "не найден kwriteconfig"}
	}
	if k.exec != nil {
		writeTool = "kwriteconfig6"
	}

	var failure error
	// ProxyType first: the session stops proxying before the addresses are cleared.
	for _, key := range append([]string{"ProxyType"}, kdeKeys...) {
		value, ok := snapshot.Values[key]
		if !ok {
			continue
		}
		if value == "" {
			value = "0"
			if key != "ProxyType" {
				value = ""
			}
		}
		if _, err := k.runner()(writeTool, "--file", "kioslaverc",
			"--group", "Proxy Settings", "--key", key, value); err != nil && failure == nil {
			failure = err
		}
	}
	k.notify()
	return failure
}

// notify asks running KDE applications to re-read the proxy configuration.
func (k *kdeController) notify() {
	if k.exec != nil {
		return
	}
	if tool, err := exec.LookPath("dbus-send"); err == nil {
		_ = exec.Command(tool, "--type=signal", "/KIO/Scheduler",
			"org.kde.KIO.Scheduler.reparseSlaveConfiguration", "string:").Run()
	}
}

// ---------------------------------------------------------------------------
// Fallback: login environment (systemd environment.d)
// ---------------------------------------------------------------------------

type environmentController struct {
	configHome string
}

func (e *environmentController) Name() string { return "environment" }

func (e *environmentController) Available() bool { return true }

func (e *environmentController) path() (string, error) {
	base := e.configHome
	if base == "" {
		resolved, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("каталог настроек не определён: %w", err)
		}
		base = resolved
	}
	return filepath.Join(base, "environment.d", "50-sa05-proxy.conf"), nil
}

func (e *environmentController) Enable(settings Settings) (Snapshot, error) {
	path, err := e.path()
	if err != nil {
		return Snapshot{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Snapshot{}, fmt.Errorf("каталог %s не создан: %w", filepath.Dir(path), err)
	}
	host := settings.host()
	content := fmt.Sprintf(`# Записано SA05. Файл читается при входе в сеанс.
http_proxy=http://%s:%d
https_proxy=http://%s:%d
all_proxy=socks5://%s:%d
no_proxy=%s
`, host, settings.HTTPPort, host, settings.HTTPPort, host, settings.SocksPort,
		strings.Join(settings.bypass(), ","))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return Snapshot{}, fmt.Errorf("файл окружения не записан: %w", err)
	}
	return Snapshot{Backend: e.Name(), Values: map[string]string{"path": path}}, nil
}

func (e *environmentController) Disable(snapshot Snapshot) error {
	path := snapshot.Values["path"]
	if path == "" {
		resolved, err := e.path()
		if err != nil {
			return err
		}
		path = resolved
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("файл окружения не удалён: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------

func quoteGVariant(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `\'`) + "'"
}

func gvariantList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quoteGVariant(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
