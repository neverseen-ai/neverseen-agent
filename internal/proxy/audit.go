package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
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
// `cloakfleet audit` is the opposite situation: one operator, at their own
// keyboard, running the agent in the foreground of their own terminal on their
// own data, to answer "is my address actually being replaced". That question
// cannot be answered by a count, and answering it by reading a masked body in one
// window and guessing at the other is how the shape bugs got in.
//
// So it is a mode, not a setting: nothing writes here unless the audit command
// built the agent, there is no environment variable that turns it on under
// `cloakfleet proxy`, and the output goes to the terminal that asked for it
// rather than to a logger somebody may have pointed at a file.
type auditor struct {
	// mu because two requests in flight write to one terminal, and because what
	// it writes is a block of several lines: interleaved halves of two exchanges
	// is an audit trail nobody can read.
	mu sync.Mutex
	w  io.Writer

	// colour is off unless the writer is a terminal, so `cloakfleet audit | tee
	// audit.log` and a test both get plain text rather than escape sequences
	// through the middle of a value.
	colour bool
}

// newAuditor returns nil when no writer is given, so the ordinary agent carries
// no audit state at all rather than one that is switched off.
func newAuditor(w io.Writer) *auditor {
	if w == nil {
		return nil
	}
	return &auditor{w: w, colour: isTerminal(w)}
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
// One block written under one lock, rather than a line at a time as the masking
// finds things. Two tools talking to the agent at once would otherwise interleave
// their bodies, and a body read half from one exchange and half from another is
// the reading an audit must never allow.
func (a *auditor) request(session, provider, received, sent string, replaced [][2]string) {
	if a == nil {
		return
	}

	var b strings.Builder
	b.WriteString(a.rule("IN   from the tool", session, received))
	b.WriteString(a.bodyIn(received, replaced))
	for _, pair := range replaced {
		b.WriteString(a.line(ansiYellow, "MASK",
			a.paint(ansiBlue, pair[0]), a.paint(ansiRed, pair[1])))
	}
	b.WriteString(a.rule("OUT  to "+provider, session, sent))
	b.WriteString(a.bodyOut(sent, replaced))

	a.mu.Lock()
	defer a.mu.Unlock()
	fmt.Fprint(a.w, b.String())
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
	if a == nil {
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

// A body is printed whole, however long.
//
// Whole, and that is the operator's call rather than a default: a coding tool
// resends tens of kilobytes of system prompt every turn, so this scrolls — but a
// ceiling would be the console deciding which part of the traffic is worth
// looking at, and the part it cut is exactly where an unrecognised value would
// be. The MASK and UNMASK lines are the record of what was transformed; the
// bodies are the record of what was sent, and half of one answers nothing.
//
// Each half marks what is about to change hands, so the two can be read against
// each other: in the body that arrived, the values that will be replaced; in the
// body that left, the replacements that will be turned back. Whatever is
// unmarked in both is what the catalogue never saw — which is the finding a count
// cannot carry.

// Both halves are marked by looking for the text itself rather than by byte
// offsets from the scan: the body is masked field by field through a JSON
// decoder, so a match's position is a position in a decoded string and means
// nothing in the raw document. A value carrying an escape — a quote, a newline —
// is therefore not marked, and that is the honest limit of this: it is still
// printed whole, and its MASK line still names it.
//
// bodyIn marks the values that were replaced; bodyOut marks what replaced them.
// Both walk the text once through mark, below.
func (a *auditor) bodyIn(text string, replaced [][2]string) string {
	return terminated(a.mark(indented(text), ansiBlue, sideOf(replaced, 0), false))
}

// indented lays a JSON body out over several lines, and leaves anything else
// exactly as it arrived.
//
// json.Indent rather than a decode and a re-encode, and that is the whole reason
// this is safe to do to a body somebody is reading as evidence: it inserts
// whitespace between tokens and touches nothing inside a string, so every byte of
// every value is still the byte that was sent. That is what lets mark go on
// finding a value by its own text, and what keeps this from being a body the
// provider never saw. A decode and re-encode would rewrite an angle bracket into
// its numeric escape, reorder object keys and silently drop a duplicate one —
// three ways for the console to disagree with the wire about what left the
// machine.
//
// It is applied whether or not the output is a terminal, unlike colour: a body
// redirected to a file is read by the same person for the same reason, and
// nothing downstream parses this.
//
// The size on the rule above is measured on the body as it arrived, so no figure
// here reports bytes that never went anywhere.
//
// What this does not fix is worth naming rather than leaving to be discovered: a
// coding tool's system prompt is one JSON string of tens of kilobytes with its
// newlines escaped, and after indenting it is still one enormous line. Turning
// those "\n" into real newlines would read far better and would stop the console
// showing the bytes that were sent, which is the one property an audit cannot
// trade away.
func indented(body string) string {
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(body), "", "  "); err != nil {
		// Not JSON, or not valid JSON. Either way the body is printed as it
		// arrived: a partially indented document would be the console inventing a
		// shape for something it could not read.
		return body
	}
	return out.String()
}

// bodyOut marks the replacements, from the pass that made them rather than from
// their shape.
//
// From the pass, because a replacement is not always a bracket token: in fake
// mode it is a stand-in that reads as prose — "1 rue de l'Exemple, 99000
// Villeneuve" — and a console looking for brackets marked nothing at all in the
// half where it matters most. What was substituted is a fact this exchange
// already knows.
//
// Tokens are marked as well as those, for the turn after: a conversation resends
// its history, so the body leaving on turn two carries tokens minted on turn one,
// which this pass never saw and the vault will still expand.
func (a *auditor) bodyOut(text string, replaced [][2]string) string {
	return terminated(a.mark(indented(text), ansiRed, sideOf(replaced, 1), true))
}

// sideOf pulls one half of the pairs out, skipping empties so mark cannot match
// an empty string at every position.
func sideOf(pairs [][2]string, side int) []string {
	out := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if pair[side] != "" {
			out = append(out, pair[side])
		}
	}
	return out
}

// mark paints, in one forward walk, whatever is longest at each position: one of
// the literals, or a bracket token when tokens is set.
//
// One walk rather than a replacement per literal, and that is not tidiness.
// Replacing them in turn paints a shorter value *inside* one already painted — a
// phone number inside the address containing it — and the inner reset ends the
// outer colour early, so the rest of the longer value comes out unmarked. Taking
// the longest match at each position, once, cannot do that. It is also why the
// token pass cannot simply run afterwards: a painted token still looks like a
// token, and would be painted again inside itself.
func (a *auditor) mark(text, colour string, literals []string, tokens bool) string {
	if !a.colour || (len(literals) == 0 && !tokens) {
		return text
	}

	// Longest first, so the loop below can stop at its first hit.
	sorted := append([]string(nil), literals...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })

	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		matched := ""
		for _, literal := range sorted {
			if strings.HasPrefix(text[i:], literal) {
				matched = literal
				break
			}
		}
		if tokens {
			if token := pii.TokenAt(text[i:]); len(token) > len(matched) {
				matched = token
			}
		}
		if matched == "" {
			b.WriteByte(text[i])
			i++
			continue
		}
		b.WriteString(a.paint(colour, matched))
		i += len(matched)
	}
	return b.String()
}

// terminated ends a body with a newline, so the rule that follows starts its own
// line whatever the body was.
func terminated(text string) string {
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

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
	if a == nil {
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
