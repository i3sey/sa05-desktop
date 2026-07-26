package tgws

import (
	"strings"
	"testing"
)

func TestValidSecret(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef"
	if !ValidSecret(valid) {
		t.Fatal("корректный секрет отклонён")
	}
	invalid := []string{
		"",
		"0123456789abcdef",                  // короткий
		"0123456789abcdef0123456789abcdefa", // длинный
		"0123456789ABCDEF0123456789abcdef",  // верхний регистр
		"0123456789abcdef0123456789abcdeg",  // не hex
	}
	for _, value := range invalid {
		if ValidSecret(value) {
			t.Fatalf("некорректный секрет принят: %q", value)
		}
	}
}

func TestGenerateAndEnsureSecret(t *testing.T) {
	first, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	if !ValidSecret(first) {
		t.Fatalf("сгенерирован некорректный секрет: %q", first)
	}
	second, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	if first == second {
		t.Fatal("секрет не случаен")
	}
	// A stored valid secret must survive, otherwise Telegram would need reconfiguring
	// on every start.
	kept, err := EnsureSecret(first)
	if err != nil || kept != first {
		t.Fatalf("EnsureSecret заменил валидный секрет: %q, %v", kept, err)
	}
	replaced, err := EnsureSecret("мусор")
	if err != nil || !ValidSecret(replaced) {
		t.Fatalf("EnsureSecret не восстановил секрет: %q, %v", replaced, err)
	}
}

func TestProxyURI(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	uri, err := ProxyURI(secret, false)
	if err != nil {
		t.Fatalf("ProxyURI: %v", err)
	}
	want := "tg://proxy?server=127.0.0.1&port=1443&secret=dd" + secret
	if uri != want {
		t.Fatalf("ссылка = %q, ожидалось %q", uri, want)
	}
	web, err := ProxyURI(secret, true)
	if err != nil {
		t.Fatalf("ProxyURI(web): %v", err)
	}
	if !strings.HasPrefix(web, "https://t.me/proxy?") {
		t.Fatalf("web-ссылка = %q", web)
	}
	if _, err := ProxyURI("bad", false); err == nil {
		t.Fatal("ссылка построена по некорректному секрету")
	}
}

func TestParseTransport(t *testing.T) {
	cases := map[string]Transport{
		"":       TransportAuto,
		"auto":   TransportAuto,
		"AUTO":   TransportAuto,
		" cf ":   TransportCloudflare,
		"ws":     TransportWebSocket,
		"tcp":    TransportTCP,
		"чепуха": TransportAuto,
	}
	for value, want := range cases {
		if got := ParseTransport(value); got != want {
			t.Fatalf("ParseTransport(%q) = %s, ожидалось %s", value, got, want)
		}
	}
	if TransportTCP.Title() != "Прямой TCP" {
		t.Fatalf("название транспорта = %q", TransportTCP.Title())
	}
}
