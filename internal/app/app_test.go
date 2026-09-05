package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/fife/sa05-desktop/internal/core/state"
	"github.com/fife/sa05-desktop/internal/storage"
)

// localProfile is a valid profile that exits through freedom and probes a local URL, so
// the controller can be exercised end-to-end without touching the internet.
func localProfile(remarks, probeURL string) string {
	return fmt.Sprintf(`{
      "remarks": %q,
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
    }`, remarks, probeURL)
}

type harness struct {
	app          *App
	store        *storage.Store
	echo         *httptest.Server
	subscription *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	echo := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, "sa05")
	}))
	t.Cleanup(echo.Close)

	body := "[" +
		localProfile("🇩🇪 Германия", echo.URL) + "," +
		localProfile("🇸🇪 Швеция", echo.URL) + "]"
	subscriptionServer := httptest.NewTLSServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("profile-title", "Тест")
			fmt.Fprint(writer, body)
		}))
	t.Cleanup(subscriptionServer.Close)

	store, err := storage.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	application := New(store, t.TempDir())
	t.Cleanup(application.Shutdown)

	return &harness{
		app:          application,
		store:        store,
		echo:         echo,
		subscription: subscriptionServer,
	}
}

// importAll uses the TLS test server's own client, since its certificate is self-signed.
func (h *harness) importAll(t *testing.T) {
	t.Helper()
	transport := h.subscription.Client().Transport
	original := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original })

	if err := h.app.ImportSubscription(context.Background(), h.subscription.URL); err != nil {
		t.Fatalf("ImportSubscription: %v", err)
	}
}

func TestConnectRequiresSubscription(t *testing.T) {
	harness := newHarness(t)
	if err := harness.app.Connect(context.Background()); err == nil {
		t.Fatal("подключение без подписки разрешено")
	}
	view, err := harness.app.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if view.Snapshot.FailureKind != state.FailureAuthorization {
		t.Fatalf("вид отказа = %s", view.Snapshot.FailureKind)
	}
	if view.Presentation.PrimaryAction != state.ActionOpenSubscription {
		t.Fatalf("действие = %s", view.Presentation.PrimaryAction)
	}
}

