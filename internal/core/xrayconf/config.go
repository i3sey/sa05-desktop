// Package xrayconf validates and derives runtime Xray configurations.
//
// The subscription hands out complete Xray configs. They are never rewritten in
// storage: routing, balancers, VLESS, Reality, gRPC and XHTTP settings stay exactly
// as the provider sent them. Only runtime copies are augmented, and every added
// element uses a collision-safe __sa05_ tag.
//
// Ported from the Android client's XrayConfig.kt so both clients accept and reject
// the same configs.
package xrayconf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Validated is a config that satisfies the SOCKS inbound contract.
type Validated struct {
	RuntimeJSON string
	SocksPort   int
}

// Host is one dialable endpoint of one outbound, used by the latency probe.
type Host struct {
	ID            string
	OutboundIndex int
	EndpointIndex int
	Tag           string
	Protocol      string
	Address       string
	Port          int
}

// PingConfig is a single-outbound runtime config plus the probe parameters taken
// from the provider's observatory settings.
type PingConfig struct {
	RuntimeJSON string
	ProbeURL    string
	TimeoutMS   int
}

// DefaultProbeURL is used when the provider config carries no observatory settings.
const DefaultProbeURL = "https://www.gstatic.com/generate_204"

var directOutbounds = map[string]bool{
	"freedom":   true,
	"blackhole": true,
	"dns":       true,
	"loopback":  true,
}

var validSocksListen = map[string]bool{
	"127.0.0.1": true,
	"localhost": true,
	"0.0.0.0":   true,
}

// Validate checks the SOCKS inbound contract required to feed a TUN device and
// returns the reserialized config together with that inbound's port.
func Validate(raw string) (Validated, error) {
	root, err := parse(raw)
	if err != nil {
		return Validated{}, err
	}
	inbounds, ok := root["inbounds"].([]any)
	if !ok {
		return Validated{}, errors.New("Нет массива inbounds")
	}
	for _, entry := range inbounds {
		inbound, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if str(inbound["protocol"]) != "socks" {
			continue
		}
		listen := str(inbound["listen"])
		if listen == "" {
			listen = "127.0.0.1"
		}
		if !validSocksListen[listen] {
			return Validated{}, errors.New(
				"SOCKS inbound должен слушать 127.0.0.1, localhost или 0.0.0.0")
		}
		port := intOf(inbound["port"], -1)
		if port < 1 || port > 65535 {
			return Validated{}, errors.New("У SOCKS inbound некорректный port")
		}
		settings, _ := inbound["settings"].(map[string]any)
		if settings == nil || !boolOf(settings["udp"]) {
			return Validated{}, errors.New("Для VPN нужен settings.udp=true у SOCKS inbound")
		}
		encoded, err := encode(root)
		if err != nil {
			return Validated{}, err
		}
		return Validated{RuntimeJSON: encoded, SocksPort: port}, nil
	}
	return Validated{}, errors.New("Нужен SOCKS inbound на 127.0.0.1, localhost или 0.0.0.0")
}

// ExtractHosts lists every dialable endpoint of every proxying outbound.
func ExtractHosts(raw string) ([]Host, error) {
	root, err := parse(raw)
	if err != nil {
		return nil, err
	}
	outbounds, _ := root["outbounds"].([]any)
	result := make([]Host, 0, len(outbounds))
	for outboundIndex, entry := range outbounds {
		outbound, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		protocol := str(outbound["protocol"])
		if directOutbounds[protocol] {
			continue
		}
		tag := str(outbound["tag"])
		if strings.TrimSpace(tag) == "" {
			tag = fmt.Sprintf("outbound-%d", outboundIndex)
		}
		settings, _ := outbound["settings"].(map[string]any)
		if settings == nil {
			continue
		}
		endpoints := endpointsOf(protocol, settings)
		if endpoints == nil {
			continue
		}
		for endpointIndex, raw := range endpoints {
			endpoint, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			address := str(endpoint["address"])
			port := intOf(endpoint["port"], -1)
			if strings.TrimSpace(address) == "" || port < 1 || port > 65535 {
				continue
			}
			result = append(result, Host{
				ID:            fmt.Sprintf("%d:%d:%s:%d", outboundIndex, endpointIndex, address, port),
				OutboundIndex: outboundIndex,
				EndpointIndex: endpointIndex,
				Tag:           tag,
				Protocol:      protocol,
				Address:       address,
				Port:          port,
			})
		}
	}
	return result, nil
}

