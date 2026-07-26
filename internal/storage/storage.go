// Package storage persists client state on disk.
//
// One file holds everything: the cached subscription (which may contain access tokens),
// the Telegram secret and the toggles. It is written atomically with 0600 permissions so
// a crash mid-write cannot leave a truncated cache and other users cannot read the tokens.
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/fife/sa05-desktop/internal/core/subscription"
	"github.com/fife/sa05-desktop/internal/sysproxy"
)

const (
	fileName    = "state.json"
	fileMode    = 0o600
	dirMode     = 0o700
	appDirName  = "sa05"
	schemaValue = 1
)

// TelegramSettings configures the built-in MTProto proxy.
type TelegramSettings struct {
	// Secret is the 32-hex-char MTProto secret; it stays stable across transport changes
	// so Telegram is configured once.
	Secret string `json:"secret"`
	// Transport selects how tgws reaches the datacenters: auto, cf, ws or tcp.
	Transport string `json:"transport"`
	// CloudflareDomain overrides the built-in Cloudflare front-end list.
	CloudflareDomain string `json:"cloudflareDomain"`
	// Applied records that the user already pasted the tg:// link into Telegram.
	Applied bool `json:"applied"`
}

// Toggles are the three switches on the main screen plus the tunnel policy flags.
type Toggles struct {
	SystemProxy bool `json:"systemProxy"`
	Tun         bool `json:"tun"`
	Telegram    bool `json:"telegram"`
	// AllowIPv6Bypass hands IPv6 back to the system instead of blackholing it in the
	// tunnel. Off by default: it reopens the leak IPv6 blackholing exists to close.
	AllowIPv6Bypass bool `json:"allowIpv6Bypass"`
	// KillSwitch keeps traffic blocked when the core dies while TUN is up.
	KillSwitch  bool `json:"killSwitch"`
	AutoConnect bool `json:"autoConnect"`
	Autostart   bool `json:"autostart"`
	AutoUpdate  bool `json:"autoUpdate"`
}

// State is the whole persisted document.
type State struct {
	Schema       int                `json:"schema"`
	Subscription subscription.State `json:"subscription"`
	Telegram     TelegramSettings   `json:"telegram"`
	Toggles      Toggles            `json:"toggles"`
	// SysProxy is the desktop's proxy configuration from before SA05 changed it. It is
	// persisted so a crash or a forced quit can still restore the user's own settings on
	// the next start.
	SysProxy *sysproxy.Snapshot `json:"sysProxy,omitempty"`
}

// Defaults returns the state a fresh install starts from.
func Defaults() State {
	return State{
		Schema: schemaValue,
		Telegram: TelegramSettings{
			Transport: "auto",
		},
		Toggles: Toggles{
			KillSwitch: true,
			AutoUpdate: true,
		},
	}
}

// Store reads and writes the state file.
type Store struct {
	path  string
	mutex sync.Mutex
}

// Open returns a store for the given file, creating its directory. An empty path
// resolves to the per-user config directory.
func Open(path string) (*Store, error) {
	if path == "" {
		resolved, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = resolved
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, fmt.Errorf("каталог настроек не создан: %w", err)
	}
	return &Store{path: path}, nil
}

// Path is the file this store reads and writes.
func (s *Store) Path() string { return s.path }

// Load reads the state, returning defaults when the file does not exist yet.
func (s *Store) Load() (State, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("настройки не прочитаны: %w", err)
	}
	state := Defaults()
	if err := json.Unmarshal(raw, &state); err != nil {
		return State{}, fmt.Errorf("настройки повреждены: %w", err)
	}
	return state, nil
}

// Save writes the state atomically: a temporary file in the same directory is renamed
// over the target, so a crash never leaves a half-written subscription cache.
func (s *Store) Save(state State) error {
	state.Schema = schemaValue
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("настройки не сериализованы: %w", err)
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()

	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, dirMode); err != nil {
		return fmt.Errorf("каталог настроек не создан: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".state-*.json")
	if err != nil {
		return fmt.Errorf("временный файл не создан: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(fileMode); err != nil {
		temporary.Close()
		return fmt.Errorf("права на файл настроек не выставлены: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return fmt.Errorf("настройки не записаны: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("настройки не сброшены на диск: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("временный файл не закрыт: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("настройки не сохранены: %w", err)
	}
	return nil
}

// Update loads the state, applies mutate and saves the result.
func (s *Store) Update(mutate func(*State)) (State, error) {
	state, err := s.Load()
	if err != nil {
		return State{}, err
	}
	mutate(&state)
	if err := s.Save(state); err != nil {
		return State{}, err
	}
	return state, nil
}

// DefaultPath is the per-user state file: $XDG_CONFIG_HOME/sa05/state.json on Linux,
// %AppData%\sa05\state.json on Windows.
func DefaultPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("каталог настроек не определён: %w", err)
	}
	return filepath.Join(directory, appDirName, fileName), nil
}

// DefaultCacheDir is where runtime caches live (Cloudflare domain lists, probe results).
func DefaultCacheDir() (string, error) {
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("каталог кэша не определён: %w", err)
	}
	path := filepath.Join(directory, appDirName)
	if err := os.MkdirAll(path, dirMode); err != nil {
		return "", fmt.Errorf("каталог кэша не создан: %w", err)
	}
	return path, nil
}

// DefaultAssetDir is where geoip.dat / geosite.dat are looked up at runtime.
func DefaultAssetDir() (string, error) {
	if override := os.Getenv("SA05_ASSET_DIR"); override != "" {
		return override, nil
	}
	if runtime.GOOS == "windows" {
		directory, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("каталог гео-баз не определён: %w", err)
		}
		return filepath.Join(directory, appDirName, "assets"), nil
	}
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("каталог гео-баз не определён: %w", err)
	}
	return filepath.Join(directory, appDirName, "assets"), nil
}
