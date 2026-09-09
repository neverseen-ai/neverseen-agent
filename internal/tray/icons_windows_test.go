//go:build windows

package tray

import (
	"encoding/binary"
	"fmt"
)

// iconSize reports an icon's pixel dimensions and refuses one that is not the format
// the toolkit on this platform is handed.
//
// Windows is the exception the shared test cannot paper over: systray calls
// CreateIconFromResourceEx, which takes an .ico and nothing else, so these three are
// ICO files rather than the PNGs every other platform gets. image.DecodeConfig has no
// decoder for one — the standard library ships none and a dependency for three files
// read only by a test would be a poor trade — so the directory is read directly.
//
// The format is a six-byte header (reserved 0, type 1, and the number of images)
// followed by one sixteen-byte entry per image, whose first two bytes are the width
// and the height. A zero there means 256, which is the format's way of fitting the
// only size that does not fit in a byte.
func iconSize(raw []byte) (width, height int, err error) {
	const header, entry = 6, 16
	if len(raw) < header+entry {
		return 0, 0, fmt.Errorf("%d bytes is too short for an icon directory", len(raw))
	}
	if binary.LittleEndian.Uint16(raw[0:2]) != 0 || binary.LittleEndian.Uint16(raw[2:4]) != 1 {
		return 0, 0, fmt.Errorf("not an ICO: reserved=%d type=%d",
			binary.LittleEndian.Uint16(raw[0:2]), binary.LittleEndian.Uint16(raw[2:4]))
	}
	if n := binary.LittleEndian.Uint16(raw[4:6]); n == 0 {
		return 0, 0, fmt.Errorf("the icon directory holds no images")
	}

	size := func(b byte) int {
		if b == 0 {
			return 256
		}
		return int(b)
	}
	return size(raw[header]), size(raw[header+1]), nil
}
