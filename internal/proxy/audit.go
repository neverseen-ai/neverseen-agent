package proxy

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// The audit console is the one place in this agent where a real value is written
// out in clear, and it is deliberate.
//
// Everywhere else the rule holds without exception: the log carries counts and
// category names, the heartbeat carries no content at all, and a test walks the
// contract to keep it that way. Those surfaces are read by somebody other than
// the person whose data it is — a dashboard, a support ticket, a log shipper —
// and a value that reaches one of them has left the machine as surely as if it
// had gone to the model.
//
// `cloakfleet proxy -a` is the opposite situation: one operator, at their own
// keyboard, running the agent in the foreground of their own terminal on their own
// data, to answer "is my address actually being replaced". That question cannot be
// answered by a count, and answering it by reading a masked body in one window and
// guessing at the other is how the shape bugs got in.
//
// It is a flag rather than an environment variable, and the difference matters less
// than it did: this used to be a command of its own, which meant no configuration
// could turn it on under the background service. A flag can go in a service
// definition, where this agent's output is a log file — so the command's banner warns
// about exactly that on every start, because a rule nobody reads is not a rule.
type auditor struct {
	// mu because two requests in flight write to one terminal, and because what
	// it writes is a block of several lines: interleaved halves of two exchanges
	// is an audit trail nobody can read.
	mu sync.Mutex
	w  io.Writer

	// colour is off unless the writer is a terminal, so `cloakfleet proxy -a | tee
	// audit.log` and a test both get plain text rather than escape sequences
	// through the middle of a value.
	colour bool

	// traces is nil unless somebody asked for the bodies to be recorded. Its
	// methods are nil-safe, so the request path calls them without a branch.
	traces *tracer
}

// newAuditor returns nil when neither -a nor -v was asked for, so the ordinary agent
// carries no audit state at all rather than one that is switched off.
//
// Either alone is enough to build one, and that is worth being explicit about: the two
// flags are independent. -v with no -a records both bodies to files and prints nothing,
// which is what somebody wants when they mean to read the traffic afterwards rather
// than watch it go past; -a with no -v prints the transformations and keeps nothing.
// Requiring a console writer to record a trace would have made the quiet half of that
// silently do nothing.
func newAuditor(w io.Writer, traces *tracer) *auditor {
	if w == nil && traces == nil {
		return nil
	}
	return &auditor{w: w, colour: isTerminal(w), traces: traces}
}

// writes reports whether anything is printed to a console at all.
func (a *auditor) writes() bool { return a != nil && a.w != nil }

// traceDir reports where this console's bodies are written, or "" for none.
func (a *auditor) traceDir() string {
	if a == nil {
		return ""
	}
	return a.traces.Dir()
}

// isTerminal reports whether writing here reaches a screen.
//
// Asked of the writer rather than of NO_COLOR, and that is the environment rule
// rather than an opinion about the convention: this package reads the agent's own
// settings and nothing else. Redirecting the console to a file or a pipe — which
// is what somebody setting NO_COLOR here would be doing — already produces plain
// text.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// The palette is four colours and a dim, because an audit console is read by
// somebody looking for one thing: the values. Anything that is not a value or a
// replacement is dim, so the eye lands on the two that matter.
const (
	ansiReset  = "\033[0m"
	ansiDim    = "\033[2m"
	ansiBold   = "\033[1m"
	ansiRed    = "\033[31m" // a replacement: what stands in for a value
	ansiYellow = "\033[33m" // leaving the machine
	ansiGreen  = "\033[32m" // coming back

	// Bright rather than the standard blue, which on a dark terminal — which is
	// what a console like this is read on — is close to unreadable against the
	// background.
	ansiBlue = "\033[94m" // a value in clear
)

// paint wraps text in an escape, or returns it untouched when the console is not
// a terminal.
func (a *auditor) paint(colour, text string) string {
	if !a.colour {
		return text
	}
	return colour + text + ansiReset
}

// request writes one exchange's outbound half: what the tool sent, what was
// replaced in it, and what actually left for the provider.
//
// It returns the trace's path, or "" when nothing is being recorded, so the
// response half can be appended to the same file when the answer arrives.
//
// One block written under one lock, rather than a line at a time as the masking
// finds things. Two tools talking to the agent at once would otherwise interleave
// their bodies, and a body read half from one exchange and half from another is
// the reading an audit must never allow.
func (a *auditor) request(session, provider, received, sent string, count int, replaced [][2]string) string {
	if a == nil {
		return ""
	}

	// Written before anything is printed, so the console can name the file it went
	// to — and so a -v run with no console still records, which is half of what the
	// two flags are for.
	path, err := a.traces.write(session, provider, received, sent, count, replaced)
	if !a.writes() {
		return path
	}

	var b strings.Builder
	b.WriteString(a.rule("IN   from the tool", session, received))
	for _, pair := range replaced {
		b.WriteString(a.line(ansiYellow, "MASK",
			a.paint(ansiBlue, pair[0]), a.paint(ansiRed, pair[1])))
	}
	// The count, because the MASK lines above are first sightings and the console
	// showed nothing at all for an exchange whose every value the session had
	// already seen — which reads as "nothing was masked" over a body in which
	// three values were. Printed whenever anything was replaced, so the absence of
	// a MASK line is explained rather than left to be interpreted.
	if count > 0 {
		b.WriteString(a.paint(ansiDim,
			fmt.Sprintf("     %s\n", replacedSummary(count, len(replaced)))))
	}
	b.WriteString(a.rule("OUT  to "+provider, session, sent))

	// The bodies go to a file, or nowhere. On screen they scroll the MASK lines
	// away — a coding tool resends tens of kilobytes of system prompt every turn —
	// and those lines are what an operator is watching. The file is where the two
	// halves are read against each other, which is the finding no count carries: a
	// value present in both is one the catalogue never recognised.
	switch {
	case err != nil:
		// Said and carried on. A trace that could not be written must not stop the
		// masking — the rule the telemetry already follows, that the thing which
		// records the control must never be able to take it down.
		b.WriteString(a.line(ansiRed, "TRACE", "not written", err.Error()))
	case path != "":
		b.WriteString(a.paint(ansiDim, "     bodies in "+path) + "\n")
	}

	a.mu.Lock()
	fmt.Fprint(a.w, b.String())
	a.mu.Unlock()

	return path
}

