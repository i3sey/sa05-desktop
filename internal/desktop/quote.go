package desktop

import "strings"

// Quote protects executable paths for shell, registry and scheduler command lines.
// Go's %q doubles backslashes on Windows paths and breaks autostart entries.
func Quote(path string) string {
	if !strings.ContainsAny(path, " \t\"") {
		return path
	}
	return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"`
}
