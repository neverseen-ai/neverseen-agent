package tray

import _ "embed"

var (
	//go:embed icons/masking.ico
	maskingIcon []byte

	//go:embed icons/partial.ico
	partialIcon []byte

	//go:embed icons/unmasked.ico
	unmaskedIcon []byte
)
