package state

import (
	"sync"
	"testing"
	"time"
)

func TestPresentDisconnectedOffersConnect(t *testing.T) {
	presentation := Present(Snapshot{Status: StatusDisconnected})
	if presentation.PrimaryAction != ActionConnect {
		t.Fatalf("действие = %s", presentation.PrimaryAction)
	}
	if presentation.Title != "Отключено" {
		t.Fatalf("заголовок = %q", presentation.Title)
	}
}

func TestPresentConnectedShowsProfileAndTrouble(t *testing.T) {
	plain := Present(Snapshot{
		Status:      StatusConnected,
		ProfileName: "NL-01",
		Message:     "Пинг: 42 мс",
	})
	if plain.Description != "NL-01 · Пинг: 42 мс" {
		t.Fatalf("описание = %q", plain.Description)
	}

	// A failed component must win over the cosmetic latency message.
	degraded := Present(Snapshot{
		Status:      StatusConnected,
		ProfileName: "NL-01",
		Message:     "Пинг: 42 мс",
		Components: []ComponentSnapshot{
			{Component: ComponentTun2Socks, Status: ComponentFailed},
		},
	})
	if degraded.Description != "NL-01 · Перенос трафика в туннель остановился, восстанавливаем" {
		t.Fatalf("описание при сбое = %q", degraded.Description)
	}
}

func TestPresentAuthorizationFailureSendsToSubscription(t *testing.T) {
	presentation := Present(Snapshot{
		Status:      StatusError,
		FailureKind: FailureAuthorization,
	})
	if presentation.PrimaryAction != ActionOpenSubscription {
		t.Fatalf("действие = %s", presentation.PrimaryAction)
	}
	if len(presentation.SecondaryActions) != 0 {
		t.Fatalf("лишние действия: %v", presentation.SecondaryActions)
	}
}

func TestPresentNetworkFailureOffersNetworkSettings(t *testing.T) {
	presentation := Present(Snapshot{Status: StatusError, FailureKind: FailureNetwork})
	if presentation.PrimaryAction != ActionRetry {
		t.Fatalf("действие = %s", presentation.PrimaryAction)
	}
	if presentation.SecondaryActions[0] != ActionNetworkSettings {
		t.Fatalf("вторичные действия: %v", presentation.SecondaryActions)
	}
}

func TestComponentTroublePriority(t *testing.T) {
	trouble := ComponentTrouble([]ComponentSnapshot{
		{Component: ComponentXray, Status: ComponentFailed},
		{Component: ComponentTun, Status: ComponentFailed},
	})
	// The tunnel is the outermost failure: reporting it first matches what the user sees.
	if trouble != "Туннель закрылся, перезапускаем" {
		t.Fatalf("сообщение = %q", trouble)
	}
	if ComponentTrouble([]ComponentSnapshot{
		{Component: ComponentXray, Status: ComponentRunning},
	}) != "" {
		t.Fatal("исправный стек породил сообщение о сбое")
	}
}

func TestStorePublishesToSubscribers(t *testing.T) {
	store := NewStore()
	var mutex sync.Mutex
	seen := []RunStatus{}
	cancel := store.Subscribe(func(snapshot Snapshot) {
		mutex.Lock()
		defer mutex.Unlock()
		seen = append(seen, snapshot.Status)
	})

	store.Update(func(snapshot *Snapshot) { snapshot.Status = StatusConnecting })
	store.Publish(Snapshot{Status: StatusConnected, SocksPort: 10808})
	cancel()
	store.Publish(Snapshot{Status: StatusDisconnected})

	mutex.Lock()
	defer mutex.Unlock()
	want := []RunStatus{StatusDisconnected, StatusConnecting, StatusConnected}
	if len(seen) != len(want) {
		t.Fatalf("получено %v, ожидалось %v", seen, want)
	}
	for index, status := range want {
		if seen[index] != status {
			t.Fatalf("получено %v, ожидалось %v", seen, want)
		}
	}
	if store.Snapshot().Status != StatusDisconnected {
		t.Fatalf("состояние после отписки = %s", store.Snapshot().Status)
	}
}

func TestUptimeOnlyCountsWhileConnected(t *testing.T) {
	now := time.UnixMilli(1_700_000_060_000)
	connected := Snapshot{Status: StatusConnected, ConnectedAt: 1_700_000_000_000}
	if got := Uptime(connected, now); got != time.Minute {
		t.Fatalf("аптайм = %s", got)
	}
	if got := Uptime(Snapshot{Status: StatusConnecting, ConnectedAt: 1}, now); got != 0 {
		t.Fatalf("аптайм вне подключения = %s", got)
	}
	// A clock jump backwards must not produce a negative uptime in the UI.
	future := Snapshot{Status: StatusConnected, ConnectedAt: now.UnixMilli() + 5_000}
	if got := Uptime(future, now); got != 0 {
		t.Fatalf("аптайм при сдвиге часов = %s", got)
	}
}
