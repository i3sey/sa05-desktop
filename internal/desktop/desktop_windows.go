//go:build windows

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath     = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue       = "SA05"
	taskName       = "SA05"
	schemeKey      = `Software\Classes\sa05`
	autostartDelay = "0000:30"
)

// windowsIntegration writes per-user registry entries only: nothing here needs elevation.
type windowsIntegration struct{}

// New returns the session integration for this platform.
func New() (Integration, error) { return &windowsIntegration{}, nil }

func autostartCommand(executable string) string {
	return Quote(executable) + " --tray"
}

func (w *windowsIntegration) SetAutostart(enabled bool) error {
	if !enabled {
		if err := w.deleteScheduledTask(); err != nil {
			return err
		}
		return w.deleteRunKey()
	}
	executable, err := ExecutablePath()
	if err != nil {
		return err
	}
	command := autostartCommand(executable)
	if err := w.createScheduledTask(command); err != nil {
		return err
	}
	return w.setRunKey(command)
}

func (w *windowsIntegration) AutostartEnabled() (bool, error) {
	if ok, err := w.scheduledTaskExists(); err != nil {
		return false, err
	} else if ok {
		return true, nil
	}
	return w.runKeyExists()
}

func (w *windowsIntegration) setRunKey(command string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("ключ автозапуска не открыт: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue(runValue, command); err != nil {
		return fmt.Errorf("автозапуск не включён: %w", err)
	}
	return nil
}

func (w *windowsIntegration) deleteRunKey() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("ключ автозапуска не открыт: %w", err)
	}
	defer key.Close()
	if err := key.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("автозапуск не отключён: %w", err)
	}
	return nil
}

func (w *windowsIntegration) runKeyExists() (bool, error) {
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

func (w *windowsIntegration) createScheduledTask(command string) error {
	output, err := exec.Command(
		"schtasks",
		"/Create", "/F",
		"/TN", taskName,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/DELAY", autostartDelay,
		"/TR", command,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("задача автозапуска не создана: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (w *windowsIntegration) deleteScheduledTask() error {
	output, err := exec.Command("schtasks", "/Delete", "/F", "/TN", taskName).CombinedOutput()
	if err != nil {
		text := strings.ToLower(string(output))
		if strings.Contains(text, "cannot find the file") ||
			strings.Contains(text, "не удается найти") ||
			strings.Contains(text, "не найден") {
			return nil
		}
		return fmt.Errorf("задача автозапуска не удалена: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (w *windowsIntegration) scheduledTaskExists() (bool, error) {
	err := exec.Command("schtasks", "/Query", "/TN", taskName).Run()
	if err != nil {
		return false, nil
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
	handler := Quote(executable) + ` "%1"`
	if err := command.SetStringValue("", handler); err != nil {
		return fmt.Errorf("обработчик sa05:// не зарегистрирован: %w", err)
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
		return filepath.Clean(executable), nil
	}
	return filepath.Clean(resolved), nil
}
