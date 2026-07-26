// Package state holds the single source of truth about what the client is running.
//
// The GUI, the tray icon and the CLI all read the same snapshot, and every transition
// goes through Store so no component invents its own status text.
//
// Ported from the Android client's VpnRuntimeState.kt / VpnStatusPresentation.kt with
// the desktop component set (no ByeDPI, plus the system-proxy toggle).
package state

import (
	"sync"
	"time"
)

// RunStatus is the lifecycle of the core.
type RunStatus string

const (
	StatusDisconnected      RunStatus = "DISCONNECTED"
	StatusConnecting        RunStatus = "CONNECTING"
	StatusConnected         RunStatus = "CONNECTED"
	StatusRecovering        RunStatus = "RECOVERING"
	StatusWaitingForNetwork RunStatus = "WAITING_FOR_NETWORK"
	StatusError             RunStatus = "ERROR"
)

// FailureKind tells the UI which recovery action makes sense.
type FailureKind string

const (
	FailureNone          FailureKind = "NONE"
	FailureAuthorization FailureKind = "AUTHORIZATION"
	FailureNetwork       FailureKind = "NETWORK"
	FailureBackend       FailureKind = "BACKEND"
	FailureTunnel        FailureKind = "TUNNEL"
	FailureHealthCheck   FailureKind = "HEALTH_CHECK"
	FailureService       FailureKind = "SERVICE"
)

// Component is one moving part of the stack.
type Component string

const (
	ComponentXray      Component = "XRAY"
	ComponentTun       Component = "TUN"
	ComponentTun2Socks Component = "TUN2SOCKS"
	ComponentTelegram  Component = "TELEGRAM"
	ComponentSysProxy  Component = "SYSPROXY"
)

// ComponentStatus is the health of one component.
type ComponentStatus string

const (
	ComponentStarting ComponentStatus = "STARTING"
	ComponentRunning  ComponentStatus = "RUNNING"
	ComponentStopped  ComponentStatus = "STOPPED"
	ComponentFailed   ComponentStatus = "FAILED"
)

// ComponentSnapshot pairs a component with its health.
type ComponentSnapshot struct {
	Component Component       `json:"component"`
	Status    ComponentStatus `json:"status"`
}

// Snapshot is the full runtime state handed to the UI.
type Snapshot struct {
	Status          RunStatus   `json:"status"`
	ProfileID       string      `json:"profileId"`
	ProfileName     string      `json:"profileName"`
	Message         string      `json:"message"`
	FailureKind     FailureKind `json:"failureKind"`
	SocksPort       int         `json:"socksPort"`
	HTTPPort        int         `json:"httpPort"`
	NetworkKey      string      `json:"networkKey"`
	ConnectedAt     int64       `json:"connectedAt"`
	RecoveryAttempt int         `json:"recoveryAttempt"`
	SystemProxyOn   bool        `json:"systemProxyOn"`
	TunOn           bool        `json:"tunOn"`
	TelegramOn      bool        `json:"telegramOn"`
	LatencyMS       int         `json:"latencyMs"`
	// Traffic totals since the current connection started, and the last measured rate.
	TrafficUp   int64               `json:"trafficUp"`
	TrafficDown int64               `json:"trafficDown"`
	RateUp      int64               `json:"rateUp"`
	RateDown    int64               `json:"rateDown"`
	Components  []ComponentSnapshot `json:"components"`
}

// Requested reports whether the user currently wants the tunnel up.
func (s Snapshot) Requested() bool { return s.Status != StatusDisconnected }

// PrimaryAction is the main button's meaning.
type PrimaryAction string

const (
	ActionConnect          PrimaryAction = "CONNECT"
	ActionStop             PrimaryAction = "STOP"
	ActionRetry            PrimaryAction = "RETRY"
	ActionOpenSubscription PrimaryAction = "OPEN_SUBSCRIPTION"
)

// SecondaryAction is an optional follow-up offered next to the main button.
type SecondaryAction string

const (
	ActionNetworkSettings SecondaryAction = "NETWORK_SETTINGS"
	ActionChangeProfile   SecondaryAction = "CHANGE_PROFILE"
	ActionDiagnostics     SecondaryAction = "DIAGNOSTICS"
)

// Presentation is the rendered form of a snapshot: what the user reads and can press.
type Presentation struct {
	Title            string            `json:"title"`
	Description      string            `json:"description"`
	PrimaryAction    PrimaryAction     `json:"primaryAction"`
	SecondaryActions []SecondaryAction `json:"secondaryActions"`
}

