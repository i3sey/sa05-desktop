package xrayconf

import "testing"

func TestOverrideSocksPort(t *testing.T) {
	updated, err := OverrideSocksPort(realityConfig, 21808)
	if err != nil {
		t.Fatalf("OverrideSocksPort: %v", err)
	}
	validated, err := Validate(updated)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.SocksPort != 21808 {
		t.Fatalf("порт = %d, ожидался 21808", validated.SocksPort)
	}
	// The outbound side must stay untouched: rebinding is a local concern.
	root := decode(t, updated)
	if outbound(t, root, 0)["protocol"] != "vless" {
		t.Fatal("outbound изменён при смене порта")
	}
}

func TestOverrideSocksPortRejectsBadInput(t *testing.T) {
	if _, err := OverrideSocksPort(realityConfig, 0); err == nil {
		t.Fatal("нулевой порт принят")
	}
	if _, err := OverrideSocksPort(realityConfig, 70000); err == nil {
		t.Fatal("порт вне диапазона принят")
	}
	if _, err := OverrideSocksPort(`{"inbounds": []}`, 21808); err == nil {
		t.Fatal("конфиг без SOCKS-инбаунда принят")
	}
}

func TestEnsureHTTPInboundForceRebindsProviderInbound(t *testing.T) {
	root := decode(t, realityConfig)
	root["inbounds"] = append(root["inbounds"].([]any), map[string]any{
		"tag":      "http-in",
		"listen":   "127.0.0.1",
		"port":     10809,
		"protocol": "http",
	})
	raw, err := encode(root)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Without force the provider's port wins...
	_, port, err := EnsureHTTPInbound(raw, 21809, false)
	if err != nil {
		t.Fatalf("EnsureHTTPInbound: %v", err)
	}
	if port != 10809 {
		t.Fatalf("порт без force = %d", port)
	}

	// ...with force the collision is resolved by rebinding.
	updated, forced, err := EnsureHTTPInbound(raw, 21809, true)
	if err != nil {
		t.Fatalf("EnsureHTTPInbound(force): %v", err)
	}
	if forced != 21809 {
		t.Fatalf("порт с force = %d", forced)
	}
	inbounds := decode(t, updated)["inbounds"].([]any)
	last := inbounds[len(inbounds)-1].(map[string]any)
	if intOf(last["port"], 0) != 21809 {
		t.Fatalf("инбаунд не перепривязан: %v", last)
	}
}

func TestUsesGeoAssets(t *testing.T) {
	if UsesGeoAssets(realityConfig) {
		t.Fatal("профиль без гео-правил помечен как требующий баз")
	}
	withGeo := `{"routing": {"rules": [{"ip": ["geoip:private"], "outboundTag": "direct"}]}}`
	if !UsesGeoAssets(withGeo) {
		t.Fatal("правило geoip: не распознано")
	}
	withSite := `{"routing": {"rules": [{"domain": ["geosite:youtube"]}]}}`
	if !UsesGeoAssets(withSite) {
		t.Fatal("правило geosite: не распознано")
	}
}
