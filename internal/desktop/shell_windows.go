//go:build windows

package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const shellWaitInterval = 500 * time.Millisecond

var (
	bootLogMu   sync.Mutex
	bootLogPath string
)

// InitBootLog opens the per-user boot log used when the client is autostarted with --tray.
func InitBootLog() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	bootLogMu.Lock()
	bootLogPath = filepath.Join(configDir, "sa05", "boot.log")
	bootLogMu.Unlock()
	return appendBootLog("--- session " + time.Now().Format(time.RFC3339) + " ---")
}

// BootLog appends a diagnostic line to the boot log.
func BootLog(message string) {
	if err := appendBootLog(message); err != nil {
		// Best effort: logging must not block startup.
		_, _ = os.Stderr.WriteString("boot log: " + err.Error() + "\n")
	}
}

func appendBootLog(message string) error {
	bootLogMu.Lock()
	path := bootLogPath
	bootLogMu.Unlock()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(fmt.Sprintf("%s %s\n", time.Now().Format("15:04:05"), message))
	return err
}

// WaitForShellReady blocks until explorer.exe is running or the context ends.
func WaitForShellReady(ctx context.Context) {
	for {
		if explorerRunning() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(shellWaitInterval):
		}
	}
}

func explorerRunning() bool {
	output, err := exec.Command("tasklist", "/FI", "IMAGENAME eq explorer.exe", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(output)), "explorer.exe")
}