// Present turns a snapshot into user-facing text and actions.
func Present(snapshot Snapshot) Presentation {
	profile := snapshot.ProfileName
	if profile == "" {
		profile = "Сервер не выбран"
	}
	switch snapshot.Status {
	case StatusConnecting:
		return Presentation{
			Title:         "Подключаем…",
			Description:   fallback(snapshot.Message, profile),
			PrimaryAction: ActionStop,
		}
	case StatusConnected:
		description := profile
		if trouble := ComponentTrouble(snapshot.Components); trouble != "" {
			description = join(profile, trouble)
		} else if snapshot.Message != "" {
			description = join(profile, snapshot.Message)
		}
		return Presentation{
			Title:         "Подключено",
			Description:   description,
			PrimaryAction: ActionStop,
		}
	case StatusRecovering:
		return Presentation{
			Title:         "Восстанавливаем",
			Description:   fallback(snapshot.Message, "Проверяем маршрут после смены сети"),
			PrimaryAction: ActionStop,
		}
	case StatusWaitingForNetwork:
		return Presentation{
			Title:            "Нет сети",
			Description:      fallback(snapshot.Message, "Подключение продолжится, когда сеть появится"),
			PrimaryAction:    ActionStop,
			SecondaryActions: []SecondaryAction{ActionNetworkSettings},
		}
	case StatusError:
		if snapshot.FailureKind == FailureAuthorization {
			return Presentation{
				Title:         "Нужна действующая подписка",
				Description:   fallback(snapshot.Message, "Добавьте ссылку подписки"),
				PrimaryAction: ActionOpenSubscription,
			}
		}
		secondary := []SecondaryAction{ActionChangeProfile, ActionDiagnostics}
		if snapshot.FailureKind == FailureNetwork {
			secondary = []SecondaryAction{ActionNetworkSettings, ActionDiagnostics}
		}
		return Presentation{
			Title:            "Не удалось подключиться",
			Description:      fallback(snapshot.Message, "Повторите попытку или откройте диагностику"),
			PrimaryAction:    ActionRetry,
			SecondaryActions: secondary,
		}
	default:
		return Presentation{
			Title:         "Отключено",
			Description:   "Выберите сервер и подключитесь",
			PrimaryAction: ActionConnect,
		}
	}
}

// ComponentTrouble is a plain-language note about a broken component, or "" while the
// stack is healthy — healthy components are not worth screen space.
func ComponentTrouble(components []ComponentSnapshot) string {
	failed := map[Component]bool{}
	for _, item := range components {
		if item.Status == ComponentFailed {
			failed[item.Component] = true
		}
	}
	switch {
	case len(failed) == 0:
		return ""
	case failed[ComponentTun]:
		return "Туннель закрылся, перезапускаем"
	case failed[ComponentTun2Socks]:
		return "Перенос трафика в туннель остановился, восстанавливаем"
	case failed[ComponentXray]:
		return "Соединение с сервером оборвалось, восстанавливаем"
	case failed[ComponentTelegram]:
		return "Telegram Proxy остановился"
	case failed[ComponentSysProxy]:
		return "Системный прокси не применён"
	default:
		return "Один из компонентов остановился"
	}
}

// Store publishes snapshots to every listener under one lock, so the UI, the tray and
// the CLI can never disagree about the current state.
type Store struct {
	mutex       sync.RWMutex
	current     Snapshot
	nextID      int
	subscribers map[int]func(Snapshot)
}

// NewStore returns a disconnected store.
func NewStore() *Store {
	return &Store{
		current:     Snapshot{Status: StatusDisconnected},
		subscribers: map[int]func(Snapshot){},
	}
}

// Snapshot returns the current state.
func (s *Store) Snapshot() Snapshot {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.current
}

// Publish replaces the state and notifies listeners. Listeners run outside the lock so
// a slow subscriber cannot deadlock a state transition.
func (s *Store) Publish(snapshot Snapshot) {
	s.mutex.Lock()
	s.current = snapshot
	listeners := make([]func(Snapshot), 0, len(s.subscribers))
	for _, listener := range s.subscribers {
		listeners = append(listeners, listener)
	}
	s.mutex.Unlock()
	for _, listener := range listeners {
		listener(snapshot)
	}
}

// Update applies mutate to a copy of the current state and publishes the result.
func (s *Store) Update(mutate func(*Snapshot)) Snapshot {
	s.mutex.Lock()
	next := s.current
	mutate(&next)
	s.current = next
	listeners := make([]func(Snapshot), 0, len(s.subscribers))
	for _, listener := range s.subscribers {
		listeners = append(listeners, listener)
	}
	s.mutex.Unlock()
	for _, listener := range listeners {
		listener(next)
	}
	return next
}

// Subscribe registers a listener and returns a cancel function.
func (s *Store) Subscribe(listener func(Snapshot)) (cancel func()) {
	s.mutex.Lock()
	id := s.nextID
	s.nextID++
	s.subscribers[id] = listener
	current := s.current
	s.mutex.Unlock()
	listener(current)
	return func() {
		s.mutex.Lock()
		delete(s.subscribers, id)
		s.mutex.Unlock()
	}
}

// Uptime is how long the current connection has been up, or 0 when disconnected.
func Uptime(snapshot Snapshot, now time.Time) time.Duration {
	if snapshot.Status != StatusConnected || snapshot.ConnectedAt == 0 {
		return 0
	}
	elapsed := now.Sub(time.UnixMilli(snapshot.ConnectedAt))
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func fallback(value, alternative string) string {
	if value == "" {
		return alternative
	}
	return value
}

func join(first, second string) string {
	switch {
	case first == "":
		return second
	case second == "", first == second:
		return first
	default:
		return first + " · " + second
	}
}
