//go:build !windows

package tray

import _ "embed"

var (
	//go:embed icons/masking.png
	maskingIcon []byte

	//go:embed icons/partial.png
	partialIcon []byte

	//go:embed icons/unmasked.png
	unmaskedIcon []byte

	//go:embed icons/absent.png
	absentIcon []byte
)
