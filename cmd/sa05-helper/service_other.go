//go:build !windows

package main

import (
	"log"
	"os"
)

// runServiceIfRequested is a no-op outside Windows: the helper there runs under
// systemd (see packaging/systemd), not under a service manager embedding the binary.
func runServiceIfRequested(_ *log.Logger, _, _ *string) bool { return false }

// isElevated reports whether the process runs as root.
func isElevated() bool { return os.Geteuid() == 0 }
