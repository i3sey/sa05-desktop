//go:build windows

package tun

import (
	"net"
	"testing"
)

// Split defaults must cover the whole unicast space in two halves without
// touching loopback, LANs (more specific originals win) or multicast.
func TestPlannedSplitV4CoversHalves(t *testing.T) {
	routes := plannedSplitV4(7)
	if len(routes) != 2 {
		t.Fatalf("маршрутов %d, ожидалось 2", len(routes))
	}
	seen := map[string]bool{}
	for _, route := range routes {
		if route.mask != "128.0.0.0" {
			t.Fatalf("маска %s не делит пространство пополам", route.mask)
		}
		if route.gateway != tunPeerV4 {
			t.Fatalf("шлюз %s не указывает в туннель", route.gateway)
		}
		if route.metric != lowMetric {
			t.Fatalf("метрика %d", route.metric)
		}
		if route.ifIndex != 7 {
			t.Fatalf("интерфейс %d", route.ifIndex)
		}
		seen[route.dst] = true
	}
	if !seen["0.0.0.0"] || !seen["128.0.0.0"] {
		t.Fatalf("половины не покрыты: %+v", routes)
	}
}

func TestPlannedSplitV6HalvesDefault(t *testing.T) {
	halves := plannedSplitV6()
	if len(halves) != 2 || halves[0] != "::/1" || halves[1] != "8000::/1" {
		t.Fatalf("половины IPv6: %q", halves)
	}
	for _, half := range halves {
		if _, _, err := net.ParseCIDR(half); err != nil {
			t.Fatalf("%s не парсится: %v", half, err)
		}
	}
}

func TestKillSwitchV4IsPersistentAndSinkholed(t *testing.T) {
	routes := killSwitchV4()
	if len(routes) != 2 {
		t.Fatalf("маршрутов %d, ожидалось 2", len(routes))
	}
	for _, route := range routes {
		if !route.persist {
			t.Fatal("kill-switch обязан пережить перезагрузку (-p)")
		}
		if route.gateway != nullGatewayV4 {
			t.Fatalf("шлюз %s не ведёт в чёрную дыру", route.gateway)
		}
		if route.metric != killMetric {
			t.Fatalf("метрика %d", route.metric)
		}
	}
}

func TestParseNetRouteObjectAndArray(t *testing.T) {
	single := []byte(`{"NextHop":"192.168.1.1","InterfaceIndex":12}`)
	route, err := parseNetRoute(single)
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if route.NextHop != "192.168.1.1" || route.InterfaceIndex != 12 {
		t.Fatalf("маршрут: %+v", route)
	}

	array := []byte(`[{"NextHop":"10.0.0.1","InterfaceIndex":3},{"NextHop":"10.0.0.2","InterfaceIndex":4}]`)
	route, err = parseNetRoute(array)
	if err != nil {
		t.Fatalf("разбор массива: %v", err)
	}
	if route.NextHop != "10.0.0.1" {
		t.Fatalf("взят не первый маршрут: %+v", route)
	}

	for _, bad := range []string{"", "{}", "[]", "not json"} {
		if _, err := parseNetRoute([]byte(bad)); err == nil {
			t.Fatalf("мусор принят: %q", bad)
		}
	}
}

func TestDeleteArgsV4Shape(t *testing.T) {
	args := deleteArgsV4("1.2.3.4", "255.255.255.255")
	want := []string{"delete", "1.2.3.4", "mask", "255.255.255.255"}
	if len(args) != len(want) {
		t.Fatalf("аргументы: %q", args)
	}
	for index := range want {
		if args[index] != want[index] {
			t.Fatalf("аргументы: %q", args)
		}
	}
}
