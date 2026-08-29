//go:build linux

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newIntegration(t *testing.T) (*linuxIntegration, string) {
	t.Helper()
	root := t.TempDir()
	original := ExecutablePath
	ExecutablePath = func() (string, error) { return "/opt/sa05/sa05 client", nil }
	t.Cleanup(func() { ExecutablePath = original })
	return &linuxIntegration{
		configHome: filepath.Join(root, "config"),
		dataHome:   filepath.Join(root, "data"),
	}, root
}

func TestAutostartCreatesAndRemovesEntry(t *testing.T) {
	integration, _ := newIntegration(t)

	enabled, err := integration.AutostartEnabled()
	if err != nil || enabled {
		t.Fatalf("свежая система: enabled=%v err=%v", enabled, err)
	}
	if err := integration.SetAutostart(true); err != nil {
		t.Fatalf("SetAutostart(true): %v", err)
	}
	enabled, err = integration.AutostartEnabled()
	if err != nil || !enabled {
		t.Fatalf("после включения: enabled=%v err=%v", enabled, err)
	}

	raw, err := os.ReadFile(integration.autostartPath())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	entry := string(raw)
	// A path with a space must survive Exec= parsing.
	if !strings.Contains(entry, `Exec="/opt/sa05/sa05 client" --tray`) {
		t.Fatalf("Exec собран неверно:\n%s", entry)
	}
	if !strings.Contains(entry, "X-GNOME-Autostart-enabled=true") {
		t.Fatalf("нет флага GNOME:\n%s", entry)
	}

	if err := integration.SetAutostart(false); err != nil {
		t.Fatalf("SetAutostart(false): %v", err)
	}
	if _, err := os.Stat(integration.autostartPath()); !os.IsNotExist(err) {
		t.Fatal("файл автозапуска остался")
	}
	// Disabling twice must stay harmless: the UI calls it on every toggle.
	if err := integration.SetAutostart(false); err != nil {
		t.Fatalf("повторное отключение: %v", err)
	}
}

func TestRegisterURLSchemeWritesHandler(t *testing.T) {
	integration, _ := newIntegration(t)
	if err := integration.RegisterURLScheme(); err != nil {
		t.Fatalf("RegisterURLScheme: %v", err)
	}
	raw, err := os.ReadFile(integration.applicationPath())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	entry := string(raw)
	if !strings.Contains(entry, "MimeType=x-scheme-handler/sa05;") {
		t.Fatalf("нет обработчика схемы:\n%s", entry)
	}
	// %u passes the sa05:// link through to the client.
	if !strings.Contains(entry, `Exec="/opt/sa05/sa05 client" %u`) {
		t.Fatalf("Exec без %%u:\n%s", entry)
	}
}
