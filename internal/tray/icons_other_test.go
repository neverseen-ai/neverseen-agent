//go:build !windows

package tray

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
)

// iconSize reports an icon's pixel dimensions and refuses one that is not the format
// the toolkit on this platform is handed. Off Windows that is a bare PNG: systray
// takes the bytes and decodes them itself.
func iconSize(raw []byte) (width, height int, err error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return 0, 0, err
	}
	if format != "png" {
		return 0, 0, fmt.Errorf("format is %s, want png", format)
	}
	return cfg.Width, cfg.Height, nil
}
