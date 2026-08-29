package desktop

import (
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	if got := Quote("/usr/bin/sa05"); got != "/usr/bin/sa05" {
		t.Fatalf("простой путь закавычен: %q", got)
	}
	if got := Quote(`/opt/my apps/sa05`); got != `"/opt/my apps/sa05"` {
		t.Fatalf("путь с пробелом: %q", got)
	}
	if got := Quote(`C:\Programs\sa05\sa05.exe`); got != `C:\Programs\sa05\sa05.exe` {
		t.Fatalf("windows путь без пробелов: %q", got)
	}
	if strings.Contains(Quote(`C:\Programs\sa05\sa05.exe`), "\\\\") {
		t.Fatalf("удвоенные слэши: %q", Quote(`C:\Programs\sa05\sa05.exe`))
	}
}
