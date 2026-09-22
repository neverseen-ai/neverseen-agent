package tray

// The four status icons, generated and committed, in three sets.
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
// # Why there are three sets and not one
//
// The difference is one question: does the platform recolour what it is handed?
//
// **macOS does**, which is what a *template* image is — black plus alpha, redrawn for a
// light or a dark bar. A coloured icon there is a sticker that goes unreadable half the
// time.
//
// **Windows does not.** It reads ICO — a PNG handed to the shell is not a wrong-looking
// icon, it is no icon at all — and it draws exactly what it is given, so the black
// template would be invisible on the dark taskbar that is the Windows 11 default. Drawn
// in a mid grey that carries against both themes, since one file serves them.
//
// **Linux does not either**, and it has room for more than grey. fyne.io/systray
// publishes the decoded pixels as a StatusNotifierItem IconPixmap and the panel paints
// them as they arrive — SetTemplateIcon there discards the template and keeps the
// regular icon. So this set is coloured: green while masking, amber with categories in
// clear, red when nothing is being replaced. The colour is added to the glyph and never
// instead of it, because it is the channel about eight percent of men read differently.
//
// An icon that is working and invisible reads as an agent that is not running, which is
// the one thing this icon exists to disprove. That is the failure both non-template sets
// exist to avoid.
//
// partialIcon is the agent masking with categories switched off. It is not a cosmetic
// third option: such an agent is masking, so the masking icon would be the green light
// over the values that are not being replaced, and the unmasked one would be a lie
// about the twenty-odd categories that are.
//
// absentIcon is no agent answering at all. It carries the same two filled squares as
// unmaskedIcon and loses the divider between them, because the divider is the agent
// standing between a value and where it was going. Both states leave the traffic in
// clear, which is why they were one picture until now; what separates them is what a
// person has to do next, and the icon is what they look at before doing it.

// (The embeds live in icons_other.go, icons_linux.go and icons_windows.go, which
// differ only in the file each name points at.)
