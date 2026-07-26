package recovery

import "testing"

func TestNetworkChanged(t *testing.T) {
	cases := []struct {
		previous string
		current  string
		want     Decision
	}{
		{"wifi-1", "", DecisionWaitForNetwork},
		{"", "", DecisionWaitForNetwork},
		{"", "wifi-1", DecisionNone},
		{"wifi-1", "wifi-1", DecisionNone},
		{"wifi-1", "lte-2", DecisionVerifyRoute},
	}
	for _, item := range cases {
		if got := NetworkChanged(item.previous, item.current); got != item.want {
			t.Fatalf("NetworkChanged(%q, %q) = %s, ожидалось %s",
				item.previous, item.current, got, item.want)
		}
	}
}

func TestRouteChecked(t *testing.T) {
	if got := RouteChecked(true, MaxAutomaticAttempts+5); got != DecisionNone {
		t.Fatalf("исправный маршрут = %s", got)
	}
	if got := RouteChecked(false, 0); got != DecisionReconnect {
		t.Fatalf("первая неудача = %s", got)
	}
	if got := RouteChecked(false, MaxAutomaticAttempts-1); got != DecisionReconnect {
		t.Fatalf("последняя разрешённая попытка = %s", got)
	}
	// Beyond the bound the user must see the failure instead of a silent restart loop.
	if got := RouteChecked(false, MaxAutomaticAttempts); got != DecisionFail {
		t.Fatalf("исчерпание попыток = %s", got)
	}
}
