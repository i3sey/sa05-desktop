//go:build windows

package desktop

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue   = "SA05"
	schemeKey  = `Software\Classes\sa05`
)

// windowsIntegration writes per-user registry entries only: nothing here needs elevation.
type windowsIntegration struct{}

// New returns the session integration for this platform.
func New() (Integration, error) { return &windowsIntegration{}, nil }

func (w *windowsIntegration) SetAutostart(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("ключ автозапуска не открыт: %w", err)
	}
	defer key.Close()

	if !enabled {
		if err := key.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("автозапуск не отключён: %w", err)
		}
		return nil
	}
	executable, err := ExecutablePath()
	if err != nil {
		return err
	}
	// --tray starts minimised: an autostarted client should not steal focus at login.
	command := fmt.Sprintf("%q --tray", executable)
	if err := key.SetStringValue(runValue, command); err != nil {
		return fmt.Errorf("автозапуск не включён: %w", err)
	}
	return nil
}

func (w *windowsIntegration) AutostartEnabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, fmt.Errorf("ключ автозапуска не прочитан: %w", err)
	}
	defer key.Close()

	if _, _, err := key.GetStringValue(runValue); err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, fmt.Errorf("значение автозапуска не прочитано: %w", err)
	}
	return true, nil
}

func (w *windowsIntegration) RegisterURLScheme() error {
	executable, err := ExecutablePath()
	if err != nil {
		return err
	}
	root, _, err := registry.CreateKey(registry.CURRENT_USER, schemeKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("схема sa05:// не зарегистрирована: %w", err)
	}
	defer root.Close()
	if err := root.SetStringValue("", "URL:SA05 Protocol"); err != nil {
		return fmt.Errorf("схема sa05:// не зарегистрирована: %w", err)
	}
	if err := root.SetStringValue("URL Protocol", ""); err != nil {
		return fmt.Errorf("схема sa05:// не зарегистрирована: %w", err)
	}

	command, _, err := registry.CreateKey(registry.CURRENT_USER,
		schemeKey+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("обработчик sa05:// не зарегистрирован: %w", err)
	}
	defer command.Close()
	if err := command.SetStringValue("", fmt.Sprintf("%q \"%%1\"", executable)); err != nil {
		return fmt.Errorf("обработчик sa05:// не зарегистрирован: %w", err)
	}
	return nil
}

func defaultExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("путь к программе не определён: %w", err)
	}
	return filepath.Clean(executable), nil
}
