// Package trayicon renders the tray indicator.
//
// The icons are drawn in code rather than shipped as files: there are only two states,
// they must match the SA05 palette exactly, and generating them keeps the binary
// self-contained with no asset paths to resolve at runtime.
package trayicon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

const size = 32

var (
	// ocean is the SA05 accent, used while the tunnel carries traffic.
	ocean = color.NRGBA{R: 0x00, G: 0x6A, B: 0x66, A: 0xFF}
	// idle is a muted ring: visible on both light and dark trays, clearly "off".
	idle = color.NRGBA{R: 0x8A, G: 0x8F, B: 0x88, A: 0xFF}
	// coral marks a failure that needs the user.
	coral = color.NRGBA{R: 0x9A, G: 0x45, B: 0x2D, A: 0xFF}
)

// State selects which icon to draw.
type State int

const (
	// StateIdle is the disconnected client.
	StateIdle State = iota
	// StateActive is a working tunnel.
	StateActive
	// StateError is a failure the user must look at.
	StateError
)

// PNG renders the icon for a state.
func PNG(state State) []byte {
	fill := idle
	filled := false
	switch state {
	case StateActive:
		fill, filled = ocean, true
	case StateError:
		fill, filled = coral, true
	}

	canvas := image.NewNRGBA(image.Rect(0, 0, size, size))
	center := float64(size-1) / 2
	outer := center - 1.5
	inner := outer - 4.5

	for y := range size {
		for x := range size {
			distance := math.Hypot(float64(x)-center, float64(y)-center)
			// Anti-alias the edges by fading the last pixel of each boundary.
			alpha := coverage(outer - distance)
			if !filled {
				alpha *= coverage(distance - inner)
			}
			if alpha <= 0 {
				continue
			}
			shade := fill
			shade.A = uint8(float64(fill.A) * alpha)
			canvas.SetNRGBA(x, y, shade)
		}
	}

	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, canvas); err != nil {
		// Encoding an in-memory RGBA image cannot fail; an empty icon is still better
		// than crashing the client over a tray glyph.
		return nil
	}
	return buffer.Bytes()
}

// ICO wraps the PNG in an ICO container, which is what the Windows tray expects.
func ICO(state State) []byte {
	payload := PNG(state)
	if payload == nil {
		return nil
	}
	buffer := &bytes.Buffer{}
	// ICONDIR: reserved, type 1 (icon), one image.
	binary.Write(buffer, binary.LittleEndian, uint16(0))
	binary.Write(buffer, binary.LittleEndian, uint16(1))
	binary.Write(buffer, binary.LittleEndian, uint16(1))
	// ICONDIRENTRY: 32x32, no palette, 32bpp, PNG payload at offset 22.
	buffer.WriteByte(size)
	buffer.WriteByte(size)
	buffer.WriteByte(0)
	buffer.WriteByte(0)
	binary.Write(buffer, binary.LittleEndian, uint16(1))
	binary.Write(buffer, binary.LittleEndian, uint16(32))
	binary.Write(buffer, binary.LittleEndian, uint32(len(payload)))
	binary.Write(buffer, binary.LittleEndian, uint32(22))
	buffer.Write(payload)
	return buffer.Bytes()
}

// coverage turns a signed distance in pixels into an alpha multiplier.
func coverage(distance float64) float64 {
	switch {
	case distance >= 0.5:
		return 1
	case distance <= -0.5:
		return 0
	default:
		return distance + 0.5
	}
}
