// Package desktop integrates the client with the session: autostart and the sa05://
// URL scheme.
//
// Every operation is per-user and reversible — nothing here needs root, and disabling a
// feature removes exactly what enabling it created.
package desktop

// Integration installs and removes session integration for the current user.
type Integration interface {
	// SetAutostart enables or disables launching the client at login.
	SetAutostart(enabled bool) error
	// AutostartEnabled reports the current state.
	AutostartEnabled() (bool, error)
	// RegisterURLScheme makes sa05:// links open this client.
	RegisterURLScheme() error
}

// ExecutablePath is the binary session integration should point at. It is a variable so
// tests can pin it instead of depending on the test runner's own path.
var ExecutablePath = defaultExecutablePath
