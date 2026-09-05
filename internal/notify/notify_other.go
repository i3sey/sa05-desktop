//go:build !linux

package notify

// send is a no-op where no notification backend is wired yet. Windows toasts need an
// AppUserModelID registered by the installer, which the Windows packaging does not do
// yet; until then the tray tooltip carries the state.
func send(kind Kind, title, body string, replace uint32) uint32 { return 0 }
