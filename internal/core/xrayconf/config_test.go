package xrayconf

import (
	"encoding/json"
	"strings"
	"testing"
)

// Fixtures mirror the Android client's XrayConfigTest so both clients are pinned to
// the same acceptance rules.
const realityConfig = `{
  "inbounds": [{
    "tag": "socks",
    "listen": "127.0.0.1",
    "port": 10808,
    "protocol": "socks",
    "settings": {"udp": true}
  }],
  "outbounds": [
    {
      "tag": "proxy",
      "protocol": "vless",
      "settings": {
        "vnext": [
          {"address": "one.example", "port": 443, "users": [{"id": "uuid"}]},
          {"address": "two.example", "port": 8443, "users": [{"id": "uuid"}]}
        ]
      },
      "streamSettings": {"security": "reality"}
    },
    {
      "tag": "hy2",
      "protocol": "hysteria",
      "settings": {"address": "hy.example", "port": 443, "version": 2},
      "streamSettings": {
        "network": "hysteria",
        "hysteriaSettings": {"version": 2, "auth": "secret"},
        "security": "tls"
      }
    },
    {"tag": "direct", "protocol": "freedom"}
  ],
  "burstObservatory": {
    "pingConfig": {
      "timeout": "3s",
      "destination": "https://example.com/generate_204"
    }
  }
}`

const beelineConfig = `{
  "inbounds": [{
    "tag": "socks",
    "listen": "127.0.0.1",
    "port": 20808,
    "protocol": "socks",
    "settings": {"udp": true, "auth": "noauth"}
  }],
  "outbounds": [
    {
      "tag": "proxy",
      "protocol": "vless",
      "settings": {
        "vnext": [{
          "address": "48typmw3qq.a.trbcdn.net",
          "port": 443,
          "users": [{"id": "user-uuid", "encryption": "mlkem768x25519plus.native.0rtt.SECRET"}]
        }]
      },
      "streamSettings": {
        "network": "xhttp",
        "security": "tls",
        "xhttpSettings": {
          "mode": "packet-up",
          "host": "48typmw3qq.a.trbcdn.net",
          "path": "/assets/api/v1/",
          "xPaddingBytes": "100-500",
          "xPaddingObfsMode": true,
          "xPaddingPlacement": "header",
          "xPaddingMethod": "tokenish"
        },
        "tlsSettings": {
          "serverName": "48typmw3qq.a.trbcdn.net",
          "alpn": ["h2", "http/1.1"]
        }
      }
    },
    {"tag": "direct", "protocol": "freedom"},
    {"tag": "block", "protocol": "blackhole"}
  ]
}`

func decode(t *testing.T, raw string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		t.Fatalf("не разобрать JSON: %v", err)
	}
	return root
}

func outbound(t *testing.T, root map[string]any, index int) map[string]any {
	t.Helper()
	outbounds, ok := root["outbounds"].([]any)
	if !ok || index >= len(outbounds) {
		t.Fatalf("нет outbound %d", index)
	}
	entry, ok := outbounds[index].(map[string]any)
	if !ok {
		t.Fatalf("outbound %d не объект", index)
	}
	return entry
}

func xhttpOf(t *testing.T, root map[string]any, index int) map[string]any {
	t.Helper()
	stream, ok := outbound(t, root, index)["streamSettings"].(map[string]any)
	if !ok {
		t.Fatal("нет streamSettings")
	}
	xhttp, ok := stream["xhttpSettings"].(map[string]any)
	if !ok {
		t.Fatal("нет xhttpSettings")
	}
	return xhttp
}

