package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A trace is one exchange written to a file: the body that arrived, the body that
// left, every value replaced between them, and the answer that came back.
//
// It exists because the console cannot hold them. A coding tool resends tens of
// kilobytes of system prompt every turn, so bodies on screen scroll the MASK lines
// away — and those lines are what an operator is watching. So the console keeps the
// summary and the transformations, and the bodies go to a file per exchange, where
// they can be read at leisure and diffed against each other.
//
// # Why this is off unless somebody asks
//
// A trace is the only thing this agent writes to disk that holds a value in clear.
// The log carries counts and category names; the heartbeat carries no content at
// all; the audit console carries values but only to one terminal, and it stops with
// it. A file outlives the run and can be read by anything that can read the
// directory, which is a different kind of exposure — closer to a log shipper than to
// somebody watching their own screen.
//
// So it takes a flag on a command that is already a foreground mode, never an
// environment variable, and never `neverseen proxy`: a background service writing
// prompts to disk is the one thing this design must not be able to do by accident.
// The directory is 0700 and each file 0600, the same treatment the control key gets,
// because both are things only their owner may read.

// traceDirPerm and traceFilePerm keep a trace readable by its owner and nobody else.
const (
	traceDirPerm  = 0o700
	traceFilePerm = 0o600
)

// tracer writes one file per exchange into a directory.
type tracer struct {
	dir string

	// mu serialises the counter and the writes. Two tools talking to the agent at
	// once would otherwise be handed the same sequence number, and one file would
	// hold half of each exchange — the reading an audit must never allow, which is
	// the same reason the console writes its block under one lock.
	mu   sync.Mutex
	seq  int
	now  func() time.Time
	last string
}

// newTracer prepares the directory, or reports why it cannot.
//
// The directory is created here rather than at the first exchange, so an operator
// who cannot write where they asked is told at start-up instead of discovering it
// after the traffic they wanted to look at has gone past.
func newTracer(dir string) (*tracer, error) {
	if dir == "" {
		return nil, nil
	}

	resolved, err := expandHome(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(resolved, traceDirPerm); err != nil {
		return nil, fmt.Errorf("create %s: %w", resolved, err)
	}
	// Tightened even when the directory already existed, because a trace in a
	// world-readable directory is a prompt anybody on the machine can read.
	if err := os.Chmod(resolved, traceDirPerm); err != nil {
		return nil, fmt.Errorf("set the permissions on %s: %w", resolved, err)
	}

	return &tracer{dir: resolved, now: time.Now}, nil
}

// Dir reports where traces are written, for the line the command prints.
func (t *tracer) Dir() string {
	if t == nil {
		return ""
	}
	return t.dir
}

// write records one exchange and returns the path it went to.
//
// The path is returned rather than logged from here, so the console prints it in the
// same block as the rest of the exchange: a filename on a line of its own, arriving
// between two exchanges, belongs to neither.
func (t *tracer) write(session, provider, received, sent string, count int, replaced [][2]string) (string, error) {
	if t == nil {
		return "", nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.seq++
	name := t.filename(session, provider)
	path := filepath.Join(t.dir, name)

	body := traceBody(session, provider, received, sent, count, replaced)
	if err := os.WriteFile(path, []byte(body), traceFilePerm); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	t.last = path
	return path, nil
}

// filename is the timestamp, a sequence number and the provider.
//
// Timestamp first so `ls` is chronological, which is how somebody looks for the
// exchange they have just made. The sequence number because a coding tool sends
// several requests in the same second and a timestamp alone would have them
// overwrite each other — the one failure that loses exactly the exchange being
// looked for. The session because two tools talking to one agent produce two
// interleaved streams, and which file belongs to which is the first thing to know.
func (t *tracer) filename(session, provider string) string {
	return fmt.Sprintf("%s-%04d-%s-%s.txt",
		t.now().UTC().Format("20060102T150405"), t.seq, safeForFilename(session), safeForFilename(provider))
}

// safeForFilename keeps a caller-supplied name from deciding where a file goes.
//
// The session comes from a header the caller controls, so it could be "../../etc" or
// carry a slash by accident. Anything that is not a letter, a digit, a dash or an
// underscore becomes a dash, which cannot traverse a directory and cannot collide
// with the separators in the name above.
func safeForFilename(name string) string {
	if name == "" {
		return "none"
	}

	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	// A name made entirely of separators would leave an empty field and two dashes
	// running together, which reads as a missing part rather than a sanitised one.
	if trimmed := strings.Trim(b.String(), "-"); trimmed != "" {
		return trimmed
	}
	return "none"
}

// traceBody is the file's contents.
//
// Plain text and no escape sequences, for the reason the console goes plain when it
// is not a terminal: escapes through the middle of a value make the file unsearchable
// for the value itself. Which also means the bodies carry no colour marking — and
// that is what the two of them side by side are for. A value present in both is one
// the catalogue never recognised, and `diff` says so better than any highlighting.
//
// A JSON body is indented, so the two halves line up field by field and a diff points
// at the field rather than at one enormous line.
func traceBody(session, provider, received, sent string, count int, replaced [][2]string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "session:  %s\n", session)
	fmt.Fprintf(&b, "provider: %s\n", provider)
	fmt.Fprintf(&b, "in:       %d bytes\n", len(received))
	fmt.Fprintf(&b, "out:      %d bytes\n", len(sent))
	fmt.Fprintf(&b, "replaced: %s\n", replacedSummary(count, len(replaced)))

	// The transformations before the bodies, so the file opens on the answer to
	// "what changed" and the bodies are there for "what did not".
	if len(replaced) > 0 {
		fmt.Fprint(&b, "\n")
		for _, pair := range replaced {
			fmt.Fprintf(&b, "MASK %s TO %s\n", pair[0], pair[1])
		}
	}

	fmt.Fprintf(&b, "\n%s\n%s\n", traceRule("IN   from the tool"), indented(received))
	fmt.Fprintf(&b, "\n%s\n%s\n", traceRule("OUT  to "+provider), indented(sent))
	return b.String()
}

// traceRule is the console's separator without its colour, so a file and a screen
// read the same way round.
func traceRule(label string) string {
	head := "── " + label + " "
	if pad := ruleWidth - len([]rune(head)); pad > 0 {
		head += strings.Repeat("─", pad)
	}
	return head
}

// indented lays a JSON body out over several lines, and leaves anything else exactly
// as it arrived.
//
// json.Indent rather than a decode and a re-encode, and that is the whole reason it is
// safe to do to a body somebody is reading as evidence: it inserts whitespace between
// tokens and touches nothing inside a string, so every byte of every value is still
// the byte that was sent. A decode and re-encode would rewrite an angle bracket into
// its numeric escape, reorder object keys and silently drop a duplicate one — three
// ways for the trace to disagree with the wire about what left the machine.
//
// The sizes reported above are measured on the body as it arrived, so no figure in the
// file counts bytes that never went anywhere.
//
// What it does not fix is worth naming: a coding tool's system prompt is one JSON
// string of tens of kilobytes with its newlines escaped, and after indenting it is
// still one enormous line. Turning those "\n" into real newlines would read far better
// and would stop the file holding the bytes that were sent, which is the one property
// a trace cannot trade away.
func indented(body string) string {
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(body), "", "  "); err != nil {
		// Not JSON, or not valid JSON. Either way the body is written as it arrived: a
		// partially indented document would be the file inventing a shape for
		// something it could not read.
		return body
	}
	return out.String()
}

