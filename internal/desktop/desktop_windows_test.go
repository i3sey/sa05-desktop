//go:build windows

package desktop

import (
	"strings"
	"testing"
)

func TestAutostartCommandUsesSingleBackslashes(t *testing.T) {
	path := `C:\Programs\sa05-v0.1.0-windows-amd64\sa05.exe`
	got := autostartCommand(path)
	want := `C:\Programs\sa05-v0.1.0-windows-amd64\sa05.exe --tray`
	if got != want {
		t.Fatalf("команда автозапуска: got %q want %q", got, want)
	}
	if strings.Contains(got, "\\\\") {
		t.Fatalf("удвоенные слэши в команде: %q", got)
	}
}
