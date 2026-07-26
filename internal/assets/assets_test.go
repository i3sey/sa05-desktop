package assets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func writeDatabase(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, size), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestEnsureCopiesFromSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	for _, name := range Names {
		writeDatabase(t, filepath.Join(source, name), minimumSize+16)
	}
	t.Setenv("SA05_ASSET_SOURCE", source)

	if err := Ensure(target); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !Present(target) {
		t.Fatal("гео-базы не установлены")
	}
	for _, name := range Names {
		info, err := os.Stat(filepath.Join(target, name))
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Size() != int64(minimumSize+16) {
			t.Fatalf("%s скопирован не полностью: %d байт", name, info.Size())
		}
	}
}

func TestEnsureKeepsExistingFiles(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	for _, name := range Names {
		writeDatabase(t, filepath.Join(source, name), minimumSize+16)
		writeDatabase(t, filepath.Join(target, name), minimumSize+99)
	}
	t.Setenv("SA05_ASSET_SOURCE", source)

	if err := Ensure(target); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// A user who updated the databases by hand must keep their copies.
	info, err := os.Stat(filepath.Join(target, Names[0]))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != int64(minimumSize+99) {
		t.Fatalf("существующий файл перезаписан: %d байт", info.Size())
	}
}

func TestEnsureReportsMissingDatabases(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SA05_ASSET_SOURCE", filepath.Join(root, "empty"))

	err := Ensure(filepath.Join(root, "target"))
	if err == nil {
		t.Fatal("отсутствие гео-баз не привело к ошибке")
	}
	if !IsMissing(err) {
		t.Fatalf("тип ошибки: %T", err)
	}
	// The message must tell the user where the files are expected.
	if len(err.Error()) < 40 {
		t.Fatalf("сообщение слишком короткое: %s", err)
	}
}

func TestTruncatedDatabaseCountsAsMissing(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	for _, name := range Names {
		// A half-downloaded file must not be mistaken for an installed database.
		writeDatabase(t, filepath.Join(source, name), 128)
	}
	t.Setenv("SA05_ASSET_SOURCE", source)

	if err := Ensure(target); !IsMissing(err) {
		t.Fatalf("обрезанный файл принят: %v", err)
	}
}
