package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fife/sa05-desktop/internal/core/diag"
)

// localTargets replace the public sites: the wiring is what these tests are about, and
// real targets would make them fail for reasons outside the client.
func localTargets(t *testing.T) []diag.Target {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, strings.Repeat("страница", 40))
		}))
	t.Cleanup(server.Close)
	return []diag.Target{
		{ID: "control", Label: "Контроль", URL: server.URL, Group: diag.GroupControl, MinimumBodyBytes: 8},
		{ID: "dpi-1", Label: "Сайт 1", URL: server.URL, Group: diag.GroupDPI, MinimumBodyBytes: 8},
		{ID: "dpi-2", Label: "Сайт 2", URL: server.URL, Group: diag.GroupDPI, MinimumBodyBytes: 8},
	}
}

func TestDiagnoseRunsDirectlyWhenDisconnected(t *testing.T) {
	harness := newHarness(t)
	harness.app.diagTargets = localTargets(t)
	report, err := harness.app.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if report.ThroughTunnel {
		t.Fatal("проверка без туннеля помечена как проверка через туннель")
	}
	if len(report.Results) != len(harness.app.diagTargets) {
		t.Fatalf("результатов %d, целей %d", len(report.Results), len(harness.app.diagTargets))
	}
	if !report.Verdict.BypassOK {
		t.Fatalf("все цели ответили, но вердикт отрицательный: %+v", report.Verdict)
	}
	if report.Verdict.Headline == "" {
		t.Fatal("вердикт без заголовка")
	}
	for _, result := range report.Results {
		if result.Target.Label == "" {
			t.Fatalf("результат без названия цели: %+v", result)
		}
	}
}

func TestDiagnoseUsesTunnelWhenConnected(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	harness.app.notifier.Enabled = false
	harness.app.diagTargets = localTargets(t)
	ctx := context.Background()
	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	report, err := harness.app.Diagnose(ctx)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	// The profile exits through freedom, so the probes reach the internet either way; what
	// matters is that they were routed through the core.
	if !report.ThroughTunnel {
		t.Fatal("проверка не пошла через туннель при поднятом ядре")
	}
}
