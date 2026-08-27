package tray

import _ "embed"

// The three menu bar icons, generated and committed.
//
// Regenerate them with:
//
//	go run ./internal/tray/icons/generate.go
//
// Committed rather than built, for the reason testdata/heartbeats.json is: a
// generated asset a reviewer can look at beats a build step nobody can, and the
// alternative is an SVG rasteriser in go.mod for three thirty-two-by-thirty-two
// pixel pictures. The generator carries the geometry and the reasoning; TestTheIcons
// holds them to being what they claim.
//
// Handed to macOS as template images, which is why they are black plus alpha: the
// system recolours a template for a light or a dark bar, and a coloured icon there
// would be a sticker that goes unreadable half the time.
var (
	//go:embed icons/masking.png
	maskingIcon []byte

	// partialIcon is the agent masking with categories switched off. It is not a
	// cosmetic third option: such an agent is masking, so the masking icon would be
	// the green light over the values that are not being replaced, and the unmasked
	// one would be a lie about the twenty-odd categories that are.
	//go:embed icons/partial.png
	partialIcon []byte

	//go:embed icons/unmasked.png
	unmaskedIcon []byte
)
