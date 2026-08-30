package proxy

import (
	_ "embed"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/pkg/pii"
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

	text := s.det.Sample()
	if r.Method == http.MethodPost {
		submitted, err := submittedText(r)
		if err != nil {
			http.Error(w, err.Error(), err.status())
			return
		}
		if strings.TrimSpace(submitted) != "" {
			text = submitted
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := playgroundTemplate.Execute(w, s.playgroundView(text)); err != nil {
		// The status line has already gone out, so this can only be reported.
		s.log.Error("rendering the test page failed", "error", err)
	}
}

// submittedText reads the form field, capping the read before the body is
// buffered rather than after.
func submittedText(r *http.Request) (string, *playgroundError) {
	body, err := io.ReadAll(io.LimitReader(r.Body, playgroundMaxBytes+1))
	if err != nil {
		return "", &playgroundError{"cannot read the submitted form", http.StatusBadRequest}
	}
	if len(body) > playgroundMaxBytes {
		return "", &playgroundError{"the submitted text is too large", http.StatusRequestEntityTooLarge}
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", &playgroundError{"cannot parse the submitted form", http.StatusBadRequest}
	}
	return values.Get("text"), nil
}

type playgroundError struct {
	message string
	code    int
}

func (e *playgroundError) Error() string { return e.message }
func (e *playgroundError) status() int   { return e.code }

type playgroundView struct {
	Text    string
	Locales string

	Token playgroundColumn
	Fake  playgroundColumn

	// RoundTrips reports whether unmasking the token column returns the input
	// exactly. It is the product's claim, checked on the operator's own text
	// rather than asserted in a paragraph.
	RoundTrips bool

	Findings []playgroundFinding
}

type playgroundColumn struct {
	Output string
	Count  int
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

func (s *Server) playgroundView(text string) playgroundView {
	locales := "none"
	if l := s.det.Locales(); len(l) > 0 {
		locales = strings.Join(l, ", ")
	}

	// A detector per column, each with its own counters, so the same text renders
	// identically on every reload and the comparison is readable.
	tokenDet := s.det.WithSubstitution(detector.SubstitutionToken)
	masked, mapping, replaced := tokenDet.MaskOnce(text)

	fakeMasked, _, fakeReplaced := s.det.WithSubstitution(detector.SubstitutionFake).MaskOnce(text)

	return playgroundView{
		Text:       text,
		Locales:    locales,
		Token:      playgroundColumn{Output: masked, Count: replaced},
		Fake:       playgroundColumn{Output: fakeMasked, Count: fakeReplaced},
		RoundTrips: detector.Unmask(masked, mapping) == text,
		Findings:   s.playgroundFindings(text),
	}
}

// playgroundFindings lists what the catalogue found, in reading order, so a
// missing replacement can be told apart from an undetected value.
func (s *Server) playgroundFindings(text string) []playgroundFinding {
	matches := s.det.Scan(text)
	fakes := pii.NewFakeSet(s.det.Locales())

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
