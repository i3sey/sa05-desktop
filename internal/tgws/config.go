// Package tgws runs the built-in Telegram MTProto proxy.
//
// Telegram points at one stable SA05 endpoint (127.0.0.1:1443) with one stable secret;
// switching the upstream transport — Cloudflare-fronted WebSocket, direct WebSocket or
// plain TCP to the datacenter — never changes what the user configured in Telegram.
//
// This file holds the parts the UI needs before the proxy itself is vendored; it is the
// port of TelegramProxyConfig from the Android client's XrayPreferences.kt.
package tgws

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Port is the fixed MTProto listener. It is deliberately stable across transports and
// restarts so Telegram is configured exactly once.
const Port = 1443

// Transport selects how the proxy reaches Telegram datacenters.
type Transport string

const (
	// TransportAuto tries Cloudflare-fronted WebSocket, then direct WebSocket, then TCP.
	TransportAuto Transport = "auto"
	// TransportCloudflare forces the Cloudflare-fronted WebSocket path.
	TransportCloudflare Transport = "cf"
	// TransportWebSocket forces a direct WebSocket connection to the datacenter.
	TransportWebSocket Transport = "ws"
	// TransportTCP forces a plain MTProto TCP connection to the datacenter.
	TransportTCP Transport = "tcp"
)

// ParseTransport maps stored values onto a transport, defaulting to auto.
func ParseTransport(value string) Transport {
	switch Transport(strings.ToLower(strings.TrimSpace(value))) {
	case TransportCloudflare:
		return TransportCloudflare
	case TransportWebSocket:
		return TransportWebSocket
	case TransportTCP:
		return TransportTCP
	default:
		return TransportAuto
	}
}

// Title is the human-readable name of the transport.
func (t Transport) Title() string {
	switch t {
	case TransportCloudflare:
		return "Через Cloudflare"
	case TransportWebSocket:
		return "Прямой WebSocket"
	case TransportTCP:
		return "Прямой TCP"
	default:
		return "Автоматически"
	}
}

// ValidSecret reports whether value is a 32-character lowercase-hex MTProto secret.
func ValidSecret(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, symbol := range value {
		isDigit := symbol >= '0' && symbol <= '9'
		isHexLetter := symbol >= 'a' && symbol <= 'f'
		if !isDigit && !isHexLetter {
			return false
		}
	}
	return true
}

// GenerateSecret produces a fresh 16-byte secret. It never leaves the machine: the proxy
// runs locally, so the secret only authenticates the local Telegram client.
func GenerateSecret() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("секрет не сгенерирован: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

// EnsureSecret returns the stored secret, replacing it when it is missing or malformed.
func EnsureSecret(stored string) (string, error) {
	if ValidSecret(stored) {
		return stored, nil
	}
	return GenerateSecret()
}

// ProxyURI builds the link that configures Telegram. webFallback returns the https://t.me
// form for clients that do not register the tg:// scheme.
func ProxyURI(secret string, webFallback bool) (string, error) {
	if !ValidSecret(secret) {
		return "", errors.New("Некорректный секрет Telegram Proxy")
	}
	base := "tg://proxy"
	if webFallback {
		base = "https://t.me/proxy"
	}
	// The dd prefix requests random-padded MTProto, which survives DPI far better than
	// the bare secret form.
	return fmt.Sprintf("%s?server=127.0.0.1&port=%d&secret=dd%s", base, Port, secret), nil
}
