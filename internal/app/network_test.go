package app

import (
	"context"
	"testing"
	"time"

	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/netmon"
	"github.com/fife/sa05-desktop/internal/storage"
)

func TestAutoConnectHonoursTheToggle(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	// Notifications would reach the real desktop from a test run.
	harness.app.notifier.Enabled = false

	// Off: startup must leave the tunnel alone.
	harness.app.Start(context.Background())
	time.Sleep(200 * time.Millisecond)
	if harness.app.Snapshot().Requested() {
		t.Fatal("клиент подключился с выключенным автоподключением")
	}

	if _, err := harness.store.Update(func(next *storage.State) {
		next.Toggles.AutoConnect = true
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	harness.app.autoConnect(context.Background())
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if harness.app.Snapshot().Status == state.StatusConnected {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("автоподключение не сработало: %s", harness.app.Snapshot().Status)
}

func TestAutoConnectSkipsWithoutSubscription(t *testing.T) {
	harness := newHarness(t)
	harness.app.notifier.Enabled = false
	if _, err := harness.store.Update(func(next *storage.State) {
		next.Toggles.AutoConnect = true
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Connecting without a subscription would only produce an error popup at login.
	harness.app.autoConnect(context.Background())
	time.Sleep(300 * time.Millisecond)
	if harness.app.Snapshot().Requested() {
		t.Fatal("клиент попытался подключиться без подписки")
	}
}

func TestNetworkChangeIgnoredWhileDisconnected(t *testing.T) {
	harness := newHarness(t)
	harness.app.notifier.Enabled = false

	fingerprint := harness.app.handleNetworkChange(context.Background(), "wifi-старый")
	if fingerprint != netmon.Current() {
		t.Fatalf("отпечаток не обновлён: %q", fingerprint)
	}
	if harness.app.Snapshot().Requested() {
		t.Fatal("смена сети подняла туннель, которого не просили")
	}
}

func TestNetworkLossMarksWaitingForNetwork(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	harness.app.notifier.Enabled = false
	ctx := context.Background()
	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// An empty fingerprint is what the watcher reports when every interface goes down.
	harness.app.states.Update(func(next *state.Snapshot) { next.Status = state.StatusConnected })
	previous := netmon.Current()
	harness.app.handleNetworkChangeWith(ctx, previous, netmon.None)

	snapshot := harness.app.Snapshot()
	if snapshot.Status != state.StatusWaitingForNetwork {
		t.Fatalf("статус = %s", snapshot.Status)
	}
	if snapshot.FailureKind != state.FailureNetwork {
		t.Fatalf("вид отказа = %s", snapshot.FailureKind)
	}
	// The user did not disconnect, so the client must keep wanting the tunnel.
	if !snapshot.Requested() {
		t.Fatal("клиент перестал считать туннель нужным")
	}
}

func TestDrainCollapsesBurst(t *testing.T) {
	events := make(chan struct{}, 8)
	for range 5 {
		events <- struct{}{}
	}
	started := time.Now()
	if !drain(context.Background(), events, 100*time.Millisecond) {
		t.Fatal("drain прервался")
	}
	// One window after the last event, not one per event.
	if elapsed := time.Since(started); elapsed > 400*time.Millisecond {
		t.Fatalf("пачка событий обрабатывалась %s", elapsed)
	}
}

func TestDrainStopsOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if drain(ctx, make(chan struct{}), time.Second) {
		t.Fatal("drain продолжил работу после отмены контекста")
	}
}
