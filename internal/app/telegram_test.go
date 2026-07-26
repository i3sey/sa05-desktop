package app

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/tgws"
)

func freeLocalPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("порт не получен: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func portAccepts(port int) bool {
	connection, err := net.DialTimeout("tcp",
		net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), time.Second)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}

func TestTelegramToggleRunsWithoutSubscription(t *testing.T) {
	harness := newHarness(t)
	harness.app.telegramPort = freeLocalPort(t)
	ctx := context.Background()

	// The Telegram proxy is the one feature that must work with no subscription at all.
	if err := harness.app.Toggle(ctx, "telegram", true); err != nil {
		t.Fatalf("Toggle(on): %v", err)
	}
	if !portAccepts(harness.app.telegramPort) {
		t.Fatal("порт MTProto не открыт")
	}
	snapshot := harness.app.Snapshot()
	if !snapshot.TelegramOn {
		t.Fatal("состояние не отражает работающий Telegram")
	}
	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !stored.Toggles.Telegram || !tgws.ValidSecret(stored.Telegram.Secret) {
		t.Fatalf("состояние не сохранено: %+v", stored.Telegram)
	}

	if err := harness.app.Toggle(ctx, "telegram", false); err != nil {
		t.Fatalf("Toggle(off): %v", err)
	}
	if portAccepts(harness.app.telegramPort) {
		t.Fatal("порт остался открытым")
	}
	if harness.app.Snapshot().TelegramOn {
		t.Fatal("состояние осталось включённым")
	}
}

func TestTelegramTransportChangeKeepsPortAndSecret(t *testing.T) {
	harness := newHarness(t)
	harness.app.telegramPort = freeLocalPort(t)
	ctx := context.Background()

	if err := harness.app.Toggle(ctx, "telegram", true); err != nil {
		t.Fatalf("Toggle(on): %v", err)
	}
	defer harness.app.Toggle(ctx, "telegram", false)

	before, err := harness.app.TelegramLink()
	if err != nil {
		t.Fatalf("TelegramLink: %v", err)
	}

	if err := harness.app.SetTelegramTransport(ctx, "tcp"); err != nil {
		t.Fatalf("SetTelegramTransport: %v", err)
	}
	// Same port, same secret, same link: Telegram is configured exactly once.
	if !portAccepts(harness.app.telegramPort) {
		t.Fatal("порт закрылся при смене транспорта")
	}
	after, err := harness.app.TelegramLink()
	if err != nil {
		t.Fatalf("TelegramLink: %v", err)
	}
	if before != after {
		t.Fatalf("ссылка изменилась: %q -> %q", before, after)
	}
	view, err := harness.app.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if view.Telegram.Transport != "tcp" {
		t.Fatalf("транспорт = %q", view.Telegram.Transport)
	}
	if !harness.app.Snapshot().TelegramOn {
		t.Fatal("прокси не работает после смены транспорта")
	}
}

func TestTelegramSurvivesTunnelDisconnect(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	harness.app.telegramPort = freeLocalPort(t)
	ctx := context.Background()

	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := harness.app.Toggle(ctx, "telegram", true); err != nil {
		t.Fatalf("Toggle(on): %v", err)
	}
	defer harness.app.Toggle(ctx, "telegram", false)

	// Telegram is independent of the tunnel: stopping the VPN must not kill it.
	harness.app.Disconnect()
	if !portAccepts(harness.app.telegramPort) {
		t.Fatal("Telegram остановился вместе с туннелем")
	}
	if harness.app.Snapshot().Status != state.StatusDisconnected {
		t.Fatalf("статус = %s", harness.app.Snapshot().Status)
	}
	if !harness.app.Snapshot().TelegramOn {
		t.Fatal("состояние Telegram потеряно при отключении туннеля")
	}
}