// replacedSummary is the one sentence both audit surfaces use to say how much of a
// body changed.
//
// Two numbers, because they answer different questions and the file used to carry
// only the second while claiming to answer the first. A trace read "replaced: 0
// value(s)" above a body holding [SECRET_12], [EMAIL_8] and [EMAIL_9]: those three
// values were replaced here, and none of them was *minted* here — the session had
// seen each of them in an earlier exchange, so the mapping was reused and Reveal,
// which fires at minting, fired for none of them.
//
// The first number is what left the machine transformed, repeats included, which is
// the number somebody checking the control is looking at. The second is how many of
// them are listed below as MASK lines, so a body with replacements and no lines is
// explained by the header rather than read as a contradiction.
//
// Both are printed even when equal: a trace is evidence, and a header whose fields
// appear and disappear is one a reader has to parse rather than scan.
func replacedSummary(count, minted int) string {
	return fmt.Sprintf("%d value(s), %d of them first seen in this session", count, minted)
}

// appendResponse adds the provider's answer to a trace already on disk.
//
// Appended rather than written with the rest, because the two halves become known
// at different moments. The outbound half is recorded before the request leaves —
// deliberately, so an exchange the provider never answers still leaves behind the
// body that was about to go. Rewriting the file once the answer arrived would trade
// that guarantee for a tidier header, and would lose exactly the exchange somebody
// is looking at when an upstream hangs.
func (t *tracer) appendResponse(ref *traceRef, raw string) error {
	if t == nil || ref == nil || ref.path == "" {
		return nil
	}

	// The same lock as write, for the same reason: two exchanges finishing at once
	// must not interleave their sections.
	t.mu.Lock()
	defer t.mu.Unlock()

	f, err := os.OpenFile(ref.path, os.O_APPEND|os.O_WRONLY, traceFilePerm)
	if err != nil {
		return fmt.Errorf("append to %s: %w", ref.path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(traceResponse(ref, raw)); err != nil {
		return fmt.Errorf("append to %s: %w", ref.path, err)
	}
	return nil
}

// traceResponse is the section added when the answer has arrived: what the exchange
// cost, then the answer itself.
//
// The fields carry the same prefixed layout as the header at the top of the file,
// because that is where they belong and none of them could be written there — not
// one is known until the body ends. Prefixed the same way, `grep "^upstream:"` over
// a directory of traces answers "which exchange was slow" without opening one.
//
// Four durations rather than one total, because they are four different questions
// and a sum answers none of them: masking is this agent's own cost, upstream is the
// provider's latency, delivering is how long the answer took to arrive, and
// expanding is the restoration. On a stream the last two are wildly different — one
// is nearly all waiting — so adding them would hide both.
func traceResponse(ref *traceRef, raw string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "\nback:       %d bytes\n", len(raw))
	fmt.Fprintf(&b, "mask:       %s\n", ref.masking)
	fmt.Fprintf(&b, "upstream:   %s\n", ref.upstream)
	fmt.Fprintf(&b, "delivering: %s\n", ref.delivering)
	fmt.Fprintf(&b, "unmask:     %s\n", ref.expanding)

	// Tokens, never a price. Converting to money needs a table per model, and a
	// stale price in a file somebody reads as evidence is worse than no price —
	// which is the rule usage.go already states for the same reason.
	//
	// The line is omitted rather than printed as zeroes when the answer named no
	// model: a provider this agent does not know how to read is a gap, and a row of
	// noughts would read as an exchange that cost nothing.
	if ref.model != "" {
		fmt.Fprintf(&b, "model:      %s\n", ref.model)
		fmt.Fprintf(&b, "tokens:     in %d, out %d, cache read %d, cache write %d\n",
			ref.usage.Input, ref.usage.Output, ref.usage.CacheRead, ref.usage.CacheWrite)
	}

	// The readable view first, because that is what the file is opened for, and the
	// verbatim events below it, because that is what it is kept for. See
	// reassemble.go for why both.
	if text, ok := reassembled(raw); ok {
		fmt.Fprintf(&b, "\n%s\n%s", traceRule("BACK from "+ref.provider+", reassembled"), text)
	}

	fmt.Fprintf(&b, "\n%s\n%s\n", traceRule("BACK from "+ref.provider), indented(raw))
	return b.String()
}

// responseRecorder copies the provider's answer as it passes, and files it with the
// trace when the body is closed.
//
// It wraps the body before anything else does, so what it holds is what arrived:
// the rehydrator reads through it, and the copy is taken before a single
// replacement is expanded. That is the half worth keeping — read against the OUT
// body above it, it says which of the tokens sent up came back, and it carries no
// restored value, so recording the answer adds no class of exposure the file did
// not already have.
//
// A caller that hangs up mid-answer files what had arrived by then. Partial and
// honest: the alternative is a trace that vanishes for the exchange somebody
// abandoned, which is regularly the one they want to read.
//
// TODO: the answer is held in memory until it ends. A trace already carries a
// request body of the same order and bodies are written whole on purpose, but an
// answer that streamed for minutes is carried for as long. The upgrade is to keep
// the file open and append each event as it goes.
type responseRecorder struct {
	src io.ReadCloser
	raw bytes.Buffer

	// ended is when the answer finished arriving, which is not when the body is
	// closed: the buffered path reads the whole answer, then expands it, then
	// closes. Dated at the close, "how long the answer took to arrive" would
	// quietly include the agent's own work on it.
	ended time.Time

	file  func(raw string, ended time.Time)
	filed bool
}

func newResponseRecorder(body io.ReadCloser, file func(raw string, ended time.Time)) io.ReadCloser {
	return &responseRecorder{src: body, file: file}
}

func (r *responseRecorder) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		r.raw.Write(p[:n])
	}
	if err != nil && r.ended.IsZero() {
		r.ended = time.Now()
	}
	return n, err
}

// Close files the answer, once.
//
// Once, because the buffered path closes the body itself and the reverse proxy
// closes whatever it is left holding: filed twice, the trace would carry the answer
// two times over and read as if the provider had said everything twice.
func (r *responseRecorder) Close() error {
	if !r.filed {
		r.filed = true
		if r.ended.IsZero() {
			// The source never signalled its end: a caller that hung up mid-answer.
			r.ended = time.Now()
		}
		r.file(r.raw.String(), r.ended)
	}
	return r.src.Close()
}