// ApplyBeelinePadding fills in XHTTP padding parameters when a VLESS+XHTTP outbound
// is missing them.
//
// Beeline CDN rejects XHTTP requests on two signals: the long UUID session ID (fixed
// by the patched core) and the stock padding. Without the padding params the CDN
// answers 403 for the whole tunnel even though the TCP handshake — what latency
// probes measure — succeeds. Provider-supplied values are never overridden, so this
// is idempotent and a no-op for non-XHTTP profiles.
func ApplyBeelinePadding(raw string) (string, error) {
	root, err := parse(raw)
	if err != nil {
		return "", err
	}
	outbounds, _ := root["outbounds"].([]any)
	changed := false
	for _, entry := range outbounds {
		outbound, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if strings.ToLower(str(outbound["protocol"])) != "vless" {
			continue
		}
		stream, _ := outbound["streamSettings"].(map[string]any)
		if stream == nil {
			continue
		}
		if strings.ToLower(str(stream["network"])) != "xhttp" {
			continue
		}
		xhttp, _ := stream["xhttpSettings"].(map[string]any)
		if xhttp == nil {
			xhttp = map[string]any{}
			stream["xhttpSettings"] = xhttp
		}
		defaults := []struct {
			key   string
			value any
		}{
			{"xPaddingBytes", "100-500"},
			{"xPaddingObfsMode", true},
			{"xPaddingPlacement", "header"},
			{"xPaddingMethod", "tokenish"},
		}
		for _, item := range defaults {
			if _, exists := xhttp[item.key]; !exists {
				xhttp[item.key] = item.value
				changed = true
			}
		}
	}
	if !changed {
		return raw, nil
	}
	return encode(root)
}

// ApplyOutboundMark stamps a routing mark on every proxying outbound.
//
// When the TUN device holds the default route, the core's own connections to the server
// would be routed back into that tunnel. The mark is what the helper's routing policy
// matches to send them out through the real interface instead. Provider-supplied sockopt
// values are preserved; only the mark is filled in.
func ApplyOutboundMark(raw string, mark int) (string, error) {
	if mark <= 0 {
		return raw, nil
	}
	root, err := parse(raw)
	if err != nil {
		return "", err
	}
	outbounds, _ := root["outbounds"].([]any)
	changed := false
	for _, entry := range outbounds {
		outbound, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if directOutbounds[str(outbound["protocol"])] && str(outbound["protocol"]) != "freedom" {
			// blackhole/dns/loopback never open a socket, so a mark would be meaningless.
			continue
		}
		stream, _ := outbound["streamSettings"].(map[string]any)
		if stream == nil {
			stream = map[string]any{}
			outbound["streamSettings"] = stream
		}
		sockopt, _ := stream["sockopt"].(map[string]any)
		if sockopt == nil {
			sockopt = map[string]any{}
			stream["sockopt"] = sockopt
		}
		if _, exists := sockopt["mark"]; exists {
			continue
		}
		sockopt["mark"] = mark
		changed = true
	}
	if !changed {
		return raw, nil
	}
	return encode(root)
}

// UsesGeoAssets reports whether a config references the geoip/geosite databases. Xray
// refuses to start when a referenced database is missing, so this decides whether the
// ~30 MB downloads are required at all.
func UsesGeoAssets(raw string) bool {
	return strings.Contains(raw, "geoip:") || strings.Contains(raw, "geosite:")
}

// OverrideSocksPort moves the provider's SOCKS inbound to another loopback port.
//
// Desktop machines often already run another proxy client on the provider's port, and
// Xray binds with SO_REUSEPORT — two listeners then silently split the traffic instead
// of failing. Rebinding is the only way to be sure the tunnel that answers is ours.
func OverrideSocksPort(raw string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("некорректный порт SOCKS-инбаунда: %d", port)
	}
	root, err := parse(raw)
	if err != nil {
		return "", err
	}
	inbounds, ok := root["inbounds"].([]any)
	if !ok {
		return "", errors.New("Нет массива inbounds")
	}
	for _, entry := range inbounds {
		inbound, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if str(inbound["protocol"]) != "socks" {
			continue
		}
		listen := str(inbound["listen"])
		if listen == "" {
			listen = "127.0.0.1"
		}
		if !validSocksListen[listen] {
			continue
		}
		inbound["port"] = port
		return encode(root)
	}
	return "", errors.New("Нужен SOCKS inbound на 127.0.0.1, localhost или 0.0.0.0")
}

