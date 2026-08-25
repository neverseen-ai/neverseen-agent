package proxy

import (
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

var playgroundTemplate = template.Must(template.New("playground").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Cloakfleet — what would be masked</title>
<style>
  :root { color-scheme: light dark; --line: #8883; --muted: #7c7c85; --ok: #2e7d32; --warn: #b26a00; }
  body { margin: 0; padding: 1.5rem;
         font: 14px/1.5 ui-sans-serif, system-ui, -apple-system, sans-serif; }
  h1 { font-size: 1.1rem; margin: 0 0 .25rem; }
  p.lede { margin: 0 0 1rem; color: var(--muted); max-width: 74ch; }
  .meta { margin: 0 0 1.25rem; font-size: 12px; color: var(--muted); }
  .meta code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .cols { display: grid; gap: 1rem; grid-template-columns: repeat(3, minmax(0, 1fr)); }
  @media (max-width: 1000px) { .cols { grid-template-columns: 1fr; } }
  section { border: 1px solid var(--line); border-radius: 6px; padding: .75rem; min-width: 0; }
  h2 { font-size: .8rem; text-transform: uppercase; letter-spacing: .06em;
       margin: 0 0 .5rem; color: var(--muted); font-weight: 600; }
  h2 .count { float: right; text-transform: none; letter-spacing: 0; font-weight: 400; }
  textarea, pre { width: 100%; box-sizing: border-box; margin: 0;
                  font: 12px/1.55 ui-monospace, SFMono-Regular, Menlo, monospace; }
  textarea { min-height: 26rem; resize: vertical; padding: .5rem;
             border: 1px solid var(--line); border-radius: 4px;
             background: transparent; color: inherit; }
  pre { min-height: 26rem; overflow: auto; white-space: pre-wrap; word-break: break-word;
        padding: .5rem; border: 1px solid transparent; }
  .bar { margin-top: 1rem; display: flex; gap: .75rem; align-items: baseline; flex-wrap: wrap; }
  button { font: inherit; padding: .45rem 1rem; border-radius: 4px;
           border: 1px solid var(--line); background: transparent; color: inherit;
           cursor: pointer; }
  button:hover { border-color: currentColor; }
  .hint { color: var(--muted); font-size: 12px; max-width: 74ch; }
  .ok { color: var(--ok); }
  .warn { color: var(--warn); }
  table { border-collapse: collapse; margin-top: 1.5rem; font-size: 12px; }
  th, td { text-align: left; padding: .25rem .9rem .25rem 0; vertical-align: top; }
  th { color: var(--muted); font-weight: 600; }
  td.val { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-all; }
  .tag { color: var(--muted); }
  .none { color: var(--muted); margin-top: 1.5rem; }
</style>
</head>
<body>
<h1>What would be masked</h1>
<p class="lede">The same text as written, then masked in each of the two
representations, using this agent's own configuration. Paste a record of your
own — with the values changed — and see what leaves the machine and what does
not.</p>
<p class="meta">Locales in use: <code>{{ .Locales }}</code>. Nothing on this page
is sent anywhere, stored, or written to the session vault.</p>

<form method="post" action="/test">
  <div class="cols">
    <section>
      <h2>As written</h2>
      <textarea name="text" spellcheck="false">{{ .Text }}</textarea>
    </section>
    <section>
      <h2>Token mode <span class="count">{{ .Token.Count }} replaced</span></h2>
      <pre>{{ .Token.Output }}</pre>
    </section>
    <section>
      <h2>Stand-in mode <span class="count">{{ .Fake.Count }} replaced</span></h2>
      <pre>{{ .Fake.Output }}</pre>
    </section>
  </div>
  <div class="bar">
    <button type="submit">Run it again</button>
    {{ if .RoundTrips }}
    <span class="hint ok">Round trip verified: unmasking the token column returns
    this text exactly, character for character.</span>
    {{ else }}
    <span class="hint warn">Round trip incomplete: unmasking the token column does
    not return this text exactly. That happens when the text already contains
    something shaped like a token.</span>
    {{ end }}
  </div>
  <p class="hint">Stand-ins are indexed rather than random, so the same text
  always produces the same ones. They are unattributable by construction —
  reserved ranges and deliberately invalid checksums — and the agent does not
  expand them on the way back, which is the trade: prose the model reads better,
  in exchange for an answer carrying values nobody can check. Tokens are restored
  in full, credentials included.</p>
</form>

{{ if .Findings }}
<table>
  <tr><th>Category</th><th>Detected value</th><th>Confidence</th><th>Recognised as</th></tr>
  {{ range .Findings }}
  <tr>
    <td>{{ .Category }}{{ if .TokenInFakeMode }} <span class="tag">— token even in stand-in mode</span>{{ end }}</td>
    <td class="val">{{ .Value }}</td>
    <td>{{ .Confidence }}</td>
    <td class="tag">{{ .Label }}</td>
  </tr>
  {{ end }}
</table>
{{ else }}
<p class="none">Nothing detected in this text. If you expected something, the
category may belong to a locale this agent has not loaded.</p>
{{ end }}
</body>
</html>
`))
