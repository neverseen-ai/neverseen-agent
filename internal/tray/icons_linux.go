package tray

import _ "embed"

// The coloured set, because Linux draws exactly what it is handed.
//
// fyne.io/systray publishes these as a StatusNotifierItem IconPixmap and the panel
// paints the pixels as they arrive — SetTemplateIcon there discards the template and
// keeps the second argument. The black template beside this file would be a black
// shape on a dark panel: working and invisible, which reads as an agent that is not
// running. See tint in icons/generate.go for how the hues were chosen and what a
// single flat colour cannot do.
var (
	//go:embed icons/masking-linux.png
	maskingIcon []byte

	//go:embed icons/partial-linux.png
	partialIcon []byte

	//go:embed icons/unmasked-linux.png
	unmaskedIcon []byte

	//go:embed icons/absent-linux.png
	absentIcon []byte
)
