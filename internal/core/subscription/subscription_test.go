package subscription

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func profileJSON(remarks string, port int) string {
	return fmt.Sprintf(`{
  "remarks": %q,
  "inbounds": [{"protocol": "socks", "listen": "127.0.0.1", "port": %d, "settings": {"udp": true}}],
  "outbounds": [{"tag": "proxy", "protocol": "vless", "settings": {"vnext": [{"address": "a.example", "port": 443}]}}]
}`, remarks, port)
}

func TestParseProfilesReadsRemarksAndStableIDs(t *testing.T) {
	body := "[" + profileJSON("Германия", 10808) + "," +
		strings.Replace(profileJSON("", 10809), `"remarks": "",`, "", 1) + "]"
	profiles, err := ParseProfiles(body)
	if err != nil {
		t.Fatalf("ParseProfiles: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("профилей %d, ожидалось 2", len(profiles))
	}
	if profiles[0].Remarks != "Германия" || profiles[1].Remarks != "Профиль 2" {
		t.Fatalf("remarks: %q, %q", profiles[0].Remarks, profiles[1].Remarks)
	}
	if profiles[0].ID == profiles[1].ID {
		t.Fatal("разные профили получили одинаковый id")
	}
	// The same JSON must always produce the same id, so a refresh keeps the selection.
	again, err := ParseProfiles(body)
	if err != nil {
		t.Fatalf("ParseProfiles: %v", err)
	}
	if again[0].ID != profiles[0].ID {
		t.Fatal("id профиля нестабилен")
	}
}

func TestParseProfilesAcceptsWildcardSocksListener(t *testing.T) {
	body := `[{
      "remarks": "LAN",
      "inbounds": [{"protocol": "socks", "listen": "0.0.0.0", "port": 10808, "settings": {"udp": true}}],
      "outbounds": [{"protocol": "vless", "settings": {"vnext": [{"address": "a.example", "port": 443}]}}]
    }]`
	profiles, err := ParseProfiles(body)
	if err != nil {
		t.Fatalf("ParseProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Remarks != "LAN" {
		t.Fatalf("профиль разобран неверно: %+v", profiles)
	}
}

func TestParseProfilesRejectsInvalidDocuments(t *testing.T) {
	cases := map[string]string{
		"пустой массив":  `[]`,
		"не массив":      `{"remarks": "x"}`,
		"битый JSON":     `[`,
		"профиль-строка": `["nope"]`,
		"нет socks":      `[{"inbounds": [], "outbounds": []}]`,
	}
	for name, body := range cases {
		if _, err := ParseProfiles(body); err == nil {
			t.Fatalf("%s: ожидалась ошибка", name)
		}
	}
}

func newTestClient(handler http.HandlerFunc) (*Client, *httptest.Server) {
	server := httptest.NewTLSServer(handler)
	client := &Client{
		HTTP: server.Client(),
		Now:  func() time.Time { return time.UnixMilli(1_700_000_000_000) },
	}
	return client, server
}

func TestUpdateStoresHeadersAndSelectsFirstProfile(t *testing.T) {
	client, server := newTestClient(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("ETag", `W/"v1"`)
		writer.Header().Set("profile-title", "base64:0KHQtdGA0LLQtdGA0Ys=")
		writer.Header().Set("subscription-userinfo", "upload=0; download=10")
		writer.Header().Set("profile-update-interval", "6")
		fmt.Fprint(writer, "["+profileJSON("Германия", 10808)+"]")
	})
	defer server.Close()

	result, err := client.Update(context.Background(), server.URL, State{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.NotModified {
		t.Fatal("свежий ответ помечен как NotModified")
	}
	state := result.State
	if state.Title != "Серверы" {
		t.Fatalf("title = %q", state.Title)
	}
	if state.ETag != `W/"v1"` || state.UserInfo != "upload=0; download=10" {
		t.Fatalf("заголовки потеряны: %+v", state)
	}
	if state.UpdateIntervalHours != 6 || state.RefreshIntervalHours() != 6 {
		t.Fatalf("интервал обновления = %d", state.UpdateIntervalHours)
	}
	if state.ActiveProfileID != state.Profiles[0].ID {
		t.Fatal("активный профиль не выбран")
	}
	if state.UpdatedAt != 1_700_000_000_000 {
		t.Fatalf("updatedAt = %d", state.UpdatedAt)
	}
	if !state.Authorized() {
		t.Fatal("успешный импорт не авторизовал клиент")
	}
}

func TestUpdateKeepsSelectionAcrossRefresh(t *testing.T) {
	body := "[" + profileJSON("Германия", 10808) + "," + profileJSON("Нидерланды", 10809) + "]"
	client, server := newTestClient(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, body)
	})
	defer server.Close()

	first, err := client.Update(context.Background(), server.URL, State{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	previous := first.State
	previous.ActiveProfileID = previous.Profiles[1].ID

	second, err := client.Update(context.Background(), server.URL, previous)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if second.State.ActiveProfileID != previous.ActiveProfileID {
		t.Fatal("выбор профиля потерян при обновлении")
	}
}

func TestUpdateMatchesRenamedProfileByRemarks(t *testing.T) {
	// The provider rotates the server key: same remarks, new JSON -> new id.
	responses := []string{
		"[" + profileJSON("Германия", 10808) + "]",
		"[" + profileJSON("Германия", 10810) + "]",
	}
	index := 0
	client, server := newTestClient(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, responses[index])
	})
	defer server.Close()

	first, err := client.Update(context.Background(), server.URL, State{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	index = 1
	second, err := client.Update(context.Background(), server.URL, first.State)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if second.State.ActiveProfileID != second.State.Profiles[0].ID {
		t.Fatal("профиль не сопоставлен по remarks")
	}
	if second.State.Profiles[0].ID == first.State.Profiles[0].ID {
		t.Fatal("тест не проверяет смену id")
	}
}

func TestUpdateHonoursNotModified(t *testing.T) {
	client, server := newTestClient(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("If-None-Match") == `"cached"` {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		fmt.Fprint(writer, "["+profileJSON("Германия", 10808)+"]")
	})
	defer server.Close()

	first, err := client.Update(context.Background(), server.URL, State{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	previous := first.State
	previous.ETag = `"cached"`
	previous.UpdatedAt = 1

	result, err := client.Update(context.Background(), server.URL, previous)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !result.NotModified {
		t.Fatal("304 не распознан")
	}
	if len(result.State.Profiles) != len(previous.Profiles) {
		t.Fatal("кэш профилей потерян при 304")
	}
	if result.State.UpdatedAt == previous.UpdatedAt {
		t.Fatal("updatedAt не обновлён при 304")
	}
}

func TestUpdateFailureNeverReturnsState(t *testing.T) {
	client, server := newTestClient(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()

	previous := State{
		URL:      server.URL,
		Profiles: []Profile{{ID: "cached", Remarks: "cached", JSON: "{}"}},
	}
	result, err := client.Update(context.Background(), server.URL, previous)
	if err == nil {
		t.Fatal("HTTP 500 не привёл к ошибке")
	}
	if len(result.State.Profiles) != 0 {
		t.Fatal("при ошибке вернулось состояние — кэш может быть затёрт")
	}
}

func TestUpdateRejectsOversizedBody(t *testing.T) {
	client, server := newTestClient(func(writer http.ResponseWriter, request *http.Request) {
		writer.Write([]byte("["))
		chunk := strings.Repeat("a", 64*1024)
		for written := 0; written < maxResponseBytes+1024; written += len(chunk) {
			writer.Write([]byte(chunk))
		}
	})
	defer server.Close()

	if _, err := client.Update(context.Background(), server.URL, State{}); err == nil {
		t.Fatal("ответ больше 2 МБ принят")
	}
}

func TestNormalizeURLRequiresHTTPS(t *testing.T) {
	if _, err := NormalizeURL("http://example.com/sub"); err == nil {
		t.Fatal("HTTP-ссылка принята")
	}
	if _, err := NormalizeURL("не ссылка"); err == nil {
		t.Fatal("мусор принят как ссылка")
	}
	value, err := NormalizeURL("  https://example.com/sub  ")
	if err != nil || value != "https://example.com/sub" {
		t.Fatalf("NormalizeURL = %q, %v", value, err)
	}
}

func TestParseDeepLink(t *testing.T) {
	cases := map[string]string{
		"sa05://add/https%3A%2F%2Fsub.sa05.tech%2Ftoken%2Fjson":                    "https://sub.sa05.tech/token/json",
		"sa05://add/https%3A%2F%2Fexample.com%2Fsub%3Ftoken%3Da%2Bb%26mode%3Djson": "https://example.com/sub?token=a+b&mode=json",
		"SA05://ADD/https%3A%2F%2Fexample.com%2Fsub":                               "https://example.com/sub",
		"https://add/https%3A%2F%2Fexample.com%2Fsub":                              "",
		"sa05://import/https%3A%2F%2Fexample.com%2Fsub":                            "",
		"sa05://add/http%3A%2F%2Fexample.com%2Fsub":                                "",
		"sa05://add/https%3A%2F%ZZ":                                                "",
		"sa05://add/":                                                              "",
		"":                                                                         "",
	}
	for link, want := range cases {
		if got := ParseDeepLink(link); got != want {
			t.Fatalf("ParseDeepLink(%q) = %q, ожидалось %q", link, got, want)
		}
	}
}

func TestAuthorizationAndRefreshGates(t *testing.T) {
	valid := State{
		URL:                 "https://example.com/sub",
		Profiles:            []Profile{{ID: "a"}},
		UpdateIntervalHours: 6,
	}
	if !valid.Authorized() || valid.RefreshIntervalHours() != 6 {
		t.Fatal("валидная подписка не проходит гейт")
	}
	noProfiles := valid
	noProfiles.Profiles = nil
	if noProfiles.Authorized() || noProfiles.RefreshIntervalHours() != 0 {
		t.Fatal("подписка без профилей авторизует клиент")
	}
	noURL := valid
	noURL.URL = ""
	if noURL.Authorized() || noURL.RefreshIntervalHours() != 0 {
		t.Fatal("подписка без URL авторизует клиент")
	}
	noInterval := valid
	noInterval.UpdateIntervalHours = 0
	if noInterval.RefreshIntervalHours() != 0 {
		t.Fatal("периодическое обновление включено без интервала провайдера")
	}
}

func TestActiveProfileFallsBackToFirst(t *testing.T) {
	state := State{
		Profiles:        []Profile{{ID: "a"}, {ID: "b"}},
		ActiveProfileID: "gone",
	}
	if active := state.ActiveProfile(); active == nil || active.ID != "a" {
		t.Fatalf("ActiveProfile = %+v", active)
	}
	empty := State{}
	if empty.ActiveProfile() != nil {
		t.Fatal("пустая подписка вернула профиль")
	}
}

func TestParseServerRemark(t *testing.T) {
	cases := []struct {
		raw  string
		name string
		flag string
	}{
		{"🇩🇪 Frankfurt", "Frankfurt", "🇩🇪"},
		{"Amsterdam | 🇳🇱", "Amsterdam", "🇳🇱"},
		{"Primary server", "Primary server", ""},
	}
	for _, item := range cases {
		got := ParseServerRemark(item.raw)
		if got.Name != item.name || got.Flag != item.flag {
			t.Fatalf("ParseServerRemark(%q) = %+v", item.raw, got)
		}
	}
}
