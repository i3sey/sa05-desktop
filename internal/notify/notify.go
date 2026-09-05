package notify

import (
	"sync"
	"time"
)

// Kind selects the urgency and the icon of a notification.
type Kind int

const (
	// KindInfo is a normal state change: connected, disconnected.
	KindInfo Kind = iota
	// KindWarning is a recoverable problem: reconnecting after a network change.
	KindWarning
	// KindError is a failure the user has to act on.
	KindError
)

// appName is what the notification daemon shows as the sender.
const appName = "SA05"

// iconName matches the hicolor icon installed by the packages.
const iconName = "sa05"

// Notifier sends desktop notifications.
type Notifier struct {
	mutex sync.Mutex
	// last suppresses repeats: a flapping network would otherwise bury the desktop in
	// identical popups.
	last     string
	lastTime time.Time
	// sendMu serializes delivery so every popup replaces the previous one instead of
	// stacking next to it. Delivery runs in a goroutine; state transitions never wait.
	sendMu sync.Mutex
	// replaceID is the daemon's handle of our last popup, handed back as replaces_id.
	replaceID uint32
	// Enabled allows the UI to switch notifications off without unwiring the callers.
	Enabled bool
}

// New returns an enabled notifier.
func New() *Notifier { return &Notifier{Enabled: true} }

// repeatWindow is how long an identical message is suppressed.
const repeatWindow = 20 * time.Second

// Send shows a notification unless the same text was just shown.
func (n *Notifier) Send(kind Kind, title, body string) {
	if n == nil || !n.Enabled {
		return
	}
	if !n.admit(title+"\x00"+body, time.Now()) {
		return
	}
	// Sending is best-effort and must never block a state transition.
	go n.deliver(kind, title, body)
}

// admit reports whether the message passes the repeat filter, recording it when it
// does. Pure decision under the mutex, so the filter itself is unit-testable without a
// notification daemon.
func (n *Notifier) admit(key string, now time.Time) bool {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	if key == n.last && now.Sub(n.lastTime) < repeatWindow {
		return false
	}
	n.last = key
	n.lastTime = now
	return true
}

// deliver hands one popup to the backend, replacing the previous SA05 popup: state
// changes are a running commentary, not a list of events worth keeping.
func (n *Notifier) deliver(kind Kind, title, body string) {
	n.sendMu.Lock()
	defer n.sendMu.Unlock()
	if id := send(kind, title, body, n.replaceID); id != 0 {
		n.replaceID = id
	}
}
