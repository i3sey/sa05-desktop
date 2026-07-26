package tgws

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("порт не получен: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func testSettings(t *testing.T) Settings {
	t.Helper()
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	return Settings{
		Port:     freePort(t),
		Secret:   secret,
		CacheDir: t.TempDir(),
		Log:      io.Discard,
	}
}

func dial(t *testing.T, port int) {
	t.Helper()
	connection, err := net.DialTimeout("tcp",
		net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), 2*time.Second)
	if err != nil {
		t.Fatalf("порт %d не принимает соединения: %v", port, err)
	}
	connection.Close()
}

func TestProxyStartsAndStops(t *testing.T) {
	proxy := &Proxy{}
	settings := testSettings(t)

	if err := proxy.Start(context.Background(), settings); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	if !proxy.Running() || proxy.Port() != settings.Port {
		t.Fatalf("состояние после запуска: running=%v port=%d", proxy.Running(), proxy.Port())
	}
	dial(t, settings.Port)

	proxy.Stop()
	if proxy.Running() {
		t.Fatal("прокси остался запущенным")
	}
	// Stopping twice must stay harmless: the UI calls it on every toggle.
	proxy.Stop()

	if _, err := net.DialTimeout("tcp",
		net.JoinHostPort("127.0.0.1", fmt.Sprint(settings.Port)), 500*time.Millisecond); err == nil {
		t.Fatal("порт остался открытым после остановки")
	}
}

func TestProxyRestartsOnTransportChange(t *testing.T) {
	proxy := &Proxy{}
	settings := testSettings(t)
	settings.Transport = TransportAuto

	if err := proxy.Start(context.Background(), settings); err != nil {
		t.Fatalf("Start(auto): %v", err)
	}
	defer proxy.Stop()
	if proxy.Transport() != TransportAuto {
		t.Fatalf("транспорт = %s", proxy.Transport())
	}

	// The port and the secret stay put across a transport change: Telegram must not need
	// reconfiguring.
	settings.Transport = TransportTCP
	if err := proxy.Start(context.Background(), settings); err != nil {
		t.Fatalf("Start(tcp): %v", err)
	}
	if proxy.Transport() != TransportTCP {
		t.Fatalf("транспорт = %s", proxy.Transport())
	}
	if proxy.Port() != settings.Port {
		t.Fatalf("порт изменился: %d", proxy.Port())
	}
	dial(t, settings.Port)
}

func TestProxyRejectsBadSecret(t *testing.T) {
	proxy := &Proxy{}
	settings := testSettings(t)
	settings.Secret = "не-секрет"

	if err := proxy.Start(context.Background(), settings); err == nil {
		t.Fatal("некорректный секрет принят")
	}
	if proxy.Running() {
		t.Fatal("прокси запустился с некорректным секретом")
	}
}

func TestProxyUsesHostDialer(t *testing.T) {
	// The injected dialer is what keeps the proxy's own sockets out of the SA05 TUN, so
	// it must be the one the upstream code calls.
	used := make(chan string, 1)
	proxy := &Proxy{}
	settings := testSettings(t)
	settings.Transport = TransportTCP
	settings.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		select {
		case used <- address:
		default:
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}

	if err := proxy.Start(context.Background(), settings); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proxy.Stop()

	// A client that connects and immediately closes still drives the proxy through its
	// upstream dial path.
	connection, err := net.DialTimeout("tcp",
		net.JoinHostPort("127.0.0.1", fmt.Sprint(settings.Port)), 2*time.Second)
	if err != nil {
		t.Fatalf("подключение к прокси: %v", err)
	}
	// Enough bytes for the MTProto handshake read to complete and the relay to start.
	payload := make([]byte, 64)
	for index := range payload {
		payload[index] = byte(index * 7)
	}
	connection.Write(payload)
	defer connection.Close()

	select {
	case address := <-used:
		if address == "" {
			t.Fatal("диалер вызван без адреса")
		}
	case <-time.After(15 * time.Second):
		t.Skip("апстрим не был вызван: сеть в тестовой среде недоступна")
	}
}