func TestValidatePreservesXhttpPaddingAndTLS(t *testing.T) {
	validated, err := Validate(beelineConfig)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.SocksPort != 20808 {
		t.Fatalf("socksPort = %d, ожидался 20808", validated.SocksPort)
	}
	root := decode(t, validated.RuntimeJSON)
	stream := outbound(t, root, 0)["streamSettings"].(map[string]any)
	if stream["network"] != "xhttp" || stream["security"] != "tls" {
		t.Fatalf("transport изменён: %v", stream)
	}
	xhttp := xhttpOf(t, root, 0)
	for key, want := range map[string]any{
		"mode":              "packet-up",
		"path":              "/assets/api/v1/",
		"host":              "48typmw3qq.a.trbcdn.net",
		"xPaddingBytes":     "100-500",
		"xPaddingObfsMode":  true,
		"xPaddingPlacement": "header",
		"xPaddingMethod":    "tokenish",
	} {
		if xhttp[key] != want {
			t.Fatalf("xhttpSettings[%q] = %v, ожидалось %v", key, xhttp[key], want)
		}
	}
	tls := stream["tlsSettings"].(map[string]any)
	alpn := tls["alpn"].([]any)
	if len(alpn) != 2 || alpn[0] != "h2" || alpn[1] != "http/1.1" {
		t.Fatalf("alpn изменён: %v", alpn)
	}
}

func TestValidateRejectsBrokenSocksInbound(t *testing.T) {
	cases := map[string]string{
		"нет inbounds": `{"outbounds": []}`,
		"нет socks":    `{"inbounds": [{"protocol": "http", "port": 1080}]}`,
		"чужой listen": `{"inbounds": [{"protocol": "socks", "listen": "10.0.0.1", "port": 1080, "settings": {"udp": true}}]}`,
		"плохой порт":  `{"inbounds": [{"protocol": "socks", "listen": "127.0.0.1", "port": 0, "settings": {"udp": true}}]}`,
		"udp выключен": `{"inbounds": [{"protocol": "socks", "listen": "127.0.0.1", "port": 1080, "settings": {"udp": false}}]}`,
		"нет settings": `{"inbounds": [{"protocol": "socks", "listen": "127.0.0.1", "port": 1080}]}`,
		"битый JSON":   `{`,
	}
	for name, raw := range cases {
		if _, err := Validate(raw); err == nil {
			t.Fatalf("%s: ожидалась ошибка", name)
		}
	}
}

func TestValidateLeavesNonXhttpOutboundShapeUnchanged(t *testing.T) {
	validated, err := Validate(realityConfig)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	root := decode(t, validated.RuntimeJSON)
	vless := outbound(t, root, 0)
	if vless["protocol"] != "vless" {
		t.Fatalf("протокол изменён: %v", vless["protocol"])
	}
	stream := vless["streamSettings"].(map[string]any)
	if stream["security"] != "reality" {
		t.Fatalf("security изменён: %v", stream["security"])
	}
	if _, exists := stream["xhttpSettings"]; exists {
		t.Fatal("в reality-профиль добавлен xhttpSettings")
	}
	vnext := vless["settings"].(map[string]any)["vnext"].([]any)
	if len(vnext) != 2 {
		t.Fatalf("vnext = %d записей, ожидалось 2", len(vnext))
	}
}

func TestApplyBeelinePaddingFillsMissingParams(t *testing.T) {
	root := decode(t, beelineConfig)
	xhttp := xhttpOf(t, root, 0)
	for _, key := range []string{"xPaddingBytes", "xPaddingObfsMode", "xPaddingPlacement", "xPaddingMethod"} {
		delete(xhttp, key)
	}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	patched, err := ApplyBeelinePadding(string(raw))
	if err != nil {
		t.Fatalf("ApplyBeelinePadding: %v", err)
	}
	result := xhttpOf(t, decode(t, patched), 0)
	for key, want := range map[string]any{
		"xPaddingBytes":     "100-500",
		"xPaddingObfsMode":  true,
		"xPaddingPlacement": "header",
		"xPaddingMethod":    "tokenish",
	} {
		if result[key] != want {
			t.Fatalf("%s = %v, ожидалось %v", key, result[key], want)
		}
	}
}

