package app

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/ipc"
	"github.com/fife/sa05-desktop/internal/netbypass"
)

// fakeHelper stands in for the privileged daemon: the controller must talk to it over the
// real IPC transport, so the test starts an actual server on a temporary socket.
type fakeHelper struct {
	mutex   sync.Mutex
	ups     []ipc.TunUp
	downs   int
	failUp  error
	status  ipc.Status
	message string
}

func (f *fakeHelper) Status(context.Context) (ipc.Status, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.status, nil
}

func (f *fakeHelper) TunUp(_ context.Context, request ipc.TunUp) (ipc.Status, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.failUp != nil {
		return ipc.Status{}, f.failUp
	}
	f.ups = append(f.ups, request)
	f.status = ipc.Status{
		TunUp:     true,
		Interface: "sa05",
		SocksPort: request.SocksPort,
		Message:   f.message,
	}
	return f.status, nil
}

func (f *fakeHelper) TunDown(context.Context) (ipc.Status, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.downs++
	f.status = ipc.Status{}
	return f.status, nil
}

func (f *fakeHelper) calls() ([]ipc.TunUp, int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]ipc.TunUp{}, f.ups...), f.downs
}

func attachHelper(t *testing.T, application *App, handler ipc.Handler) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "helper.sock")
	listener, err := ipc.Listen(path)
	if err != nil {
		t.Fatalf("ipc.Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = (&ipc.Server{Handler: handler, Authorize: ipc.AllowLoginUsers()}).Serve(ctx, listener)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	application.helper = ipc.NewClient(path)
}

func TestTunRequiresRunningCore(t *testing.T) {
	harness := newHarness(t)
	helper := &fakeHelper{}
	attachHelper(t, harness.app, helper)

	if err := harness.app.Toggle(context.Background(), "tun", true); err == nil {
		t.Fatal("туннель поднят без работающего ядра")
	}
	ups, _ := helper.calls()
	if len(ups) != 0 {
		t.Fatalf("хелпер вызван без ядра: %+v", ups)
	}
}

func TestTunPassesCorePortAndPolicy(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	helper := &fakeHelper{message: "Kill-switch активен"}
	attachHelper(t, harness.app, helper)
	ctx := context.Background()

	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	snapshot := harness.app.Snapshot()

	if err := harness.app.Toggle(ctx, "tun", true); err != nil {
		t.Fatalf("Toggle(on): %v", err)
	}
	ups, _ := helper.calls()
	if len(ups) != 1 {
		t.Fatalf("вызовов TunUp: %d", len(ups))
	}
	request := ups[0]
	if request.SocksPort != snapshot.SocksPort {
		t.Fatalf("порт = %d, у ядра %d", request.SocksPort, snapshot.SocksPort)
	}
	// The mark must match what the core stamps on its own sockets, otherwise the client's
	// own traffic would loop back into the tunnel.
	if request.BypassMark != netbypass.Mark {
		t.Fatalf("метка = %#x, ожидалась %#x", request.BypassMark, netbypass.Mark)
	}
	if !request.KillSwitch {
		t.Fatal("kill-switch по умолчанию выключен")
	}
	if request.AllowIPv6Bypass {
		t.Fatal("IPv6 отдан системе по умолчанию")
	}

	current := harness.app.Snapshot()
	if !current.TunOn || current.Message != "Kill-switch активен" {
		t.Fatalf("состояние: %+v", current)
	}
	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !stored.Toggles.Tun {
		t.Fatal("состояние туннеля не сохранено")
	}
}

func TestTunFailureIsReportedAndNotRecorded(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	helper := &fakeHelper{failUp: errors.New("интерфейс не создан")}
	attachHelper(t, harness.app, helper)
	ctx := context.Background()

	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := harness.app.Toggle(ctx, "tun", true); err == nil {
		t.Fatal("ошибка хелпера не доведена до вызывающего")
	}
	if harness.app.Snapshot().TunOn {
		t.Fatal("состояние показывает работающий туннель после сбоя")
	}
	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Toggles.Tun {
		t.Fatal("сбойный туннель записан как включённый")
	}
}

func TestDisconnectDropsTunnel(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	helper := &fakeHelper{}
	attachHelper(t, harness.app, helper)
	ctx := context.Background()

	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := harness.app.Toggle(ctx, "tun", true); err != nil {
		t.Fatalf("Toggle(on): %v", err)
	}

	// Routing must never outlive the core: otherwise every connection dies silently.
	harness.app.Disconnect()
	_, downs := helper.calls()
	if downs == 0 {
		t.Fatal("маршрутизация не восстановлена при отключении")
	}
	if harness.app.Snapshot().TunOn {
		t.Fatal("состояние показывает работающий туннель после отключения")
	}
	if harness.app.Snapshot().Status != state.StatusDisconnected {
		t.Fatalf("статус = %s", harness.app.Snapshot().Status)
	}
}

func TestHelperAvailability(t *testing.T) {
	harness := newHarness(t)
	if harness.app.HelperAvailable(context.Background()) {
		t.Skip("в системе уже установлен хелпер SA05")
	}
	attachHelper(t, harness.app, &fakeHelper{})
	if !harness.app.HelperAvailable(context.Background()) {
		t.Fatal("запущенный хелпер не обнаружен")
	}
}