// EnsureHTTPInbound adds an HTTP inbound on loopback so the system-proxy toggle has
// an endpoint for applications that speak HTTP proxying only. An inbound the provider
// already supplies is reused; the returned port is the one applications must use.
//
// force rebinds the provider's inbound to port, which is how a collision with another
// proxy client already holding that port is resolved.
func EnsureHTTPInbound(raw string, port int, force bool) (string, int, error) {
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("некорректный порт HTTP-инбаунда: %d", port)
	}
	root, err := parse(raw)
	if err != nil {
		return "", 0, err
	}
	inbounds, _ := root["inbounds"].([]any)
	for _, entry := range inbounds {
		inbound, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if str(inbound["protocol"]) != "http" {
			continue
		}
		listen := str(inbound["listen"])
		if listen == "" {
			listen = "127.0.0.1"
		}
		if !validSocksListen[listen] {
			continue
		}
		existing := intOf(inbound["port"], -1)
		if force {
			inbound["port"] = port
			existing = port
		}
		if existing >= 1 && existing <= 65535 {
			encoded, err := encode(root)
			if err != nil {
				return "", 0, err
			}
			return encoded, existing, nil
		}
	}
	tags := tagSet(root)
	inbounds = append(inbounds, map[string]any{
		"tag":      uniqueTag(tags, "__sa05_http"),
		"listen":   "127.0.0.1",
		"port":     port,
		"protocol": "http",
		"settings": map[string]any{"allowTransparent": false},
		"sniffing": map[string]any{
			"enabled":      true,
			"destOverride": []any{"http", "tls"},
		},
	})
	root["inbounds"] = inbounds
	encoded, err := encode(root)
	if err != nil {
		return "", 0, err
	}
	return encoded, port, nil
}

// BuildPingConfig reduces a profile to one outbound reachable through a dedicated
// SOCKS inbound, so the latency probe measures that endpoint and nothing else.
func BuildPingConfig(raw string, host Host, socksPort int) (PingConfig, error) {
	root, err := parse(raw)
	if err != nil {
		return PingConfig{}, err
	}
	outbounds, ok := root["outbounds"].([]any)
	if !ok {
		return PingConfig{}, errors.New("Нет массива outbounds")
	}
	if host.OutboundIndex < 0 || host.OutboundIndex >= len(outbounds) {
		return PingConfig{}, errors.New("Outbound больше не существует")
	}
	selected, ok := outbounds[host.OutboundIndex].(map[string]any)
	if !ok {
		return PingConfig{}, errors.New("Outbound больше не существует")
	}
	settings, _ := selected["settings"].(map[string]any)
	if settings == nil {
		return PingConfig{}, errors.New("У outbound нет settings")
	}

	endpointsKey := ""
	switch {
	case settings["vnext"] != nil:
		endpointsKey = "vnext"
	case settings["servers"] != nil:
		endpointsKey = "servers"
	case str(selected["protocol"]) == "hysteria" && intOf(settings["version"], 0) == 2:
		endpointsKey = ""
	default:
		return PingConfig{}, errors.New("Не найден список серверов outbound")
	}
	if endpointsKey != "" {
		endpoints, _ := settings[endpointsKey].([]any)
		if host.EndpointIndex < 0 || host.EndpointIndex >= len(endpoints) {
			return PingConfig{}, errors.New("Endpoint больше не существует")
		}
		settings[endpointsKey] = []any{endpoints[host.EndpointIndex]}
	}

	targetTag := str(selected["tag"])
	if strings.TrimSpace(targetTag) == "" {
		targetTag = "__ping_target"
		selected["tag"] = targetTag
	}
	root["inbounds"] = []any{map[string]any{
		"tag":      "__ping_in",
		"listen":   "127.0.0.1",
		"port":     socksPort,
		"protocol": "socks",
		"settings": map[string]any{"udp": true, "auth": "noauth"},
	}}
	root["routing"] = map[string]any{
		"domainStrategy": "AsIs",
		"rules": []any{map[string]any{
			"type":        "field",
			"inboundTag":  []any{"__ping_in"},
			"outboundTag": targetTag,
		}},
	}
	delete(root, "observatory")
	delete(root, "burstObservatory")
	delete(root, "api")
	delete(root, "metrics")

	source, err := parse(raw)
	if err != nil {
		return PingConfig{}, err
	}
	probeURL, err := ProbeURL(source)
	if err != nil {
		return PingConfig{}, err
	}
	encoded, err := encode(root)
	if err != nil {
		return PingConfig{}, err
	}
	return PingConfig{
		RuntimeJSON: encoded,
		ProbeURL:    probeURL,
		TimeoutMS:   probeTimeoutMS(source),
	}, nil
}