// response files the provider's answer with the trace this exchange already wrote.
//
// Nothing is printed: the console deliberately carries no body, and an answer is
// the largest one of the three. The failure is logged by the caller rather than
// here, for the reason the outbound half already gives — a trace that cannot be
// written must never be able to take the masking down with it.
func (a *auditor) response(ref *traceRef, raw string) error {
	if a == nil {
		return nil
	}
	return a.traces.appendResponse(ref, raw)
}

// unmasked reports a replacement being turned back into the value it stands for.
//
// A line at a time, unlike the request block: on a streaming response the
// restorations happen as the answer arrives, and holding them back to group them
// would mean the console showed nothing until the model had finished.
//
// The argument order is the wire order — what came back, then what the caller
// reads — so the two halves of an exchange read as a round trip.
func (a *auditor) unmasked(replacement, original string) {
	if !a.writes() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	fmt.Fprint(a.w, a.line(ansiGreen, "UNMASK",
		a.paint(ansiRed, replacement), a.paint(ansiBlue, original)))
}

// line is one MASK or UNMASK.
//
// The two halves arrive painted, by the caller that knows which is which. Sniffed
// instead — colouring whatever looks like a bracket token as the replacement — a
// stand-in in fake mode came out marked as a value in clear, which is the exact
// opposite of what it is.
func (a *auditor) line(actionColour, action, first, second string) string {
	return fmt.Sprintf("%s %s %s %s\n",
		// One escape rather than a bold nested in a colour: the two codes
		// combined leave a single reset behind, which is what a terminal that
		// strips escapes for a log file has to deal with.
		a.paint(ansiBold+actionColour, action),
		first,
		a.paint(ansiDim, "TO"),
		second)
}

// ruleWidth is where the separator stops. Eighty columns because that is the
// narrowest terminal anybody still uses, and a rule that wrapped would put a row
// of dashes between every body.
const ruleWidth = 78

// rule is the labelled separator that opens a half of an exchange, carrying the
// session and the size, so a body scrolling past can still be accounted for at a
// glance.
func (a *auditor) rule(label, session, body string) string {
	head := fmt.Sprintf("── %s · session=%s · %d B ", label, session, len(body))
	if pad := ruleWidth - len([]rune(head)); pad > 0 {
		head += strings.Repeat("─", pad)
	}
	return a.paint(ansiDim, head) + "\n"
}

// The console holds no body, and that is why nothing here marks one.
//
// Both halves used to be printed with the values about to change hands painted, so
// that what was *unmarked* in both was the finding. That reading now lives in the
// trace file, where the two bodies sit one after the other and a diff says the same
// thing without a colour — which a file must not carry anyway, since escapes through
// the middle of a value make it unsearchable for the value itself.
//
// So the marking went with the bodies, rather than being left behind as a painter
// nothing calls. What stays is the palette, because the MASK and UNMASK lines still
// use it: blue is a value in clear, red is a replacement, and those lines are the
// whole of what the console now shows about content.

// unmaskedSeen returns the callback one response reports its restorations
// through, or nil when there is no audit console.
//
// Nil rather than a no-op function on purpose: the expander checks it once per
// token, and an agent that is not auditing must not pay a call per token of every
// response to arrive at "print nothing".
//
// One reporter per response, holding what it has already named, so a replacement
// restored in forty places reads as one restoration — the same grain as the MASK
// line, which is minted once. Per response rather than per agent: the same value
// coming back in the next answer is a new exchange, and an operator watching this
// console is watching exchanges go past.
func (a *auditor) unmaskedSeen() func(replacement, original string) {
	// Nil for a -v run with no console too: the trace records the answer as it
	// arrived, before a single replacement was expanded, so a restoration is a
	// console event and nothing else has a use for it.
	if !a.writes() {
		return nil
	}
	var (
		mu   sync.Mutex
		seen = map[string]bool{}
	)
	return func(replacement, original string) {
		mu.Lock()
		first := !seen[replacement]
		seen[replacement] = true
		mu.Unlock()
		if first {
			a.unmasked(replacement, original)
		}
	}
}