func TestApplyBeelinePaddingKeepsProviderValues(t *testing.T) {
	root := decode(t, beelineConfig)
	xhttpOf(t, root, 0)["xPaddingBytes"] = "50-200"
	delete(xhttpOf(t, root, 0), "xPaddingMethod")
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	patched, err := ApplyBeelinePadding(string(raw))
	if err != nil {
		t.Fatalf("ApplyBeelinePadding: %v", err)
	}
	result := xhttpOf(t, decode(t, patched), 0)
	if result["xPaddingBytes"] != "50-200" {
		t.Fatalf("значение провайдера перезаписано: %v", result["xPaddingBytes"])
	}
	if result["xPaddingMethod"] != "tokenish" {
		t.Fatalf("пропуск не заполнен: %v", result["xPaddingMethod"])
	}
}

func TestApplyBeelinePaddingIgnoresNonXhttpProfiles(t *testing.T) {
	patched, err := ApplyBeelinePadding(realityConfig)
	if err != nil {
		t.Fatalf("ApplyBeelinePadding: %v", err)
	}
	if patched != realityConfig {
		t.Fatal("профиль без xhttp изменён")
	}
}

func TestExtractsEveryProxyEndpoint(t *testing.T) {
	hosts, err := ExtractHosts(realityConfig)
	if err != nil {
		t.Fatalf("ExtractHosts: %v", err)
	}
	if len(hosts) != 3 {
		t.Fatalf("хостов %d, ожидалось 3", len(hosts))
	}
	if hosts[0].Address != "one.example" || hosts[1].Port != 8443 {
		t.Fatalf("неверные endpoint-ы: %+v", hosts)
	}
	if hosts[2].Protocol != "hysteria" || hosts[2].Address != "hy.example" {
		t.Fatalf("hysteria endpoint потерян: %+v", hosts[2])
	}
}

