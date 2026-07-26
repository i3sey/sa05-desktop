package ipc

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Endpoint builds an address for a private helper instance: tests, and side-by-side runs
// during development.
//
// The two platforms name endpoints differently — a filesystem path on Unix, a name in the
// pipe namespace on Windows — so callers must not assemble one by hand. dir is used only
// where the endpoint really is a file.
func Endpoint(dir, name string) string {
	if runtime.GOOS == "windows" {
		// Pipes are not files: the directory is irrelevant, but the name must be unique
		// per process, or two parallel tests would fight over one pipe.
		clean := strings.Map(func(symbol rune) rune {
			if symbol == '\\' || symbol == '/' || symbol == ':' {
				return '-'
			}
			return symbol
		}, name)
		return `\\.\pipe\sa05-` + clean + "-" + strconv.Itoa(os.Getpid())
	}
	return filepath.Join(dir, name)
}
