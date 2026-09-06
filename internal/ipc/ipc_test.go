package ipc

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeHandler records calls so the tests can assert what the helper was asked to do.
type fakeHandler struct {
	mutex   sync.Mutex
	status  Status
	upCalls []TunUp
	downs   int
	failUp  error
}

func (f *fakeHandler) Status(context.Context) (Status, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.status, nil
}

func (f *fakeHandler) TunUp(_ context.Context, request TunUp) (Status, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.failUp != nil {
		return Status{}, f.failUp
	}
	f.upCalls = append(f.upCalls, request)
	f.status = Status{TunUp: true, Interface: "sa05", SocksPort: request.SocksPort}
	return f.status, nil
}

func (f *fakeHandler) TunDown(context.Context) (Status, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.downs++
	f.status = Status{}
	return f.status, nil
}

func startServer(t *testing.T, handler Handler, authorize Authorizer) *Client {
	t.Helper()
	path := Endpoint(t.TempDir(), "helper.sock")
	listener, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{Handler: handler, Authorize: authorize}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(ctx, listener)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	client := NewClient(path)
	t.Cleanup(client.Close)
	return client
}

func TestTunUpAndDownRoundTrip(t *testing.T) {
	handler := &fakeHandler{}
	client := startServer(t, handler, AllowLoginUsers())
	ctx := context.Background()

	if _, err := client.Hello(ctx); err != nil {
		t.Fatalf("Hello: %v", err)
	}
	status, err := client.TunUp(ctx, TunUp{SocksPort: 10808, DNS: "1.1.1.1", KillSwitch: true})
	if err != nil {
		t.Fatalf("TunUp: %v", err)
	}
	if !status.TunUp || status.SocksPort != 10808 || status.Version != ProtocolVersion {
		t.Fatalf("состояние: %+v", status)
	}
	if len(handler.upCalls) != 1 || handler.upCalls[0].SocksPort != 10808 {
		t.Fatalf("вызовы: %+v", handler.upCalls)
	}

	status, err = client.TunDown(ctx)
	if err != nil {
		t.Fatalf("TunDown: %v", err)
	}
	if status.TunUp || handler.downs != 1 {
		t.Fatalf("состояние после отключения: %+v, вызовов %d", status, handler.downs)
	}
}

func TestHandlerErrorReachesClient(t *testing.T) {
	handler := &fakeHandler{failUp: errors.New("интерфейс не создан")}
	client := startServer(t, handler, AllowLoginUsers())

	_, err := client.TunUp(context.Background(), TunUp{SocksPort: 10808})
	if err == nil || err.Error() != "интерфейс не создан" {
		t.Fatalf("ошибка = %v", err)
	}
}

func TestUnauthorizedPeerIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows pipes carry no peer uid; access is decided by the pipe's security
		// descriptor before a connection ever reaches the authorizer.
		t.Skip("на Windows доступ ограничивает дескриптор безопасности канала")
	}
	// A uid the policy does not know must not be able to touch routing.
	client := startServer(t, &fakeHandler{}, AllowUIDs(CurrentUID()+1))

	if _, err := client.Hello(context.Background()); err == nil {
		t.Fatal("неавторизованный клиент принят")
	}
}

func TestAuthorizedUIDPasses(t *testing.T) {
	client := startServer(t, &fakeHandler{}, AllowUIDs(CurrentUID()))
	if _, err := client.Hello(context.Background()); err != nil {
		t.Fatalf("авторизованный клиент отклонён: %v", err)
	}
}

func TestProtocolVersionMismatchIsRejected(t *testing.T) {
	client := startServer(t, &fakeHandler{}, AllowLoginUsers())
	ctx := context.Background()

	// A stale helper must refuse outright rather than half-apply a newer request.
	_, err := client.call(ctx, Request{
		Method: MethodHello,
		Hello:  &Hello{Version: ProtocolVersion + 1},
	})
	if err == nil {
		t.Fatal("несовместимая версия принята")
	}
}

func TestRequestValidation(t *testing.T) {
	cases := map[string]Request{
		"неизвестный метод":  {Method: "reboot"},
		"tun_up без данных":  {Method: MethodTunUp},
		"нулевой порт":       {Method: MethodTunUp, TunUp: &TunUp{SocksPort: 0}},
		"порт вне диапазона": {Method: MethodTunUp, TunUp: &TunUp{SocksPort: 70000}},
		"dns не адрес":       {Method: MethodTunUp, TunUp: &TunUp{SocksPort: 10808, DNS: "example.com"}},
		"dns с инъекцией":    {Method: MethodTunUp, TunUp: &TunUp{SocksPort: 10808, DNS: "1.1.1.1; rm -rf /"}},
		"hello без данных":   {Method: MethodHello},
	}
	for name, request := range cases {
		if err := request.Validate(); err == nil {
			t.Fatalf("%s: запрос принят", name)
		}
	}
	valid := Request{Method: MethodTunUp, TunUp: &TunUp{SocksPort: 10808, DNS: "1.1.1.1"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("корректный запрос отклонён: %v", err)
	}
}

func TestClientReportsMissingHelper(t *testing.T) {
	client := NewClient(Endpoint(t.TempDir(), "absent.sock"))
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if client.Available(ctx) {
		t.Fatal("отсутствующий хелпер считается доступным")
	}
	if _, err := client.TunUp(ctx, TunUp{SocksPort: 10808}); err == nil {
		t.Fatal("запрос без хелпера не привёл к ошибке")
	}
}

func TestClientReconnectsAfterHelperRestart(t *testing.T) {
	path := Endpoint(t.TempDir(), "helper.sock")
	handler := &fakeHandler{}
	client := NewClient(path)
	defer client.Close()
	ctx := context.Background()

	run := func() (stop func()) {
		listener, err := Listen(path)
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		serveCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = (&Server{Handler: handler, Authorize: AllowLoginUsers()}).Serve(serveCtx, listener)
		}()
		return func() {
			cancel()
			<-done
		}
	}

	stop := run()
	if _, err := client.Hello(ctx); err != nil {
		t.Fatalf("Hello: %v", err)
	}
	stop()

	// The GUI outlives helper restarts, so the next call must reconnect on its own.
	stop = run()
	defer stop()
	if _, err := client.Hello(ctx); err != nil {
		t.Fatalf("после перезапуска: %v", err)
	}
}

func TestTunUpValidationBounds(t *testing.T) {
	valid := TunUp{SocksPort: 10808, DNS: "1.1.1.1", BypassIPs: []string{"1.2.3.4", "example.com"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("валидный запрос отклонён: %v", err)
	}
	for _, request := range []TunUp{
		{SocksPort: 0},
		{SocksPort: 10808, DNS: "not-an-ip"},
		{SocksPort: 10808, BypassIPs: []string{"a;b"}},
		{SocksPort: 10808, BypassIPs: []string{"http://example.com/x"}},
		{SocksPort: 10808, BypassIPs: []string{""}},
	} {
		if err := request.Validate(); err == nil {
			t.Fatalf("невалидный запрос принят: %+v", request)
		}
	}
	many := TunUp{SocksPort: 10808}
	for index := 0; index < maxBypassIPs+1; index++ {
		many.BypassIPs = append(many.BypassIPs, "10.0.0.1")
	}
	if err := many.Validate(); err == nil {
		t.Fatal("переполнение bypass-списка принято")
	}
}
