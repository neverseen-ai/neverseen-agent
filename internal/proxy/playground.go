package proxy

import (
	_ "embed"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// The /test page shows one text three ways: as written, masked with tokens, and
// masked with stand-ins.
//
// It exists because two things about this agent are judgement calls an operator
// cannot make from a log line. The first is which substitution mode to run — a
// model reasons better about prose than about brackets, but a stand-in in an
// answer is a value nobody can check. The second, and the one that actually
// costs deployments time, is whether the catalogue reads *their* data: an
// operator pastes a real record with the values changed, and sees in one screen
// what would be masked and what would go out in clear.
//
// So the page runs the deployment's own detector — its locales, its allow list —
// rather than a demonstration built on defaults, which would answer a different
// question than the one being asked.
//
// It also proves the claim rather than describing it: the token column is
// unmasked again and compared to the input, so the page says whether this text
// round-trips exactly.
//
// Deliberately plain. A form POST re-renders server-side, no JavaScript, nothing
// to keep in sync. The one thing it must get right is escaping, since it echoes
// text somebody typed — hence html/template rather than concatenation.

// playgroundMaxBytes caps the submitted text. The catalogue is dozens of
// expressions run over the whole input, and a page that accepts a POST should not
// accept a megabyte of adversarial text.
const playgroundMaxBytes = 32 * 1024

// handleTest serves GET and POST /test.
func (s *Server) handleTest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodPost:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	det := s.det
	text := det.Sample()
	if r.Method == http.MethodPost {
		form, err := submittedForm(r)
		if err != nil {
			http.Error(w, err.Error(), err.status())
			return
		}
		if det, err = s.simulated(form); err != nil {
			http.Error(w, err.Error(), err.status())
			return
		}
		if submitted := form.Get("text"); strings.TrimSpace(submitted) != "" {
			text = submitted
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page reads the detector as it is now — a locale loaded or a category
	// switched off since the last visit changes every column — and without this a
	// browser serves its heuristic copy on the next GET, over an agent that has
	// moved on.
	w.Header().Set("Cache-Control", "no-store")
	if err := playgroundTemplate.Execute(w, s.playgroundView(det, text)); err != nil {
		// The status line has already gone out, so this can only be reported.
		s.log.Error("rendering the test page failed", "error", err)
	}
}

// submittedForm reads the form, capping the read before the body is buffered
// rather than after.
func submittedForm(r *http.Request) (url.Values, *playgroundError) {
	body, err := io.ReadAll(io.LimitReader(r.Body, playgroundMaxBytes+1))
	if err != nil {
		return nil, &playgroundError{"cannot read the submitted form", http.StatusBadRequest}
	}
	if len(body) > playgroundMaxBytes {
		return nil, &playgroundError{"the submitted text is too large", http.StatusRequestEntityTooLarge}
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, &playgroundError{"cannot parse the submitted form", http.StatusBadRequest}
	}
	return values, nil
}

// simulated builds the detector a submission asks to be rendered with.
//
// A form that carries no configuration — a text posted on its own — renders with
// the agent's, so the page stays the tool it was. One that does gets a detector of
// its own (Detector.WithPolicy), and nothing here writes to the agent: PUT /policy
// is the one way to change what it does, and this page is neither authenticated
// nor, under -l, loopback only.
//
// The switched-off set is rebuilt from two halves. What the form drew is in
// "shown", and a drawn switch left unticked is off. What it could not draw — a
// category no locale of the rendered configuration could emit — keeps the agent's
// own intent, for the reason Health.Off exists beside Health.Groups: rebuilt from
// the reachable half alone, loading `us` on a page rendered with `fr` would have
// shown SSN masked over an agent that has it switched off.
func (s *Server) simulated(form url.Values) (*detector.Detector, *playgroundError) {
	if _, configured := form["secret_level"]; !configured {
		return s.det, nil
	}

	level, err := detector.ParseSecretLevel(form.Get("secret_level"))
	if err != nil {
		return nil, &playgroundError{err.Error(), http.StatusUnprocessableEntity}
	}

	on := make(map[pii.Category]bool)
	for _, code := range form["on"] {
		on[pii.Category(code)] = true
	}
	shown := make(map[pii.Category]bool)
	off := make([]pii.Category, 0)
	for _, code := range strings.Split(form.Get("shown"), ",") {
		if code == "" {
			continue
		}
		cat := pii.Category(code)
		shown[cat] = true
		if !on[cat] {
			off = append(off, cat)
		}
	}
	for _, cat := range s.det.Disabled() {
		if !shown[cat] {
			off = append(off, cat)
		}
	}

	det, err := s.det.WithPolicy(form["locale"], off, level)
	if err != nil {
		return nil, &playgroundError{err.Error(), http.StatusUnprocessableEntity}
	}
	return det, nil
}

type playgroundError struct {
	message string
	code    int
}

func (e *playgroundError) Error() string { return e.message }
func (e *playgroundError) status() int   { return e.code }

type playgroundView struct {
	Text string

	// The configuration the page rendered with, drawn as the switches that would
	// reproduce it, and whether it is the agent's own. Simulated is what puts the
	// banner up: a page that showed a simulated result under the agent's heading
	// would be the picture disagreeing with the traffic.
	Locales      []playgroundLocale
	SecretLevels []playgroundLevel
	Groups       []HealthGroup
	Shown        string
	Simulated    bool

	Token playgroundColumn
	Fake  playgroundColumn

	// RoundTrips reports whether unmasking the token column returns the input
	// exactly. It is the product's claim, checked on the operator's own text
	// rather than asserted in a paragraph.
	RoundTrips bool

	Findings []playgroundFinding
}

type playgroundLocale struct {
	Code string
	On   bool
}

type playgroundLevel struct {
	Name string
	On   bool
}

type playgroundColumn struct {
	Output string
	Count  int

	// Marked is Output with each token wrapped for highlighting, already escaped.
	// Only the token column has one: a stand-in is prose by design and there is
	// nothing in the text to find it by.
	Marked template.HTML
}

// markTokens escapes the masked text and wraps every token in <mark>.
//
// Escaped first, then marked, and the order is the safety argument: escaping
// touches nothing a token is made of — capitals, digits, underscores and the two
// brackets — and produces only entities, so the scan afterwards finds exactly the
// tokens the detector wrote and nothing the caller pasted. The result is the one
// template.HTML on the page, and this function is the only thing that builds it.
func markTokens(masked string) template.HTML {
	escaped := template.HTMLEscapeString(masked)
	return template.HTML(pii.ReplaceTokens(escaped, func(token string) (string, bool) {
		return "<mark>" + token + "</mark>", true
	}))
}

type playgroundFinding struct {
	Category   string
	Label      string
	Value      string
	Confidence int

	// TokenInFakeMode marks a category with no stand-in, which therefore keeps a
	// bracket token even in fake mode. Every credential is one, by design.
	TokenInFakeMode bool
}

func (s *Server) playgroundView(det *detector.Detector, text string) playgroundView {
	// A detector per column, each with its own counters, so the same text renders
	// identically on every reload and the comparison is readable.
	tokenDet := det.WithSubstitution(detector.SubstitutionToken)
	masked, mapping, replaced := tokenDet.MaskOnce(text)

	fakeMasked, _, fakeReplaced := det.WithSubstitution(detector.SubstitutionFake).MaskOnce(text)

	loaded := make(map[string]bool)
	for _, code := range det.Locales() {
		loaded[code] = true
	}
	locales := make([]playgroundLocale, 0, len(pii.LocaleCodes()))
	for _, code := range pii.LocaleCodes() {
		locales = append(locales, playgroundLocale{Code: code, On: loaded[code]})
	}

	levels := make([]playgroundLevel, 0, 3)
	for _, name := range detector.SecretLevels() {
		levels = append(levels, playgroundLevel{Name: name, On: name == det.SecretLevel().String()})
	}

	// The same list the menu bar draws, so the two surfaces agree; the hidden
	// field is what lets the next submission tell an unticked switch from one it
	// never drew.
	groups := catalogueOf(det)
	var shown []string
	for _, g := range groups {
		for _, c := range g.Categories {
			if !c.Locked {
				shown = append(shown, c.Code)
			}
		}
	}

	return playgroundView{
		Text:         text,
		Locales:      locales,
		SecretLevels: levels,
		Groups:       groups,
		Shown:        strings.Join(shown, ","),
		Simulated:    det != s.det,
		Token:        playgroundColumn{Output: masked, Count: replaced, Marked: markTokens(masked)},
		Fake:         playgroundColumn{Output: fakeMasked, Count: fakeReplaced},
		RoundTrips:   detector.Unmask(masked, mapping) == text,
		Findings:     playgroundFindings(det, text),
	}
}

// playgroundFindings lists what the catalogue found, in reading order, so a
// missing replacement can be told apart from an undetected value.
func playgroundFindings(det *detector.Detector, text string) []playgroundFinding {
	matches := det.Scan(text)
	fakes := pii.NewFakeSet(det.Locales())

	out := make([]playgroundFinding, 0, len(matches))
	for _, m := range matches {
		out = append(out, playgroundFinding{
			Category:   string(m.Category),
			Label:      m.Label,
			Value:      m.Value,
			Confidence: m.Confidence,
			// Asked of the locale that recognised the value, because that is what
			// decides which stand-in it would get.
			TokenInFakeMode: !fakes.Has(m.Category, m.Locale),
		})
	}
	return out
}

// playgroundTemplate is the page itself, in a file of its own.
//
// Embedded rather than held in a Go string literal, which is what it was: a hundred
// and thirty lines of HTML and CSS inside a .go file is a page no editor highlights,
// no formatter touches and nobody reads before changing. The file is the same bytes
// and the same escaping rules — html/template still parses it at start-up, so a
// broken template is a panic on the first build rather than a page served half
// rendered.
//
//go:embed playground.html
var playgroundHTML string

var playgroundTemplate = template.Must(template.New("playground").Parse(playgroundHTML))
