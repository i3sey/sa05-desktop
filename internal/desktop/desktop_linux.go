//go:build linux

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	entryName     = "sa05.desktop"
	autostartName = "sa05-autostart.desktop"
	fileMode      = 0o644
	dirMode       = 0o755
)

// linuxIntegration writes freedesktop.org entries under the user's config directory.
type linuxIntegration struct {
	// configHome is $XDG_CONFIG_HOME (or its default); dataHome is $XDG_DATA_HOME.
	configHome string
	dataHome   string
}

// New returns the session integration for this platform.
func New() (Integration, error) {
	configHome, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("каталог настроек не определён: %w", err)
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("домашний каталог не определён: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return &linuxIntegration{configHome: configHome, dataHome: dataHome}, nil
}

func (l *linuxIntegration) autostartPath() string {
	return filepath.Join(l.configHome, "autostart", autostartName)
}

func (l *linuxIntegration) applicationPath() string {
	return filepath.Join(l.dataHome, "applications", entryName)
}

func (l *linuxIntegration) SetAutostart(enabled bool) error {
	path := l.autostartPath()
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("автозапуск не отключён: %w", err)
		}
		return nil
	}
	executable, err := ExecutablePath()
	if err != nil {
		return err
	}
	// --tray starts minimised: an autostarted client should not steal focus at login.
	entry := desktopEntry(executable, map[string]string{
		"Exec":                      Quote(executable) + " --tray",
		"X-GNOME-Autostart-enabled": "true",
	})
	return writeFile(path, entry)
}

func (l *linuxIntegration) AutostartEnabled() (bool, error) {
	_, err := os.Stat(l.autostartPath())
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("состояние автозапуска не прочитано: %w", err)
}

func (l *linuxIntegration) RegisterURLScheme() error {
	executable, err := ExecutablePath()
	if err != nil {
		return err
	}
	path := l.applicationPath()
	entry := desktopEntry(executable, map[string]string{
		"Exec":      Quote(executable) + " %u",
		"MimeType":  "x-scheme-handler/sa05;",
		"NoDisplay": "false",
	})
	if err := writeFile(path, entry); err != nil {
		return err
	}
	// Best effort: the entry alone works after the next desktop-database refresh, so a
	// missing tool must not fail the whole operation.
	directory := filepath.Dir(path)
	if tool, lookErr := exec.LookPath("update-desktop-database"); lookErr == nil {
		_ = exec.Command(tool, directory).Run()
	}
	if tool, lookErr := exec.LookPath("xdg-mime"); lookErr == nil {
		_ = exec.Command(tool, "default", entryName, "x-scheme-handler/sa05").Run()
	}
	return nil
}

func desktopEntry(executable string, extra map[string]string) string {
	fields := map[string]string{
		"Type":     "Application",
		"Name":     "SA05",
		"Comment":  "Клиент SA05: Xray, системный прокси, TUN и Telegram",
		"Exec":     Quote(executable),
		"Icon":     "sa05",
		"Terminal": "false",
		"Category": "Network;",
	}
	for key, value := range extra {
		fields[key] = value
	}
	order := []string{"Type", "Name", "Comment", "Exec", "Icon", "Terminal", "Category",
		"MimeType", "NoDisplay", "X-GNOME-Autostart-enabled"}
	builder := &strings.Builder{}
	builder.WriteString("[Desktop Entry]\n")
	for _, key := range order {
		if value, ok := fields[key]; ok {
			fmt.Fprintf(builder, "%s=%s\n", key, value)
		}
	}
	return builder.String()
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("каталог %s не создан: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), fileMode); err != nil {
		return fmt.Errorf("файл %s не записан: %w", path, err)
	}
	return nil
}

func defaultExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("путь к программе не определён: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return executable, nil
	}
	return resolved, nil
}
