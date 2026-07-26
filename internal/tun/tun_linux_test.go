//go:build linux

package tun

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

// The routing policy is what keeps traffic from leaking around the tunnel, so it is
// asserted here even though applying it needs root.
func TestPlannedRulesCoverLANMarkAndFamilies(t *testing.T) {
	rules := (&Tunnel{}).plannedRules(Config{SocksPort: 10808})

	lan := 0
	bypass := map[int]*netlink.Rule{}
	for _, rule := range rules {
		switch rule.Priority {
		case PriorityLAN:
			lan++
			if rule.Table != mainTable {
				t.Fatalf("локальная сеть уходит в таблицу %d", rule.Table)
			}
		case PriorityDefault:
			bypass[rule.Family] = rule
		default:
			t.Fatalf("неожиданный приоритет %d", rule.Priority)
		}
	}
	if lan != len(LocalNetworks) {
		t.Fatalf("правил для локальных сетей %d, ожидалось %d", lan, len(LocalNetworks))
	}
	// LAN exceptions must be evaluated before the catch-all, otherwise the router and the
	// printer disappear the moment the tunnel comes up.
	if PriorityLAN >= PriorityDefault {
		t.Fatal("правила локальной сети должны идти раньше общего")
	}

	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rule, ok := bypass[family]
		if !ok {
			t.Fatalf("нет общего правила для семейства %d", family)
		}
		if rule.Table != TableID {
			t.Fatalf("общее правило указывает на таблицу %d", rule.Table)
		}
		if !rule.Invert {
			t.Fatal("правило не инвертировано: помеченный трафик утечёт в туннель")
		}
		if rule.Mark != DefaultMark {
			t.Fatalf("метка = %#x, ожидалась %#x", rule.Mark, DefaultMark)
		}
	}
}

func TestPlannedRulesSkipIPv6WhenHandedToSystem(t *testing.T) {
	rules := (&Tunnel{}).plannedRules(Config{SocksPort: 10808, AllowIPv6Bypass: true})
	for _, rule := range rules {
		if rule.Family == netlink.FAMILY_V6 {
			t.Fatal("IPv6 захвачен, хотя отдан системе")
		}
	}
}

func TestCustomMarkIsHonoured(t *testing.T) {
	rules := (&Tunnel{}).plannedRules(Config{SocksPort: 10808, Mark: 0x1234})
	found := false
	for _, rule := range rules {
		if rule.Priority == PriorityDefault {
			found = true
			if rule.Mark != 0x1234 {
				t.Fatalf("метка = %#x", rule.Mark)
			}
		}
	}
	if !found {
		t.Fatal("общее правило не построено")
	}
}

func TestLocalNetworksParse(t *testing.T) {
	for _, network := range LocalNetworks {
		if _, _, err := net.ParseCIDR(network); err != nil {
			t.Fatalf("%s: %v", network, err)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	config := Config{}
	if config.mark() != DefaultMark {
		t.Fatalf("метка по умолчанию = %#x", config.mark())
	}
	if config.dns() == "" {
		t.Fatal("резолвер по умолчанию пуст")
	}
	custom := Config{Mark: 7, DNS: "9.9.9.9"}
	if custom.mark() != 7 || custom.dns() != "9.9.9.9" {
		t.Fatalf("значения не переопределены: %+v", custom)
	}
}
