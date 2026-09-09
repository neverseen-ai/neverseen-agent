package tray

// The three status icons, generated and committed, in two formats.
//
// Regenerate both with:
//
//	go run ./internal/tray/icons/generate.go
//
// Committed rather than built, for the reason testdata/heartbeats.json is: a
// generated asset a reviewer can look at beats a build step nobody can, and the
// alternative is an SVG rasteriser in go.mod for six thirty-two-by-thirty-two pixel
// pictures. The generator carries the geometry and the reasoning; TestTheIcons holds
// them to being what they claim.
//
// # Why there are two formats and not one
//
// The PNG is black plus alpha because macOS is handed it as a *template* image and
// recolours it for a light or a dark bar; a coloured icon there is a sticker that goes
// unreadable half the time. Windows does neither thing. It reads ICO — a PNG handed to
// the shell is not a wrong-looking icon, it is no icon at all — and it draws exactly
// what it is given, so the black template would be invisible on the dark taskbar that
// is the Windows 11 default. An icon that is working and invisible reads as an agent
// that is not running, which is the one thing this icon exists to disprove.
//
// partialIcon is the agent masking with categories switched off. It is not a cosmetic
// third option: such an agent is masking, so the masking icon would be the green light
// over the values that are not being replaced, and the unmasked one would be a lie
// about the twenty-odd categories that are.

// (The embeds live in icons_other.go and icons_windows.go, which differ only in
// the file each name points at.)
