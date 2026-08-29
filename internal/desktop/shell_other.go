//go:build !windows

package desktop

import "context"

// InitBootLog enables boot diagnostics; only used on Windows tray autostart.
func InitBootLog() error { return nil }

// BootLog appends a line to the boot log when enabled.
func BootLog(string) {}

// WaitForShellReady blocks until the desktop shell is ready for tray applications.
func WaitForShellReady(ctx context.Context) {}
