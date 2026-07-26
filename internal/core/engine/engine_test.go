package engine

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// freePort reserves an ephemeral port and releases it, so the config below can name a
// port that is free right now instead of a hardcoded one that may be taken.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("порт не получен: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func loopbackProfile(socksPort int) string {
	return fmt.Sprintf(`{
      "log": {"loglevel": "none"},
      "inbounds": [{
        "tag": "socks",
        "listen": "127.0.0.1",
        "port": %d,
        "protocol": "socks",
        "settings": {"udp": true, "auth": "noauth"}
      }],
      "outbounds": [{"tag": "direct", "protocol": "freedom"}]
    }`, socksPort)
}

// TestStartServesTrafficThroughSocks is the end-to-end check that the in-process core
// actually carries traffic: a request through the SOCKS inbound must reach a local
// server. A freedom outbound keeps it offline-safe.
func TestStartServesTrafficThroughSocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, "sa05")
	}))
	defer server.Close()

	socksPort := freePort(t)
	core := New(t.TempDir())
	core.HTTPPort = freePort(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ports, err := core.Start(ctx, loopbackProfile(socksPort))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer core.Stop()

	if ports.Socks != socksPort {
		t.Fatalf("SOCKS-порт = %d, ожидался %d", ports.Socks, socksPort)
	}
	if ports.HTTP != core.HTTPPort {
		t.Fatalf("HTTP-порт = %d, ожидался %d", ports.HTTP, core.HTTPPort)
	}
	if !core.Running() || !core.Healthy(ctx) {
		t.Fatal("ядро не считает себя запущенным")
	}

	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", ports.Socks), nil, proxy.Direct)
	if err != nil {
		t.Fatalf("SOCKS-клиент: %v", err)
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: dialer.(proxy.ContextDialer).DialContext,
		},
	}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("запрос через SOCKS: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("чтение ответа: %v", err)
	}
	if string(body) != "sa05" {
		t.Fatalf("ответ = %q", body)
	}

	// The added HTTP inbound is what the system-proxy toggle points at.
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustURL(t, fmt.Sprintf("http://127.0.0.1:%d", ports.HTTP))),
		},
	}
	viaHTTP, err := httpClient.Get(server.URL)
	if err != nil {
		t.Fatalf("запрос через HTTP-инбаунд: %v", err)
	}
	viaHTTP.Body.Close()

	core.Stop()
	if core.Running() || core.Healthy(ctx) {
		t.Fatal("ядро осталось запущенным после Stop")
	}
	// Stopping twice must stay harmless: the UI calls it on every state change.
	core.Stop()
}

func TestStartRejectsInvalidProfiles(t *testing.T) {
	core := New(t.TempDir())
	ctx := context.Background()
	cases := map[string]string{
		"пустой профиль": "  ",
		"битый JSON":     "{",
		"нет socks":      `{"inbounds": [], "outbounds": []}`,
	}
	for name, profile := range cases {
		if _, err := core.Start(ctx, profile); err == nil {
			t.Fatalf("%s: ожидалась ошибка", name)
		}
		if core.Running() {
			t.Fatalf("%s: ядро осталось запущенным", name)
		}
	}
}

func TestHealthyIsFalseBeforeStart(t *testing.T) {
	core := New(t.TempDir())
	if core.Healthy(context.Background()) {
		t.Fatal("незапущенное ядро считается исправным")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("URL %q: %v", raw, err)
	}
	return parsed
}

// TestTrafficCountsWhatCrossedTheTunnel is the check that the counters are actually wired:
// a request through the SOCKS inbound must show up as bytes on the outbound.
func TestTrafficCountsWhatCrossedTheTunnel(t *testing.T) {
	payload := strings.Repeat("sa05", 4096)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, payload)
	}))
	defer server.Close()

	core := New(t.TempDir())
	core.Ephemeral = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ports, err := core.Start(ctx, loopbackProfile(0))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer core.Stop()

	if before := core.Traffic(); before.Downlink != 0 || before.Uplink != 0 {
		t.Fatalf("счётчики не с нуля: %+v", before)
	}

	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", ports.Socks), nil, proxy.Direct)
	if err != nil {
		t.Fatalf("SOCKS-клиент: %v", err)
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{DialContext: dialer.(proxy.ContextDialer).DialContext},
	}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()

	// Both directions must register. The exact totals are deliberately not asserted: when
	// the outbound is a plain TCP socket — as freedom over loopback is here — the core
	// serves the bulk copy with splice/readv straight off the socket, which bypasses the
	// counting wrappers. Real profiles proxy through VLESS/XHTTP, where every byte passes
	// through the core and is counted.
	traffic := Traffic{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		traffic = core.Traffic()
		if traffic.Uplink > 0 && traffic.Downlink > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if traffic.Uplink <= 0 || traffic.Downlink <= 0 {
		t.Fatalf("счётчики не считают: %+v", traffic)
	}

	// A stopped core has no counters left to read.
	core.Stop()
	if after := core.Traffic(); after.Uplink != 0 || after.Downlink != 0 {
		t.Fatalf("счётчики пережили остановку: %+v", after)
	}
}
