//go:build linux

package notify

import (
	"time"

	"github.com/godbus/dbus/v5"
)

// dbusTimeout bounds the whole call: a hung notification daemon must not keep a goroutine
// alive forever.
const dbusTimeout = 3 * time.Second

// send delivers the notification over the freedesktop.org D-Bus interface, which every
// desktop environment implements. No fallback to notify-send: it does the same call, and
// a missing daemon means notifications are unavailable either way.
func send(kind Kind, title, body string) {
	connection, err := dbus.SessionBus()
	if err != nil {
		return
	}
	object := connection.Object(
		"org.freedesktop.Notifications", dbus.ObjectPath("/org/freedesktop/Notifications"))

	hints := map[string]dbus.Variant{
		"urgency": dbus.MakeVariant(urgency(kind)),
		// Replace the previous SA05 notification instead of stacking: state changes are a
		// running commentary, not a list of events worth keeping.
		"transient": dbus.MakeVariant(kind == KindInfo),
	}
	call := object.Call("org.freedesktop.Notifications.Notify", 0,
		appName,
		uint32(0),
		iconName,
		title,
		body,
		[]string{},
		hints,
		int32(expiry(kind)),
	)
	_ = call.Err
}

func urgency(kind Kind) byte {
	switch kind {
	case KindWarning:
		return 1
	case KindError:
		return 2
	default:
		return 0
	}
}

// expiry is the timeout in milliseconds; an error stays until dismissed.
func expiry(kind Kind) int {
	if kind == KindError {
		return 0
	}
	return 5000
}