func TestImportThenConnectCarriesTraffic(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)

	view, err := harness.app.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if !view.Subscription.Authorized || view.Subscription.Count != 2 {
		t.Fatalf("подписка не импортирована: %+v", view.Subscription)
	}
	if view.Subscription.Title != "Тест" {
		t.Fatalf("название = %q", view.Subscription.Title)
	}
	if len(view.Profiles) != 2 || !view.Profiles[0].Active {
		t.Fatalf("профили: %+v", view.Profiles)
	}
	if view.Profiles[0].Flag != "🇩🇪" || view.Profiles[0].Name != "Германия" {
		t.Fatalf("разбор remarks: %+v", view.Profiles[0])
	}

	ctx := context.Background()
	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	connected := harness.app.states.Snapshot()
	if connected.Status != state.StatusConnected {
		t.Fatalf("статус = %s", connected.Status)
	}
	if connected.SocksPort == 0 || connected.HTTPPort == 0 {
		t.Fatalf("порты не опубликованы: %+v", connected)
	}
	if connected.ProfileName != "Германия" {
		t.Fatalf("имя профиля = %q", connected.ProfileName)
	}

	// The published port must be the one that actually serves traffic.
	dialer, err := proxy.SOCKS5("tcp",
		net.JoinHostPort("127.0.0.1", fmt.Sprint(connected.SocksPort)), nil, proxy.Direct)
	if err != nil {
		t.Fatalf("SOCKS-клиент: %v", err)
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{DialContext: dialer.(proxy.ContextDialer).DialContext},
	}
	response, err := client.Get(harness.echo.URL)
	if err != nil {
		t.Fatalf("запрос через туннель: %v", err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if string(payload) != "sa05" {
		t.Fatalf("ответ = %q", payload)
	}

	harness.app.Disconnect()
	if status := harness.app.states.Snapshot().Status; status != state.StatusDisconnected {
		t.Fatalf("статус после отключения = %s", status)
	}
}

func TestSelectProfileReconnectsWhileConnected(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	ctx := context.Background()
	if err := harness.app.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	view, err := harness.app.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	second := view.Profiles[1].ID

	if err := harness.app.SelectProfile(ctx, second); err != nil {
		t.Fatalf("SelectProfile: %v", err)
	}
	snapshot := harness.app.states.Snapshot()
	if snapshot.Status != state.StatusConnected {
		t.Fatalf("статус после смены профиля = %s", snapshot.Status)
	}
	if snapshot.ProfileID != second || snapshot.ProfileName != "Швеция" {
		t.Fatalf("активный профиль не сменился: %+v", snapshot)
	}
	if err := harness.app.SelectProfile(ctx, "нет-такого"); err == nil {
		t.Fatal("несуществующий профиль принят")
	}
}

func TestSelectProfileWhileDisconnectedOnlySaves(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	view, err := harness.app.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	second := view.Profiles[1].ID

	if err := harness.app.SelectProfile(context.Background(), second); err != nil {
		t.Fatalf("SelectProfile: %v", err)
	}
	if status := harness.app.states.Snapshot().Status; status != state.StatusDisconnected {
		t.Fatalf("выбор профиля поднял туннель: %s", status)
	}
	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Subscription.ActiveProfileID != second {
		t.Fatal("выбор не сохранён")
	}
}

func TestPingAndSelectFastest(t *testing.T) {
	harness := newHarness(t)
	harness.importAll(t)
	ctx := context.Background()

	profiles, err := harness.app.PingProfiles(ctx)
	if err != nil {
		t.Fatalf("PingProfiles: %v", err)
	}
	for _, profile := range profiles {
		if profile.Error != "" || profile.LatencyMS < 0 {
			t.Fatalf("профиль %s: %+v", profile.Name, profile)
		}
	}
	chosen, err := harness.app.SelectFastest(ctx)
	if err != nil {
		t.Fatalf("SelectFastest: %v", err)
	}
	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Subscription.ActiveProfileID != chosen {
		t.Fatal("самый быстрый профиль не сохранён")
	}
}

func TestTogglesPersistAndUnimplementedOnesRefuse(t *testing.T) {
	harness := newHarness(t)
	ctx := context.Background()
	if err := harness.app.Toggle(ctx, "autoConnect", true); err != nil {
		t.Fatalf("Toggle(autoConnect): %v", err)
	}
	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !stored.Toggles.AutoConnect {
		t.Fatal("переключатель не сохранён")
	}
	// A toggle whose backend does not exist yet must fail loudly, not look enabled.
	for _, name := range []string{"tun", "systemProxy", "чепуха"} {
		if err := harness.app.Toggle(ctx, name, true); err == nil {
			t.Fatalf("переключатель %q принят", name)
		}
	}
}

func TestSetThemePersistsAndNormalizes(t *testing.T) {
	harness := newHarness(t)
	ctx := context.Background()
	view, err := harness.app.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if view.Theme != "auto" {
		t.Fatalf("тема по умолчанию = %q", view.Theme)
	}
	if err := harness.app.SetTheme(ctx, "dark"); err != nil {
		t.Fatalf("SetTheme(dark): %v", err)
	}
	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Theme != "dark" {
		t.Fatalf("тема не сохранена: %q", stored.Theme)
	}
	// Garbage must not break the UI: it falls back to following the system.
	if err := harness.app.SetTheme(ctx, "чепуха"); err != nil {
		t.Fatalf("SetTheme(чепуха): %v", err)
	}
	view, err = harness.app.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if view.Theme != "auto" {
		t.Fatalf("тема не нормализована: %q", view.Theme)
	}
}

func TestTelegramLinkPersistsSecret(t *testing.T) {
	harness := newHarness(t)
	link, err := harness.app.TelegramLink()
	if err != nil {
		t.Fatalf("TelegramLink: %v", err)
	}
	stored, err := harness.store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.Telegram.Secret == "" {
		t.Fatal("секрет не сохранён")
	}
	// The link must stay identical across calls: Telegram is configured once.
	again, err := harness.app.TelegramLink()
	if err != nil {
		t.Fatalf("TelegramLink: %v", err)
	}
	if again != link {
		t.Fatalf("ссылка изменилась: %q -> %q", link, again)
	}
	if err := harness.app.SetTelegramTransport(context.Background(), "cf"); err != nil {
		t.Fatalf("SetTelegramTransport: %v", err)
	}
	view, err := harness.app.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if view.Telegram.Transport != "cf" || view.Telegram.Port != 1443 {
		t.Fatalf("Telegram: %+v", view.Telegram)
	}
}

func TestViewSerializesForFrontend(t *testing.T) {
	harness := newHarness(t)
	view, err := harness.app.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"snapshot", "presentation", "subscription", "profiles", "toggles", "telegram"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("в модели нет поля %q", key)
		}
	}
}
