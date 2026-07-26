// Package assets provisions the Xray geo databases.
//
// Provider profiles routinely use geoip:/geosite: rules, and Xray refuses to start a
// config that references a missing database. The files are large (~30 MB together), so
// they are not embedded: they are installed next to the binary or by the package, and
// copied into the per-user directory on first use.
package assets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Names are the databases Xray looks for in XRAY_LOCATION_ASSET.
var Names = []string{"geoip.dat", "geosite.dat"}

// minimumSize rejects a truncated download or an empty placeholder file.
const minimumSize = 1 << 20

// ErrMissing reports that no source of the databases could be found.
type ErrMissing struct {
	Missing  []string
	Searched []string
}

func (e *ErrMissing) Error() string {
	return fmt.Sprintf(
		"не найдены гео-базы %s. Положите их в один из каталогов: %s "+
			"или укажите SA05_ASSET_DIR",
		strings.Join(e.Missing, ", "), strings.Join(e.Searched, ", "))
}

// Ensure makes sure every database exists in target, copying it from the first source
// directory that has it. Existing files are never overwritten.
func Ensure(target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("каталог гео-баз не создан: %w", err)
	}
	sources := SourceDirs()
	missing := []string{}

	for _, name := range Names {
		if present(filepath.Join(target, name)) {
			continue
		}
		copied := false
		for _, source := range sources {
			candidate := filepath.Join(source, name)
			if !present(candidate) {
				continue
			}
			if err := copyFile(candidate, filepath.Join(target, name)); err != nil {
				return err
			}
			copied = true
			break
		}
		if !copied {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return &ErrMissing{Missing: missing, Searched: sources}
	}
	return nil
}

// SourceDirs lists where installed databases are looked for, most specific first.
func SourceDirs() []string {
	directories := []string{}
	if override := os.Getenv("SA05_ASSET_SOURCE"); override != "" {
		directories = append(directories, override)
	}
	if executable, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		binDir := filepath.Dir(executable)
		directories = append(directories, binDir, filepath.Join(binDir, "assets"))
	}
	return append(directories,
		"/usr/share/sa05",
		"/usr/local/share/sa05",
		"/opt/sa05",
	)
}

// Present reports whether target already holds every database.
func Present(target string) bool {
	for _, name := range Names {
		if !present(filepath.Join(target, name)) {
			return false
		}
	}
	return true
}

func present(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Size() >= minimumSize
}

// copyFile writes through a temporary file and renames, so an interrupted copy never
// leaves a half-written database that would then look "present".
func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("гео-база %s не прочитана: %w", source, err)
	}
	defer input.Close()

	temporary, err := os.CreateTemp(filepath.Dir(target), ".geo-*")
	if err != nil {
		return fmt.Errorf("временный файл не создан: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := io.Copy(temporary, input); err != nil {
		temporary.Close()
		return fmt.Errorf("гео-база не скопирована: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("временный файл не закрыт: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		return fmt.Errorf("права на гео-базу не выставлены: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("гео-база не установлена: %w", err)
	}
	return nil
}

// IsMissing reports whether err is an ErrMissing, so callers can show the install hint.
func IsMissing(err error) bool {
	missing := &ErrMissing{}
	return errors.As(err, &missing)
}
