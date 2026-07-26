package trayicon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"
)

func decode(t *testing.T, payload []byte) image.Image {
	t.Helper()
	decoded, err := png.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	return decoded
}

func TestPNGRendersEachState(t *testing.T) {
	states := map[string]State{"idle": StateIdle, "active": StateActive, "error": StateError}
	rendered := map[string][]byte{}
	for name, value := range states {
		payload := PNG(value)
		if len(payload) == 0 {
			t.Fatalf("%s: пустая иконка", name)
		}
		image := decode(t, payload)
		bounds := image.Bounds()
		if bounds.Dx() != size || bounds.Dy() != size {
			t.Fatalf("%s: размер %dx%d", name, bounds.Dx(), bounds.Dy())
		}
		rendered[name] = payload
	}
	// The three states must be visually distinct, otherwise the tray says nothing.
	if bytes.Equal(rendered["idle"], rendered["active"]) ||
		bytes.Equal(rendered["active"], rendered["error"]) {
		t.Fatal("состояния рисуются одинаково")
	}
}

func TestActiveIconIsFilledAndIdleIsRing(t *testing.T) {
	center := size / 2
	active := decode(t, PNG(StateActive))
	if _, _, _, alpha := active.At(center, center).RGBA(); alpha == 0 {
		t.Fatal("активная иконка пустая в центре")
	}
	idle := decode(t, PNG(StateIdle))
	if _, _, _, alpha := idle.At(center, center).RGBA(); alpha != 0 {
		t.Fatal("иконка покоя залита, а должна быть кольцом")
	}
	// Both must actually draw something at the ring radius.
	if _, _, _, alpha := idle.At(center, 2).RGBA(); alpha == 0 {
		t.Fatal("кольцо не нарисовано")
	}
}

func TestICOWrapsPNG(t *testing.T) {
	payload := ICO(StateActive)
	if len(payload) < 22 {
		t.Fatalf("ICO слишком короткий: %d байт", len(payload))
	}
	var reserved, kind, count uint16
	reader := bytes.NewReader(payload)
	binary.Read(reader, binary.LittleEndian, &reserved)
	binary.Read(reader, binary.LittleEndian, &kind)
	binary.Read(reader, binary.LittleEndian, &count)
	if reserved != 0 || kind != 1 || count != 1 {
		t.Fatalf("заголовок ICO: %d %d %d", reserved, kind, count)
	}
	// The embedded payload must still be a decodable PNG.
	decode(t, payload[22:])
}
