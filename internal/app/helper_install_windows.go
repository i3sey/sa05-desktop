//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// helperHint tells the UI how to get the privileged component on this platform.
func helperHint() string {
	return "Нужна служба sa05-helper: нажмите «Установить» ниже " +
		"или запустите install-windows.ps1 от имени администратора"
}

// installHelperStep runs the bundled helper with --install-service elevated via
// a UAC prompt. ShellExecute with the runas verb is the documented way to
// elevate a single operation; the helper itself registers and starts the service.
func installHelperStep(_ context.Context) error {
	path, err := helperExecutable()
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	args, err := windows.UTF16PtrFromString("--install-service")
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_HIDE); err != nil {
		return fmt.Errorf("повышение прав отклонено или невозможно: %w", err)
	}
	return nil
}

// helperExecutable finds sa05-helper.exe next to the GUI binary (release layout)
// or on PATH (developer machine).
func helperExecutable() (string, error) {
	if self, err := os.Executable(); err == nil {
		if dir, err := filepath.EvalSymlinks(filepath.Dir(self)); err == nil {
			candidate := filepath.Join(dir, "sa05-helper.exe")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		} else {
			candidate := filepath.Join(filepath.Dir(self), "sa05-helper.exe")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	if path, err := exec.LookPath("sa05-helper.exe"); err == nil {
		return path, nil
	}
	if path, err := exec.LookPath("sa05-helper"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("sa05-helper.exe не найден рядом с программой или в PATH")
}
