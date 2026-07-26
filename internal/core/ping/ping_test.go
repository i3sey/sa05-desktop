package ping

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fife/sa05-desktop/internal/core/subscription"
)

// localProfile routes everything out through freedom and points the probe at a local
// server, so the measurement path is exercised without touching the internet.
func localProfile(probeURL string) string {
	return fmt.Sprintf(`{
      "log": {"loglevel": "none"},
      "inbounds": [{
        "tag": "socks",
        "listen": "127.0.0.1",
        "port": 10808,
        "protocol": "socks",
        "settings": {"udp": true, "auth": "noauth"}
      }],
      "outbounds": [{"tag": "direct", "protocol": "freedom"}],
      "burstObservatory": {"pingConfig": {"timeout": "5s", "destination": %q}}
    }`, probeURL)
}

func TestMeasureReturnsLatency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	measurer := &Measurer{AssetDir: t.TempDir()}
	latency, err := measurer.Measure(context.Background(), localProfile(server.URL))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	// A loopback answer can be faster than the platform clock's tick, so zero is a valid
	// measurement here; what matters is that it is bounded and reported.
	if latency < 0 || latency > 10*time.Second {
		t.Fatalf("задержка = %s", latency)
	}
}

func TestMeasureUsesEphemeralPorts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Both profiles name port 10808. Ephemeral rebinding is what lets them run at once;
	// without it the second core would silently share the first one's listener.
	measurer := &Measurer{AssetDir: t.TempDir(), Concurrency: 2}
	profiles := []subscription.Profile{
		{ID: "a", JSON: localProfile(server.URL)},
		{ID: "b", JSON: localProfile(server.URL)},
	}
	results := measurer.MeasureAll(context.Background(), profiles)
	if len(results) != 2 {
		t.Fatalf("результатов %d", len(results))
	}
	for index, result := range results {
		if !result.OK() {
			t.Fatalf("профиль %s: %s", result.ProfileID, result.Error)
		}
		// Callers treat zero as "not measured", so a successful probe must never report it.
		if result.LatencyMS < 1 {
			t.Fatalf("профиль %s измерен как %d мс", result.ProfileID, result.LatencyMS)
		}
		if result.ProfileID != profiles[index].ID {
			t.Fatalf("порядок результатов нарушен: %+v", results)
		}
	}
}

func TestMeasureReportsFailureInsteadOfDroppingProfile(t *testing.T) {
	measurer := &Measurer{AssetDir: t.TempDir(), Timeout: 3 * time.Second}
	results := measurer.MeasureAll(context.Background(), []subscription.Profile{
		{ID: "broken", JSON: "{"},
		{ID: "no-probe", JSON: localProfile("ftp://example.com")},
	})
	if len(results) != 2 {
		t.Fatalf("результатов %d, ожидалось 2", len(results))
	}
	for _, result := range results {
		if result.OK() {
			t.Fatalf("сломанный профиль отмечен рабочим: %+v", result)
		}
		if result.ProfileID == "" {
			t.Fatal("результат без id профиля")
		}
	}
}

func TestMeasureAllHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := (&Measurer{AssetDir: t.TempDir()}).MeasureAll(ctx, []subscription.Profile{
		{ID: "a", JSON: localProfile("https://example.com")},
	})
	if len(results) != 1 || results[0].OK() {
		t.Fatalf("отменённая проверка вернула %+v", results)
	}
}