// ProbeURL resolves the latency probe target: the burst observatory destination,
// then the background observatory probe, then a neutral default.
func ProbeURL(root map[string]any) (string, error) {
	value := str(nested(root, "burstObservatory", "pingConfig")["destination"])
	if strings.TrimSpace(value) == "" {
		observatory, _ := root["observatory"].(map[string]any)
		value = str(observatory["destination"])
		if strings.TrimSpace(value) == "" {
			value = str(observatory["probeUrl"])
		}
	}
	if strings.TrimSpace(value) == "" {
		value = DefaultProbeURL
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", errors.New("Некорректный URL для пинга")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("Пинг поддерживает только HTTP/HTTPS URL")
	}
	return value, nil
}

// ProbeURLOf is the string-input form of ProbeURL.
func ProbeURLOf(raw string) (string, error) {
	root, err := parse(raw)
	if err != nil {
		return "", err
	}
	return ProbeURL(root)
}

var durationPattern = regexp.MustCompile(`^(\d+)(ms|s|m)$`)

func probeTimeoutMS(root map[string]any) int {
	value := str(nested(root, "burstObservatory", "pingConfig")["timeout"])
	if strings.TrimSpace(value) == "" {
		return 8_000
	}
	match := durationPattern.FindStringSubmatch(value)
	if match == nil {
		return 8_000
	}
	amount, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 8_000
	}
	multiplier := int64(1)
	switch match[2] {
	case "s":
		multiplier = 1_000
	case "m":
		multiplier = 60_000
	}
	total := amount * multiplier
	if total < 1_000 {
		total = 1_000
	}
	if total > 30_000 {
		total = 30_000
	}
	return int(total)
}

func endpointsOf(protocol string, settings map[string]any) []any {
	if vnext, ok := settings["vnext"].([]any); ok {
		return vnext
	}
	if servers, ok := settings["servers"].([]any); ok {
		return servers
	}
	if protocol == "hysteria" && intOf(settings["version"], 0) == 2 {
		return []any{map[string]any{
			"address": str(settings["address"]),
			"port":    intOf(settings["port"], 0),
		}}
	}
	return nil
}

func tagSet(root map[string]any) map[string]bool {
	tags := map[string]bool{}
	for _, key := range []string{"inbounds", "outbounds"} {
		entries, _ := root[key].([]any)
		for _, entry := range entries {
			item, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if tag := strings.TrimSpace(str(item["tag"])); tag != "" {
				tags[tag] = true
			}
		}
	}
	return tags
}

func uniqueTag(tags map[string]bool, base string) string {
	value := base
	for suffix := 2; tags[value]; suffix++ {
		value = fmt.Sprintf("%s-%d", base, suffix)
	}
	tags[value] = true
	return value
}

func parse(raw string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("JSON не разобран: %w", err)
	}
	if root == nil {
		return nil, errors.New("JSON не разобран: пустой объект")
	}
	return root, nil
}

func encode(root map[string]any) (string, error) {
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(root); err != nil {
		return "", fmt.Errorf("JSON не сериализован: %w", err)
	}
	return strings.TrimRight(buffer.String(), "\n"), nil
}

func nested(root map[string]any, keys ...string) map[string]any {
	current := root
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			return map[string]any{}
		}
		current = next
	}
	return current
}

func str(value any) string {
	text, _ := value.(string)
	return text
}

func boolOf(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func intOf(value any, fallback int) int {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return fallback
		}
		return int(parsed)
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		parsed, err := strconv.Atoi(typed)
		if err != nil {
			return fallback
		}
		return parsed
	}
	return fallback
}
