package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// binaryNames are the files a self-update may replace. The helper is deliberately absent:
// it runs as root and is owned by the installer, so replacing it from an unprivileged
// process is neither possible nor desirable.
var binaryNames = []string{"sa05", "sa05ctl"}

// maxEntryBytes bounds one unpacked file, so a malicious archive cannot fill the disk.
const maxEntryBytes = 200 << 20

// extract unpacks an archive into destination, flattening the release's top-level
// directory and keeping only the files the updater installs.
func extract(archivePath, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("каталог распаковки не создан: %w", err)
	}
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZip(archivePath, destination)
	}
	return extractTarGz(archivePath, destination)
}

func extractTarGz(archivePath, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("архив не открыт: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("архив не распакован: %w", err)
	}
	defer gzipReader.Close()

	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("архив повреждён: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name := wantedName(header.Name)
		if name == "" {
			continue
		}
		if err := writeEntry(filepath.Join(destination, name), reader, header.FileInfo().Mode()); err != nil {
			return err
		}
	}
}

func extractZip(archivePath, destination string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("архив не открыт: %w", err)
	}
	defer archive.Close()

	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := wantedName(entry.Name)
		if name == "" {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return fmt.Errorf("архив повреждён: %w", err)
		}
		err = writeEntry(filepath.Join(destination, name), reader, entry.Mode())
		reader.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// wantedName returns the flat file name to extract, or "" for entries the updater ignores.
//
// Only the known binary names are accepted, by base name. That also disposes of path
// traversal: nothing with a directory component is ever written.
func wantedName(entryName string) string {
	base := filepath.Base(filepath.ToSlash(entryName))
	for _, name := range binaryNames {
		if base == executableName(name) {
			return base
		}
	}
	return ""
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func writeEntry(path string, source io.Reader, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o111)
	if err != nil {
		return fmt.Errorf("файл %s не создан: %w", path, err)
	}
	defer file.Close()

	written, err := io.Copy(file, io.LimitReader(source, maxEntryBytes+1))
	if err != nil {
		return fmt.Errorf("файл %s не записан: %w", path, err)
	}
	if written > maxEntryBytes {
		return fmt.Errorf("файл %s превышает допустимый размер", path)
	}
	return nil
}

// replaceBinaries moves the unpacked binaries over the installed ones.
//
// The running executable is replaced by rename, which on Unix swaps the directory entry
// while the process keeps running from the old inode — the update takes effect on the
// next start, and a half-written binary is never visible.
func replaceBinaries(unpacked, target string) error {
	installed := 0
	for _, name := range binaryNames {
		file := executableName(name)
		source := filepath.Join(unpacked, file)
		if _, err := os.Stat(source); err != nil {
			continue
		}
		destination := filepath.Join(target, file)
		if err := replaceFile(source, destination); err != nil {
			return err
		}
		installed++
	}
	if installed == 0 {
		return fmt.Errorf("в архиве обновления нет исполняемых файлов клиента")
	}
	return nil
}

func replaceFile(source, destination string) error {
	if runtime.GOOS == "windows" {
		// Windows refuses to replace a file that is currently executing; moving it aside
		// first is what makes the swap possible, and the leftover is cleaned up on the
		// next update.
		previous := destination + ".old"
		_ = os.Remove(previous)
		if _, err := os.Stat(destination); err == nil {
			if err := os.Rename(destination, previous); err != nil {
				return fmt.Errorf("старый файл %s не отодвинут: %w", destination, err)
			}
		}
	}
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("файл %s не заменён: %w", destination, err)
	}
	return os.Chmod(destination, 0o755)
}
