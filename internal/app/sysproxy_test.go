package app

import (
	"context"
	"errors"
	"testing"

	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/sysproxy"
)

// fakeProxy records what the controller was asked to do and can be made to fail.
type fakeProxy struct {
	name      string
	applied   sysproxy.Settings
	enabled   bool
	restored  bool
	failOnSet bool
}

func (f *fakeProxy) Name() string    { return f.name }
func (f *fakeProxy) Available() bool { return true }

func (f *fakeProxy) Enable(settings sysproxy.Settings) (sysproxy.Snapshot, error) {
	if f.failOnSet {
		return sysproxy.Snapshot{}, errors.New("не удалось применить")
	}
	f.applied = settings
	f.enabled = true
	return sysproxy.Snapshot{
		Backend: f.name,
		Values:  map[string]string{"mode": "'none'"},
	}, nil
}

func (f *fakeProxy) Disable(snapshot sysproxy.Snapshot) error {
	if snapshot.Backend != "" && snapshot.Backend != f.name {
		return errors.New("чужой снимок")
	}
	f.enabled = false
	f.restored = true
	return nil
}

func TestSystemProxyRequiresRunningCore(t *testing.T) {
	harness := newHarness(t)
	proxy := &fakeProxy{name: "gnome"}
	harness.app.proxy = proxy

	if err := harness.app.Toggle(context.Background(), "systemProxy", true); err == nil {
		t.Fatal("прокси включён без запущенного ядра")
	}
	if proxy.enabled {
		t.Fatal("контроллер вызван без ядра")
	}
}

func TestSystemProxyAppliesCorePortsAndRestoresOnDisable(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	proxy := &fakeProxy{name: "gnome"}
	harness.app.proxy = proxy

	ctx := context.Background()
	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	snapshot := harness.app.Snapshot()

	if err := harness.app.Toggle(ctx, "systemProxy", true); err != nil {
		t.Fatalf("Toggle(on): %v", err)
	}
	if proxy.applied.HTTPPort != snapshot.HTTPPort || proxy.applied.SocksPort != snapshot.SocksPort {
		t.Fatalf("применены не те порты: %+v против %+v", proxy.applied, snapshot)
	}

	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !stored.Toggles.SystemProxy || stored.SysProxy == nil {
		t.Fatalf("снимок прежних настроек не сохранён: %+v", stored.Toggles)
	}
	if harness.app.Snapshot().SystemProxyOn != true {
		t.Fatal("состояние не отражает включённый прокси")
	}

	if err := harness.app.Toggle(ctx, "systemProxy", false); err != nil {
		t.Fatalf("Toggle(off): %v", err)
	}
	if !proxy.restored {
		t.Fatal("прежние настройки не восстановлены")
	}
	stored, err = harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Toggles.SystemProxy || stored.SysProxy != nil {
		t.Fatalf("снимок остался после выключения: %+v", stored)
	}
}

func TestDisconnectAlsoDropsSystemProxy(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	proxy := &fakeProxy{name: "gnome"}
	harness.app.proxy = proxy

	ctx := context.Background()
	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := harness.app.Toggle(ctx, "systemProxy", true); err != nil {
		t.Fatalf("Toggle(on): %v", err)
	}

	// Leaving the desktop pointed at a dead port would break all browsing.
	harness.app.Disconnect()
	if proxy.enabled {
		t.Fatal("прокси остался включённым после отключения")
	}
	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Toggles.SystemProxy {
		t.Fatal("флаг прокси остался включённым")
	}
	if harness.app.Snapshot().Status != state.StatusDisconnected {
		t.Fatalf("статус = %s", harness.app.Snapshot().Status)
	}
}

func TestSystemProxyRejectsSnapshotFromAnotherBackend(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	harness.app.proxy = &fakeProxy{name: "gnome"}

	ctx := context.Background()
	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := harness.app.Toggle(ctx, "systemProxy", true); err != nil {
		t.Fatalf("Toggle(on): %v", err)
	}

	// The session changed between runs: replaying GNOME settings through KDE would write
	// nonsense, so the restore must refuse instead.
	harness.app.proxy = &fakeProxy{name: "kde"}
	if err := harness.app.Toggle(ctx, "systemProxy", false); err == nil {
		t.Fatal("снимок чужого бэкенда применён")
	}
}
