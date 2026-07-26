// Package notify shows desktop notifications about tunnel state.
//
// The client spends most of its life in the tray, so a drop or a recovery has to reach
// the user without the window being open. Notifications are strictly informational:
// nothing here can fail in a way that affects the tunnel, so every error is swallowed
// after being reported once.
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
	key := title + "\x00" + body

	n.mutex.Lock()
	if key == n.last && time.Since(n.lastTime) < repeatWindow {
		n.mutex.Unlock()
		return
	}
	n.last = key
	n.lastTime = time.Now()
	n.mutex.Unlock()

	// Sending is best-effort and must never block a state transition.
	go send(kind, title, body)
}
