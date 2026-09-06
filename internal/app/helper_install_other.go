//go:build !windows

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fife/sa05-desktop/internal/desktop"
)

// helperHint tells the UI how to get the privileged component on this platform.
func helperHint() string {
	return "Нужен системный компонент: sudo build/install-linux.sh"
}

// installHelperStep runs the installer with elevation. pkexec shows a graphical
// prompt inside a desktop session; the installer itself (install-linux.sh)
// copies the binaries and enables the systemd service.
func installHelperStep(ctx context.Context) error {
	script, err := installScript()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("pkexec"); err != nil {
		return fmt.Errorf("нет pkexec для повышения прав: выполните вручную: sudo %s", script)
	}
	cmd := exec.CommandContext(ctx, "pkexec", "/bin/sh", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("установка не удалась: %w: %s", err, firstLine(output))
	}
	return nil
}

// installScript finds build/install-linux.sh: next to the installed binaries
// (release tarball), under the checkout (developer run), or in the workdir.
func installScript() (string, error) {
	candidates := []string{}
	if self, err := desktop.ExecutablePath(); err == nil {
		dir := filepath.Dir(self)
		candidates = append(candidates,
			filepath.Join(dir, "install-linux.sh"),
			filepath.Join(dir, "build", "install-linux.sh"),
			filepath.Join(dir, "..", "build", "install-linux.sh"),
		)
	}
	if workdir, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(workdir, "build", "install-linux.sh"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			absolute, err := filepath.Abs(candidate)
			if err != nil {
				return candidate, nil
			}
			return absolute, nil
		}
	}
	return "", fmt.Errorf("install-linux.sh не найден: выполните вручную: sudo build/install-linux.sh")
}

func firstLine(output []byte) string {
	for index, symbol := range output {
		if symbol == '\n' {
			return string(output[:index])
		}
	}
	const maxLen = 300
	if len(output) > maxLen {
		return string(output[:maxLen])
	}
	return string(output)
}