func TestPingConfigKeepsOnlySelectedEndpointAndForcesRouting(t *testing.T) {
	hosts, err := ExtractHosts(realityConfig)
	if err != nil {
		t.Fatalf("ExtractHosts: %v", err)
	}
	ping, err := BuildPingConfig(realityConfig, hosts[1], 32123)
	if err != nil {
		t.Fatalf("BuildPingConfig: %v", err)
	}
	root := decode(t, ping.RuntimeJSON)
	inbound := root["inbounds"].([]any)[0].(map[string]any)
	if intOf(inbound["port"], -1) != 32123 {
		t.Fatalf("порт инбаунда = %v", inbound["port"])
	}
	vnext := outbound(t, root, 0)["settings"].(map[string]any)["vnext"].([]any)
	if len(vnext) != 1 || vnext[0].(map[string]any)["address"] != "two.example" {
		t.Fatalf("выбран не тот endpoint: %v", vnext)
	}
	rule := root["routing"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	if rule["outboundTag"] != "proxy" {
		t.Fatalf("правило указывает на %v", rule["outboundTag"])
	}
	if ping.ProbeURL != "https://example.com/generate_204" || ping.TimeoutMS != 3000 {
		t.Fatalf("параметры пробы: %+v", ping)
	}
	if _, exists := root["burstObservatory"]; exists {
		t.Fatal("burstObservatory не удалён")
	}
}

func TestPingConfigPreservesProviderHysteriaShape(t *testing.T) {
	hosts, err := ExtractHosts(realityConfig)
	if err != nil {
		t.Fatalf("ExtractHosts: %v", err)
	}
	ping, err := BuildPingConfig(realityConfig, hosts[2], 32124)
	if err != nil {
		t.Fatalf("BuildPingConfig: %v", err)
	}
	root := decode(t, ping.RuntimeJSON)
	hysteria := outbound(t, root, 1)
	if hysteria["protocol"] != "hysteria" {
		t.Fatalf("протокол изменён: %v", hysteria["protocol"])
	}
	settings := hysteria["settings"].(map[string]any)
	if settings["address"] != "hy.example" || intOf(settings["version"], 0) != 2 {
		t.Fatalf("settings изменены: %v", settings)
	}
	stream := hysteria["streamSettings"].(map[string]any)
	if stream["network"] != "hysteria" || stream["security"] != "tls" {
		t.Fatalf("streamSettings изменены: %v", stream)
	}
	if stream["hysteriaSettings"].(map[string]any)["auth"] != "secret" {
		t.Fatal("hysteria auth потерян")
	}
}

func TestPingConfigDefaultsWithoutObservatory(t *testing.T) {
	root := decode(t, realityConfig)
	delete(root, "burstObservatory")
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	hosts, err := ExtractHosts(string(raw))
	if err != nil {
		t.Fatalf("ExtractHosts: %v", err)
	}
	ping, err := BuildPingConfig(string(raw), hosts[0], 32125)
	if err != nil {
		t.Fatalf("BuildPingConfig: %v", err)
	}
	if ping.ProbeURL != DefaultProbeURL || ping.TimeoutMS != 8000 {
		t.Fatalf("значения по умолчанию: %+v", ping)
	}
	if !strings.Contains(ping.RuntimeJSON, "__ping_in") {
		t.Fatal("инбаунд пробы не добавлен")
	}
}

func TestEnsureHTTPInboundAddsLoopbackInbound(t *testing.T) {
	updated, port, err := EnsureHTTPInbound(realityConfig, 10809, false)
	if err != nil {
		t.Fatalf("EnsureHTTPInbound: %v", err)
	}
	if port != 10809 {
		t.Fatalf("порт = %d", port)
	}
	root := decode(t, updated)
	inbounds := root["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("инбаундов %d, ожидалось 2", len(inbounds))
	}
	added := inbounds[1].(map[string]any)
	if added["protocol"] != "http" || added["listen"] != "127.0.0.1" {
		t.Fatalf("инбаунд неверный: %v", added)
	}
	if added["tag"] != "__sa05_http" {
		t.Fatalf("тег = %v", added["tag"])
	}
	// The provider's SOCKS inbound must survive untouched.
	if _, err := Validate(updated); err != nil {
		t.Fatalf("конфиг перестал проходить валидацию: %v", err)
	}
}

func TestEnsureHTTPInboundReusesProviderInbound(t *testing.T) {
	root := decode(t, realityConfig)
	root["inbounds"] = append(root["inbounds"].([]any), map[string]any{
		"tag":      "http-in",
		"listen":   "127.0.0.1",
		"port":     18080,
		"protocol": "http",
	})
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	updated, port, err := EnsureHTTPInbound(string(raw), 10809, false)
	if err != nil {
		t.Fatalf("EnsureHTTPInbound: %v", err)
	}
	if port != 18080 {
		t.Fatalf("порт провайдера проигнорирован: %d", port)
	}
	if len(decode(t, updated)["inbounds"].([]any)) != 2 {
		t.Fatal("добавлен лишний инбаунд")
	}
}

func TestEnsureHTTPInboundAvoidsTagCollision(t *testing.T) {
	root := decode(t, realityConfig)
	root["outbounds"] = append(root["outbounds"].([]any), map[string]any{
		"tag":      "__sa05_http",
		"protocol": "freedom",
	})
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	updated, _, err := EnsureHTTPInbound(string(raw), 10809, false)
	if err != nil {
		t.Fatalf("EnsureHTTPInbound: %v", err)
	}
	inbounds := decode(t, updated)["inbounds"].([]any)
	added := inbounds[len(inbounds)-1].(map[string]any)
	if added["tag"] != "__sa05_http-2" {
		t.Fatalf("тег = %v, ожидался __sa05_http-2", added["tag"])
	}
}

func TestProbeTimeoutClamping(t *testing.T) {
	cases := map[string]int{
		`{"burstObservatory": {"pingConfig": {"timeout": "500ms"}}}`: 1000,
		`{"burstObservatory": {"pingConfig": {"timeout": "45s"}}}`:   30000,
		`{"burstObservatory": {"pingConfig": {"timeout": "1m"}}}`:    30000,
		`{"burstObservatory": {"pingConfig": {"timeout": "bogus"}}}`: 8000,
		`{}`: 8000,
	}
	for raw, want := range cases {
		root := decode(t, raw)
		if got := probeTimeoutMS(root); got != want {
			t.Fatalf("%s -> %d, ожидалось %d", raw, got, want)
		}
	}
}
