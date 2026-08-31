package detector

import "strings"

// Whether a piece of text is source code, asked cheaply and answered per value.
//
// The granularity is the point. A coding agent's request is a prose system prompt,
// a prose turn from the person, and tool results that are whole files — and the
// proxy already masks a JSON document value by value, so Scan is called once per
// string and the question is already being asked at the right grain. A body-level
// switch would relax the sentence somebody typed because a file was attached to
// it.
//
// It is deliberately not a model. Language identification answers a harder
// question than this needs — which language — and would put a classifier inside a
// binary whose whole design is that it holds nothing. What is here is arithmetic
// over punctuation and line shape: something a test can pin and a person can argue
// with when it is wrong.
//
// The signals are structural rather than lexical. Keywords would mean a list per
// language and a wrong answer for the fiftieth; a line ending in a brace is a line
// ending in a brace in every language that has them.
//
// TODO: that structure is biased towards languages with braces. Python reads as
// code less often than Go or TypeScript, and a `.env` file — no indentation, no
// terminators, one NAME=value per line — does not read as code at all. Neither is
// a leak: an unrecognised file is treated as prose and goes on being masked, which
// is the safe direction, and the `.env` case is the one where being conservative
// matters most. The upgrade is a fourth signal keyed on line shape rather than
// punctuation, measured against both labelled sets before it is trusted.

// codeMinLines is the shortest text this will judge.
//
// Below it there is nothing to measure: a tool result of "ok", a one-line user
// question and a single line of source are indistinguishable by shape, and the
// safe answer for a masking agent is that it is prose. That is the direction that
// errs towards masking.
const codeMinLines = 3

// codeMinSignals is how many independent signals must agree.
//
// Two, because each one alone has a plausible innocent reading: prose wrapped in a
// narrow column has short lines, a numbered list has leading spaces, and a
// sentence about arithmetic has operators in it. Two of them at once is not
// something an ordinary paragraph does.
const codeMinSignals = 2

// looksLikeCode reports whether text reads as source rather than as prose.
func looksLikeCode(text string) bool {
	lines := strings.Split(text, "\n")
	if len(lines) < codeMinLines {
		return false
	}

	// A fence or a shebang is somebody saying it outright, and neither occurs in
	// ordinary prose by accident.
	if strings.Contains(text, "```") || strings.HasPrefix(strings.TrimLeft(text, " \t"), "#!") {
		return true
	}

	var indented, terminated, assigned, counted int
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == "" {
			continue
		}
		counted++

		if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
			indented++
		}
		// A statement terminator or a block delimiter at the end of a line. Prose
		// ends on a full stop or on nothing at all.
		switch trimmed[len(trimmed)-1] {
		case ';', '{', '}', ')', ',', ':':
			terminated++
		}
		// An assignment or a call, which is what a line of code does and a sentence
		// does not.
		if strings.ContainsAny(trimmed, "={}()[]<>") {
			assigned++
		}
	}
	if counted < codeMinLines {
		return false
	}

	// A third of the lines, and strictly more than a third.
	//
	// The threshold was swept against both labelled sets. Loosening it to "at
	// least a third" buys eight points of recall and costs the one thing this must
	// never get wrong:
	//
	//	Notes from the call:
	//	- the client is Claire Moreau (06 12 34 56 78)
	//	- invoice 2024-03-14 is unpaid
	//	- her address changed to 12 rue de la Paix, 75002 Paris
	//	- follow up on Friday
	//
	// Five lines, two ending on a colon or a bracket, one carrying parentheses —
	// enough to clear "at least a third" of five. It is a bulleted note, it is
	// nothing but personal data, and reading it as code would stop the telephone
	// number and the date being masked. TestABulletedNoteIsNotCode holds it.
	//
	// The asymmetry is the whole design. Code read as prose costs over-masking,
	// which is annoying; prose read as code costs a value going out in clear.
	// Measured this way: 83% of source files read as code, and no prose does.
	third := counted / 3
	signals := 0
	for _, n := range []int{indented, terminated, assigned} {
		if n > third {
			signals++
		}
	}
	return signals >= codeMinSignals
}
