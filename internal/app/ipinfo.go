package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/proxy"

	"github.com/fife/sa05-desktop/internal/core/state"
)

// ipInfoTimeout bounds the whole lookup: it is a one-shot user action, not a probe.
const ipInfoTimeout = 15 * time.Second

// ipInfoURL is the public echo answering {ip, city, country}. Tests point it at a local
// server so they never depend on the internet.
var ipInfoURL = "https://ipinfo.io/json"

// IPInfo is the answer to "is it working": the address the world sees.
type IPInfo struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	City    string `json:"city"`
	// ThroughTunnel says whether the lookup itself went through the tunnel, because an
	// address seen directly says nothing about the tunnel.
	ThroughTunnel bool `json:"throughTunnel"`
}

// CheckIP reports the public address. It goes through the tunnel when one is up and
// directly otherwise, mirroring Diagnose.
func (a *App) CheckIP(ctx context.Context) (IPInfo, error) {
	snapshot := a.states.Snapshot()
	throughTunnel := snapshot.Status == state.StatusConnected && snapshot.SocksPort > 0

	transport := &http.Transport{
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if throughTunnel {
		dialer, err := proxy.SOCKS5("tcp",
			net.JoinHostPort("127.0.0.1", fmt.Sprint(snapshot.SocksPort)), nil, proxy.Direct)
		if err != nil {
			return IPInfo{}, fmt.Errorf("SOCKS-клиент не создан: %w", err)
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return IPInfo{}, fmt.Errorf("SOCKS-клиент не поддерживает контекст")
		}
		transport.DialContext = contextDialer.DialContext
	}

	lookupCtx, cancel := context.WithTimeout(ctx, ipInfoTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(lookupCtx, http.MethodGet, ipInfoURL, nil)
	if err != nil {
		return IPInfo{}, fmt.Errorf("запрос не создан: %w", err)
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return IPInfo{}, fmt.Errorf("адрес не определён: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return IPInfo{}, fmt.Errorf("служба ответила HTTP %d", response.StatusCode)
	}
	var payload struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
		City    string `json:"city"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return IPInfo{}, fmt.Errorf("ответ не разобран: %w", err)
	}
	if payload.IP == "" {
		return IPInfo{}, fmt.Errorf("служба не вернула адрес")
	}
	return IPInfo{
		IP:            payload.IP,
		Country:       payload.Country,
		City:          payload.City,
		ThroughTunnel: throughTunnel,
	}, nil
}
