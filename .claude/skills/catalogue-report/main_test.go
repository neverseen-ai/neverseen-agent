package main

import (
	"bytes"
	"html/template"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// go test ./.claude/skills/catalogue-report

// The two ends the score was asked to separate: a value with one shape and a parser
// to confirm it, and one with as many shapes as there are ways of writing it. If the
// formula ever stops putting them apart, it is measuring something else.
func TestTheScoreSeparatesAFixedShapeFromFreeText(t *testing.T) {
	scores := map[string]int{}
	for _, r := range assess(pii.AllPatterns()) {
		scores[r.Code] = r.Score
	}
	if s := scores[string(pii.CatIPAddr)]; s != 100 {
		t.Errorf("an IPv4 address scores %d, want 100", s)
	}
	if s := scores[string(pii.CatAnthropicKey)]; s != 100 {
		t.Errorf("a prefixed vendor key scores %d, want 100", s)
	}
	if s := scores[string(pii.CatAddress)]; s > 60 {
		t.Errorf("a postal address scores %d, want it among the hard ones (<= 60)", s)
	}
	// The catch-all is the hardest credential: every other secret has a vendor's
	// shape to look for, and it has only a name in front of it.
	generic := scores[string(pii.CatGenericSecret)]
	for _, r := range assess(pii.SecretPatterns()) {
		if r.Code != string(pii.CatGenericSecret) && r.Score <= generic {
			t.Errorf("%s scores %d, not above the generic secret's %d", r.Code, r.Score, generic)
		}
	}
}

// A literal in the context names where a value is, not what it looks like: the
// generic secret's `password=` once scored it among the easiest categories.
func TestAContextLiteralDoesNotAnchorTheValue(t *testing.T) {
	p := pii.Pattern{Regex: regexp.MustCompile(`password[ \t]*=[ \t]*(\S+)`), Group: 1}
	if valueShape(p).anchored {
		t.Fatal("the keyword before the value was counted as the value's own anchor")
	}
	p = pii.Pattern{Regex: regexp.MustCompile(`(?:^|[^A-Za-z0-9])(sk-[A-Za-z0-9]{20,})`), Group: 1}
	if !valueShape(p).anchored {
		t.Fatal("a vendor prefix inside the value was not counted as an anchor")
	}
}

func TestAnAlternationIsOnlyAsAnchoredAsItsWeakestBranch(t *testing.T) {
	for expr, want := range map[string]bool{
		`xox[bpa]-[0-9]+`:                                       true,
		`(?:sk|pk)_live_[0-9a-z]+`:                              true,
		`gh[pousr]_[a-zA-Z0-9]{36,}`:                            true,
		`(?:eyJ|ewogIC|ewo)[a-z]+`:                              true,
		`(?:rue|avenue|bd) [A-Za-z ]+`:                          false,
		`\b[0-9a-fA-F]{24}\b`:                                   false,
		`[0-3]?\d/[01]?\d/(?:19|20)\d\d`:                        false,
		`(?:0[1-9]|[12]\d|3[01])/(?:0[1-9]|1[0-2])`:             false,
		`(?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}`:       true,
		`SK[a-fA-F0-9]{32}`:                                     true,
		`00[A-Za-z0-9]{13}![A-Za-z0-9._-]{79,259}[A-Za-z0-9_-]`: true,
		`SK[a-fA-F0-9]{8}`:                                      false,
		`SK[a-fA-F0-9]{32,}`:                                    false,
	} {
		if got := valueShape(pii.Pattern{Regex: regexp.MustCompile(expr)}).anchored; got != want {
			t.Errorf("%s anchored = %v, want %v", expr, got, want)
		}
	}
}

// A negated class runs to the end of Unicode and so holds a space, but it says "up to
// the delimiter": read as spanning words, it made an email address free text.
func TestANegatedClassIsNotFreeText(t *testing.T) {
	if s := valueShape(pii.Pattern{Regex: regexp.MustCompile(`[^@\s"]+@[a-z0-9.-]+`)}); s.spaces {
		t.Error("a negated class was read as spanning words")
	}
	if s := valueShape(pii.Pattern{Regex: regexp.MustCompile(`[0-9]+[ \t][A-Za-z ]+`)}); !s.spaces || !s.open {
		t.Errorf("a street name was not read as free text: %+v", s)
	}
}

// One plate is one notation, every European plate together is forty: the EU row must
// score the category lower than any single country does, or the matrix says nothing.
func TestTheEuropeanRowScoresPlatesBelowAnyCountry(t *testing.T) {
	m := countryMatrix()
	col := -1
	for i, c := range m.Columns {
		if c.Code == string(pii.CatLicPlate) {
			col = i
		}
	}
	if col < 0 {
		t.Fatal("no licence plate column")
	}
	eu := m.Rows[0].Cells[col]
	for _, r := range m.Rows[1:] {
		if s := r.Cells[col]; s != 0 && s <= eu {
			t.Errorf("%s scores plates %d, not above the European row's %d", r.Name, s, eu)
		}
	}
}

func TestTheTemplateRenders(t *testing.T) {
	var b bytes.Buffer
	err := template.Must(template.New("report").Parse(page)).Execute(&b, map[string]any{
		"Penalty":  map[string]int{},
		"Sections": []section{newSection("Secrets", "", assess(pii.SecretPatterns()))},
		"Matrix":   countryMatrix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Anthropic key") || !strings.Contains(b.String(), "By country") {
		t.Fatal("the page is missing a section")
	}
}

// A second way to find a value already found is coverage, not difficulty. Counted as
// one more notation, reading the AWS secret beside its key id dropped the category
// from 85 to 80: the report scored the fix of a leak as a loss.
func TestAContextForAFormAlreadyReadCostsNothing(t *testing.T) {
	all := pii.AllPatterns()
	named := slices.DeleteFunc(slices.Clone(all), func(p pii.Pattern) bool {
		return p.Category == pii.CatAWSSecretKey && p.Find != nil
	})
	if len(named) == len(all) {
		t.Fatal("no AWS secret pattern without a name: the case this test is about is gone")
	}
	score := func(patterns []pii.Pattern) int {
		for _, r := range assess(patterns) {
			if r.Code == string(pii.CatAWSSecretKey) {
				return r.Score
			}
		}
		return -1
	}
	if before, after := score(named), score(all); after < before {
		t.Errorf("a second context for the same form took the score from %d to %d", before, after)
	}
}

// The other half: one more form is one more case, and must still cost. Europe's
// plates are forty-two notations of many shapes, and a single country's are one.
func TestOneMoreFormStillCosts(t *testing.T) {
	plates := func(codes ...string) row {
		for _, r := range assess(pii.LocalePatterns(codes)) {
			if r.Code == string(pii.CatLicPlate) {
				return r
			}
		}
		t.Fatalf("no plate row for %v", codes)
		return row{}
	}
	fr, frbe := plates("fr"), plates("fr", "be")
	if frbe.Forms <= fr.Forms {
		t.Errorf("France and Belgium read %d plate forms, France alone %d: Belgium's own shape was folded", frbe.Forms, fr.Forms)
	}
}

func TestSignature(t *testing.T) {
	for v, want := range map[string]string{
		"AB-123-CD":      "a2-93-a2",
		"06 12 34 56 78": "92 92 92 92 92",
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY": "a13/a9a5/a18",
	} {
		if got := signature(v); got != want {
			t.Errorf("signature(%q) = %q, want %q", v, got, want)
		}
	}
}
