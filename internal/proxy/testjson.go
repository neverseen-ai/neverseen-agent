package proxy

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"sort"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// POST /test with a JSON body is what the console's Try it screen asks: the same two
// runs as the form page, over the deployment's own detector, answered as data.
//
// The same route rather than a reserved one of its own, because it is the same
// question and the same exposure: /test is open, and the JSON mode reads nothing the
// form does not. What it leaves out is the page's simulation — locales and a secret
// level ticked for one text — since the console draws what this agent does, not what
// another configuration would.

// testRequest is what the console sends.
type testRequest struct {
	Text string `json:"text"`
}

// testReply is what POST /test answers to JSON.
type testReply struct {
	Token      testColumn    `json:"token"`
	Fake       testColumn    `json:"fake"`
	RoundTrips bool          `json:"round_trips"`
	Findings   []testFinding `json:"findings"`
}

// testColumn is one substitution mode's rendering of the text.
type testColumn struct {
	Text  string `json:"text"`
	Count int    `json:"count"`

	// Masks are the replacements Text carries, for the page to highlight. Strings
	// rather than offsets: Go counts bytes and the page counts UTF-16 units, so one
	// accent ahead of a replacement put every highlight after it out of place.
	Masks []testMask `json:"masks"`
}

type testMask struct {
	Mask string `json:"mask"`

	// Credential is drawn apart: a secret keeps a token in both modes.
	Credential bool `json:"credential"`
}

// testFinding is one value the detector found: the page's findings table.
type testFinding struct {
	Code string `json:"code"`

	// Label is the category's name, Notation the pattern's — what it is, and what it
	// looked like.
	Label    string `json:"label"`
	Notation string `json:"notation"`

	Value      string `json:"value"`
	Confidence int    `json:"confidence"`
	Credential bool   `json:"credential"`

	// TokenInFakeMode is a value that keeps a bracket token under stand-ins: a
	// credential, or a category with no stand-in for its locale.
	TokenInFakeMode bool `json:"token_in_fake_mode"`
}

// testJSONMaxBytes caps the JSON body. Twice the text's own cap, because escaping
// lengthens what it encodes — a newline is two bytes once quoted — and the text is
// held to playgroundMaxBytes once it is decoded.
const testJSONMaxBytes = 2 * playgroundMaxBytes

// wantsJSON reports whether a request's body is JSON.
func wantsJSON(r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && media == "application/json"
}

// answerTest serves POST /test sent as JSON.
func (s *Server) answerTest(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, testJSONMaxBytes+1))
	if err != nil {
		http.Error(w, "neverseen: cannot read the request", http.StatusBadRequest)
		return
	}
	if len(body) > testJSONMaxBytes {
		http.Error(w, "neverseen: the submitted text is too large", http.StatusRequestEntityTooLarge)
		return
	}
	var req testRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "neverseen: the request is not a JSON object with a text", http.StatusBadRequest)
		return
	}
	if len(req.Text) > playgroundMaxBytes {
		http.Error(w, "neverseen: the submitted text is too large", http.StatusRequestEntityTooLarge)
		return
	}

	// The detector as it is now: a setting changed a moment ago changes the answer.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, testReplyFor(s.det, req.Text))
}

// testReplyFor renders text in both modes, as the form page does (playgroundView).
//
// The findings are the form page's own (playgroundFindings), so the two tables cannot
// come to disagree about a text.
func testReplyFor(det *detector.Detector, text string) testReply {
	found := playgroundFindings(det, text)
	secret := make(map[string]bool)
	findings := make([]testFinding, 0, len(found))
	for _, f := range found {
		category := pii.Category(f.Category)
		credential := pii.IsSecret(category)
		if credential {
			secret[f.Value] = true
		}
		findings = append(findings, testFinding{
			Code:            f.Category,
			Label:           pii.Label(category),
			Notation:        f.Label,
			Value:           f.Value,
			Confidence:      f.Confidence,
			Credential:      credential,
			TokenInFakeMode: f.TokenInFakeMode,
		})
	}

	token, tokenMapping, tokenCount := det.WithSubstitution(detector.SubstitutionToken).MaskOnce(text)
	fake, fakeMapping, fakeCount := det.WithSubstitution(detector.SubstitutionFake).MaskOnce(text)
	return testReply{
		Token:      testColumn{Text: token, Count: tokenCount, Masks: masksOf(tokenMapping, secret)},
		Fake:       testColumn{Text: fake, Count: fakeCount, Masks: masksOf(fakeMapping, secret)},
		RoundTrips: detector.Unmask(token, tokenMapping) == text,
		Findings:   findings,
	}
}

// masksOf lists the replacements a mapping minted, sorted so two answers to one text
// are the same bytes.
func masksOf(mapping map[string]string, secret map[string]bool) []testMask {
	out := make([]testMask, 0, len(mapping))
	for mask, value := range mapping {
		out = append(out, testMask{Mask: mask, Credential: secret[value]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mask < out[j].Mask })
	return out
}
