//go:build linux

package sysproxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDesktop records every command and answers `get` from a settings map, so the
// backends can be tested without a real session.
type fakeDesktop struct {
	values   map[string]string
	commands []string
	failOn   string
}

func (f *fakeDesktop) run(name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	f.commands = append(f.commands, line)
	if f.failOn != "" && strings.Contains(line, f.failOn) {
		return "", fmt.Errorf("сбой команды: %s", line)
	}
	switch {
	case name == "gsettings" && len(args) >= 3 && args[0] == "get":
		return f.values[args[1]+"/"+args[2]], nil
	case name == "gsettings" && len(args) >= 4 && args[0] == "set":
		f.values[args[1]+"/"+args[2]] = args[3]
		return "", nil
	case strings.HasPrefix(name, "kreadconfig"):
		return f.values[args[len(args)-1]], nil
	case strings.HasPrefix(name, "kwriteconfig"):
		f.values[args[len(args)-2]] = args[len(args)-1]
		return "", nil
	}
	return "", nil
}

func testSettings() Settings {
	return Settings{HTTPPort: 21809, SocksPort: 21808}
}

func TestGnomeEnableRestoresPreviousConfiguration(t *testing.T) {
	// The machine already has another client's proxy configured; it must come back.
	desktop := &fakeDesktop{values: map[string]string{
		"org.gnome.system.proxy/mode":         "'manual'",
		"org.gnome.system.proxy/ignore-hosts": "['localhost']",
		"org.gnome.system.proxy.http/host":    "'127.0.0.1'",
		"org.gnome.system.proxy.http/port":    "10808",
		"org.gnome.system.proxy.https/host":   "'127.0.0.1'",
		"org.gnome.system.proxy.https/port":   "10808",
		"org.gnome.system.proxy.socks/host":   "'127.0.0.1'",
		"org.gnome.system.proxy.socks/port":   "10808",
	}}
	controller := &gnomeController{exec: desktop.run}

	snapshot, err := controller.Enable(testSettings())
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if desktop.values["org.gnome.system.proxy.http/port"] != "21809" ||
		desktop.values["org.gnome.system.proxy.socks/port"] != "21808" {
		t.Fatalf("порты не применены: %v", desktop.values)
	}
	if desktop.values["org.gnome.system.proxy/mode"] != "'manual'" {
		t.Fatalf("режим = %q", desktop.values["org.gnome.system.proxy/mode"])
	}

	if err := controller.Disable(snapshot); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if desktop.values["org.gnome.system.proxy.http/port"] != "10808" {
		t.Fatalf("чужой прокси не восстановлен: %v", desktop.values)
	}
	if desktop.values["org.gnome.system.proxy/ignore-hosts"] != "['localhost']" {
		t.Fatalf("список исключений не восстановлен: %v", desktop.values)
	}
}

func TestGnomeEnableRollsBackOnFailure(t *testing.T) {
	desktop := &fakeDesktop{
		values: map[string]string{
			"org.gnome.system.proxy/mode":      "'none'",
			"org.gnome.system.proxy.http/port": "0",
		},
		// The mode assignment is last; failing it must undo the port changes.
		failOn: "set org.gnome.system.proxy mode",
	}
	controller := &gnomeController{exec: desktop.run}

	if _, err := controller.Enable(testSettings()); err == nil {
		t.Fatal("сбой команды не привёл к ошибке")
	}
	if desktop.values["org.gnome.system.proxy.http/port"] != "0" {
		t.Fatalf("откат не выполнен: %v", desktop.values)
	}
	if desktop.values["org.gnome.system.proxy/mode"] != "'none'" {
		t.Fatalf("режим изменён при сбое: %v", desktop.values)
	}
}

func TestSnapshotFromAnotherBackendIsRejected(t *testing.T) {
	controller := &gnomeController{exec: (&fakeDesktop{values: map[string]string{}}).run}
	err := controller.Disable(Snapshot{Backend: "kde", Values: map[string]string{}})
	if err == nil {
		t.Fatal("снимок чужого бэкенда принят")
	}
}

func TestKdeEnableWritesProxyEntries(t *testing.T) {
	desktop := &fakeDesktop{values: map[string]string{
		"ProxyType": "0",
		"httpProxy": "",
	}}
	controller := &kdeController{exec: desktop.run}

	snapshot, err := controller.Enable(testSettings())
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if desktop.values["ProxyType"] != "1" {
		t.Fatalf("ProxyType = %q", desktop.values["ProxyType"])
	}
	// KDE separates host and port with a space.
	if desktop.values["httpProxy"] != "http://127.0.0.1 21809" {
		t.Fatalf("httpProxy = %q", desktop.values["httpProxy"])
	}
	if desktop.values["socksProxy"] != "socks://127.0.0.1 21808" {
		t.Fatalf("socksProxy = %q", desktop.values["socksProxy"])
	}

	if err := controller.Disable(snapshot); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if desktop.values["ProxyType"] != "0" {
		t.Fatalf("прокси не выключен: %q", desktop.values["ProxyType"])
	}
}

func TestEnvironmentBackendWritesAndRemovesFile(t *testing.T) {
	root := t.TempDir()
	controller := &environmentController{configHome: root}

	snapshot, err := controller.Enable(testSettings())
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	path := filepath.Join(root, "environment.d", "50-sa05-proxy.conf")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "http_proxy=http://127.0.0.1:21809") ||
		!strings.Contains(content, "all_proxy=socks5://127.0.0.1:21808") {
		t.Fatalf("содержимое файла:\n%s", content)
	}
	if !strings.Contains(content, "no_proxy=localhost,") {
		t.Fatalf("нет списка исключений:\n%s", content)
	}

	if err := controller.Disable(snapshot); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("файл окружения остался")
	}
	// Disabling twice must stay harmless.
	if err := controller.Disable(snapshot); err != nil {
		t.Fatalf("повторное отключение: %v", err)
	}
}

func TestDefaultBypassKeepsLoopbackDirect(t *testing.T) {
	settings := Settings{HTTPPort: 1, SocksPort: 2}
	bypass := strings.Join(settings.bypass(), ",")
	for _, expected := range []string{"localhost", "127.0.0.0/8", "::1"} {
		if !strings.Contains(bypass, expected) {
			t.Fatalf("в исключениях нет %s: %s", expected, bypass)
		}
	}
}
