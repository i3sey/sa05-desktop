package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fife/sa05-desktop/internal/storage"
)

// logBundleBytes caps the exported log tail: enough for the last session, small enough
// to paste into a chat.
const logBundleBytes = 200 * 1024

// LogBundle packs the log tail plus the current view into one text for "send to the
// developer". Secrets never land in state.json-adjacent logs, but the bundle still goes
// through the user's hands, not anywhere automatically.
func (a *App) LogBundle() (string, error) {
	var bundle strings.Builder
	fmt.Fprintf(&bundle, "SA05 %s\n\n", Version)

	view, err := a.View()
	if err != nil {
		fmt.Fprintf(&bundle, "состояние: %v\n\n", err)
	} else {
		encoded, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return "", fmt.Errorf("состояние не сериализовано: %w", err)
		}
		bundle.Write(encoded)
		bundle.WriteString("\n\n--- журнал ---\n")
	}

	path, err := storage.DefaultLogPath()
	if err != nil {
		bundle.WriteString("журнал недоступен\n")
		return bundle.String(), nil
	}
	tail, err := readLogTail(path, logBundleBytes)
	if err != nil {
		fmt.Fprintf(&bundle, "журнал не прочитан: %v\n", err)
		return bundle.String(), nil
	}
	bundle.WriteString(tail)
	return bundle.String(), nil
}

// readLogTail returns the last max bytes of the file, starting at a line boundary.
func readLogTail(path string, max int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	offset := info.Size() - max
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(io.LimitReader(file, max+1024))
	if err != nil {
		return "", err
	}
	text := string(raw)
	if offset > 0 {
		if index := strings.IndexByte(text, '\n'); index >= 0 {
			text = text[index+1:]
		}
	}
	return text, nil
}
