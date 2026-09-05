package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fife/sa05-desktop/internal/core/subscription"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "sa05", "state.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	state, err := newStore(t).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state.Telegram.Transport != "auto" {
		t.Fatalf("транспорт по умолчанию = %q", state.Telegram.Transport)
	}
	if !state.Toggles.KillSwitch || !state.Toggles.AutoUpdate {
		t.Fatalf("значения по умолчанию: %+v", state.Toggles)
	}
	// Off by default: an IPv6 bypass reopens the leak the blackhole exists to close.
	if state.Toggles.AllowIPv6Bypass {
		t.Fatal("обход IPv6 включён по умолчанию")
	}
}

func TestSaveRoundTripsAndRestrictsPermissions(t *testing.T) {
	store := newStore(t)
	saved := Defaults()
	saved.Subscription = subscription.State{
		URL:             "https://example.com/sub?token=secret",
		ActiveProfileID: "abc",
		Profiles:        []subscription.Profile{{ID: "abc", Remarks: "NL", JSON: "{}"}},
	}
	saved.Telegram.Secret = "0123456789abcdef0123456789abcdef"
	saved.Toggles.Tun = true

	if err := store.Save(saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Subscription.URL != saved.Subscription.URL ||
		len(loaded.Subscription.Profiles) != 1 ||
		loaded.Telegram.Secret != saved.Telegram.Secret ||
		!loaded.Toggles.Tun {
		t.Fatalf("состояние не совпало: %+v", loaded)
	}

	// The file holds subscription tokens, so it must not be world-readable. Windows has
	// no POSIX mode — Chmod there only flips the read-only bit — and the file lives inside
	// the per-user profile, whose ACL already excludes other accounts.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.Path())
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if mode := info.Mode().Perm(); mode != fileMode {
			t.Fatalf("права файла = %o, ожидалось %o", mode, fileMode)
		}
	}
}

func TestUpdateAppliesMutation(t *testing.T) {
	store := newStore(t)
	state, err := store.Update(func(target *State) {
		target.Toggles.SystemProxy = true
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !state.Toggles.SystemProxy {
		t.Fatal("мутация не применена")
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reloaded.Toggles.SystemProxy {
		t.Fatal("мутация не сохранена")
	}
}

func TestLoadRejectsCorruptedFile(t *testing.T) {
	store := newStore(t)
	if err := os.WriteFile(store.Path(), []byte("{не json"), fileMode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("повреждённый файл принят")
	}
}

func TestSaveLeavesNoTemporaryFiles(t *testing.T) {
	store := newStore(t)
	if err := store.Save(Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(store.Path()))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("в каталоге %d файлов, ожидался один", len(entries))
	}
}

func TestAddUsageRollsOverDayAndMonth(t *testing.T) {
	morning := time.Date(2026, 9, 4, 10, 0, 0, 0, time.Local)
	usage := AddUsage(Usage{}, morning, 100, 200)
	if usage.DayUp != 100 || usage.DayDown != 200 || usage.MonthUp != 100 || usage.MonthDown != 200 {
		t.Fatalf("накопление: %+v", usage)
	}
	// The same day accumulates.
	usage = AddUsage(usage, morning.Add(time.Hour), 50, 50)
	if usage.DayUp != 150 || usage.MonthUp != 150 {
		t.Fatalf("добавление: %+v", usage)
	}
	// The next day resets the day but keeps the month.
	usage = AddUsage(usage, morning.Add(24*time.Hour), 10, 20)
	if usage.DayUp != 10 || usage.DayDown != 20 || usage.MonthUp != 160 || usage.MonthDown != 270 {
		t.Fatalf("смена дня: %+v", usage)
	}
	// The next month resets both.
	usage = AddUsage(usage, morning.AddDate(0, 1, 0), 7, 8)
	if usage.DayUp != 7 || usage.MonthUp != 7 || usage.Day != "2026-10-04" || usage.Month != "2026-10" {
		t.Fatalf("смена месяца: %+v", usage)
	}
}
