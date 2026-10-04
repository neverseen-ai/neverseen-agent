// Command catalogue-report writes an HTML page listing everything the agent tries to
// mask, split into credentials and personal data, with a coverage score per category:
// how few shapes the value can take, so how far one set of regular expressions can be
// trusted to catch every one of them. An IPv4 address has one shape and a parser to
// confirm it; a postal address has as many shapes as there are ways of writing one.
//
// The score is computed from the loaded patterns, not written by hand, so it follows
// the catalogue: a notation added to a category moves its score in the next report.
// It is a heuristic over the structure of the expressions — never a measured
// precision or recall, which is what `make bench-accuracy` reports — and the page
// says so.
//
// The page is committed as docs/catalogue.html, so it carries no date: it changes
// when the catalogue does and at no other time.
//
// Run from the repository root:
//
//	go run ./.claude/skills/catalogue-report [-o file.html]
package main

import (
	"cmp"
	_ "embed"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

//go:embed report.html
var page string

func main() {
	out := flag.String("o", filepath.Join("docs", "catalogue.html"), "where to write the report")
	flag.Parse()

	rows := assess(pii.AllPatterns())
	var secrets, personal []row
	for _, r := range rows {
		if r.Secret {
			secrets = append(secrets, r)
		} else {
			personal = append(personal, r)
		}
	}

	m := countryMatrix()

	f, err := os.Create(*out)
	if err != nil {
		fatal(err)
	}
	err = template.Must(template.New("report").Parse(page)).Execute(f, map[string]any{
		"Penalty": map[string]int{
			"ShapeOnly": penaltyShapeOnly, "FreeText": penaltyFreeText, "OpenLength": penaltyOpenLength,
			"PerForm": penaltyPerForm, "CapForms": capForms,
			"PerCountry": penaltyPerCountry, "CapCountries": capCountries,
			"Noisy": penaltyNoisy, "Floor": floor, "MinAnchor": minAnchor,
			"MinShortAnchor": minShortAnchor, "MinFixedLength": minFixedLength,
		},
		"Sections": []section{
			newSection("Secrets", "Credentials: the agent replaces them with a token and never a stand-in, and none can be switched off.", secrets),
			newSection("Personal data", "Personal and company data: masked by default, each family can be switched off on the settings page.", personal),
		},
		"Matrix": m,
	})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fatal(err)
	}
	fmt.Printf("%d categories (%d secrets, %d personal data) written to %s\n", len(rows), len(secrets), len(personal), *out)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "catalogue-report:", err)
	os.Exit(1)
}

// section splits its rows at 100: a hundred and more vendor keys with a fixed prefix
// would otherwise bury the handful of categories the report exists to point at.
type section struct {
	Title, Note string
	Rows, Full  []row
}

func newSection(title, note string, rows []row) section {
	s := section{Title: title, Note: note}
	for _, r := range rows {
		if r.Score == 100 {
			s.Full = append(s.Full, r)
		} else {
			s.Rows = append(s.Rows, r)
		}
	}
	return s
}

// row is one category, and the facts its score was computed from — printed beside
// it, so a score nobody agrees with can be traced to the fact that produced it.
type row struct {
	Code, Label string
	Secret      bool
	Score       int

	Notations  []string
	Unanchored int      // notations whose value carries no literal of its own
	Forms      int      // what those notations come to once a context for a form already read is folded in
	Countries  []string // locale codes contributing a pattern
	Anchored   bool     // every notation's value carries a literal of minAnchor runes
	Rule       bool     // a check beyond the shape: checksum, parser, plausibility
	FreeText   bool     // an unanchored value of open length that spans words
	OpenLength bool     // an unanchored value of open length within one word
	Noisy      bool     // the shape is one source code satisfies on its own
}

// minAnchor is the shortest run of fixed characters counted as a value announcing
// itself: sk-, SG., ://. Two is a separator, not a prefix — unless the value behind
// it has a bounded length of minFixedLength or more: SK and then exactly 32 hex
// characters is Twilio's and nobody else's, and so is 00, 13 characters and a `!`.
const (
	minAnchor      = 3
	minShortAnchor = 2
	minFixedLength = 16
)

// The penalties, each one a way the number of shapes grows past what one expression
// can cover. Named so the page can print the formula from the code that applies it.
const (
	penaltyShapeOnly  = 15 // nothing but the shape vouches: no anchor, no rule
	penaltyFreeText   = 25
	penaltyOpenLength = 10
	penaltyPerForm    = 5 // per unanchored form past the first
	capForms          = 30
	penaltyPerCountry = 5 // per country past the first
	capCountries      = 20
	penaltyNoisy      = 10
	floor             = 5
)

// TODO: the score reads the structure of the expressions, not what they miss. A
// pattern that is short but wrong scores as well as one that is short and right;
// the upgrade is to weigh it with the corpus's measured recall per category
// (score-baseline.json), once every category has enough cases to mean something.
// It also cannot tell a checksum that confirms a value (Luhn, the IBAN key) from a
// plausibility rule that only filters (GenericSecretCheck, DOBCheck): both are a
// Verify, and both earn the same exemption.
func (r *row) score() {
	s := 100
	if !r.Anchored && !r.Rule {
		s -= penaltyShapeOnly
	}
	switch {
	case r.FreeText:
		s -= penaltyFreeText
	case r.OpenLength:
		s -= penaltyOpenLength
	}
	s -= min(capForms, penaltyPerForm*max(0, r.Forms-1))
	s -= min(capCountries, penaltyPerCountry*max(0, len(r.Countries)-1))
	if r.Noisy {
		s -= penaltyNoisy
	}
	r.Score = max(floor, s)
}

// assess groups the patterns by category and scores each, hardest first.
func assess(patterns []pii.Pattern) []row {
	byCat := map[pii.Category][]pii.Pattern{}
	var order []pii.Category
	for _, p := range patterns {
		if _, seen := byCat[p.Category]; !seen {
			order = append(order, p.Category)
		}
		byCat[p.Category] = append(byCat[p.Category], p)
	}

	rows := make([]row, 0, len(order))
	for _, cat := range order {
		info, _ := pii.Info(cat)
		r := row{Code: string(cat), Label: pii.Label(cat), Secret: pii.IsSecret(cat),
			Anchored: true, Rule: info.Verify != nil, Noisy: pii.NoisyInCode(cat)}
		everyRule := true
		var unanchored []pii.Pattern
		for _, p := range byCat[cat] {
			if !slices.Contains(r.Notations, p.Label) {
				r.Notations = append(r.Notations, p.Label)
			}
			if p.Locale != "" && !slices.Contains(r.Countries, p.Locale) {
				r.Countries = append(r.Countries, p.Locale)
			}
			everyRule = everyRule && p.Verify != nil
			v := valueShape(p)
			if v.anchored {
				continue
			}
			r.Anchored = false
			unanchored = append(unanchored, p)
			r.FreeText = r.FreeText || (v.open && v.spaces)
			r.OpenLength = r.OpenLength || (v.open && !v.spaces)
		}
		r.Rule = r.Rule || everyRule
		r.Unanchored, r.Forms = forms(unanchored)
		r.score()
		rows = append(rows, r)
	}
	slices.SortStableFunc(rows, func(a, b row) int {
		return cmp.Or(cmp.Compare(a.Score, b.Score), strings.Compare(a.Label, b.Label))
	})
	return rows
}

// sample is the catalogue's own reference text, which must carry every notation
// each pattern accepts (TestSampleShowsEveryNotation): what forms tells notations
// apart by.
var sample = pii.Sample(pii.LocaleCodes())

// forms counts the notations, and the forms they come to. A notation is a form of
// its own unless every value it reads in the sample has the signature of a value
// another notation reads: then it is one more context for a form already read.
//
// Counting notations instead made coverage cost points. The AWS secret read beside
// its key id is the same forty characters its name introduces, and adding that
// second way to find it dropped the category from 85 to 80 — a report scoring the
// fix of a leak as a loss. A notation the sample never exercises is a form of its
// own: unknown is counted, not folded.
//
// TODO: the forms are read off the sample's values, not off the expressions, so two
// notations whose sample values happen to share a shape fold even where their
// expressions differ — the US date, month first, folds into the French one, day
// first, which is the same shape read two ways. The upgrade, if two genuinely
// different forms ever fold, is a shape derived from what each expression allows.
func forms(patterns []pii.Pattern) (notations, distinct int) {
	sigs := map[string]map[string]bool{}
	var labels []string
	for _, p := range patterns {
		if sigs[p.Label] == nil {
			sigs[p.Label] = map[string]bool{}
			labels = append(labels, p.Label)
		}
		for _, v := range sampleValues(p) {
			sigs[p.Label][signature(v)] = true
		}
	}
	within := func(a, b string) bool {
		if len(sigs[a]) == 0 {
			return false
		}
		for s := range sigs[a] {
			if !sigs[b][s] {
				return false
			}
		}
		return true
	}
	for i, a := range labels {
		folded := slices.ContainsFunc(labels, func(b string) bool {
			// Two notations reading exactly the same forms are one form, counted
			// on the first of them.
			return b != a && within(a, b) && (!within(b, a) || slices.Index(labels, b) < i)
		})
		if !folded {
			distinct++
		}
	}
	return len(labels), distinct
}

// sampleValues is what the pattern claims in the sample: its whole match, the group
// it points at, or the spans its Find returns. The IBAN's Refine is not applied; its
// value is anchored by its checksum and never reaches forms.
func sampleValues(p pii.Pattern) []string {
	var out []string
	if p.Find != nil {
		for _, sp := range p.Find(sample) {
			out = append(out, sample[sp[0]:sp[1]])
		}
		return out
	}
	for _, m := range p.Regex.FindAllStringSubmatchIndex(sample, -1) {
		if g := p.Group; m[2*g] >= 0 {
			out = append(out, sample[m[2*g]:m[2*g+1]])
		}
	}
	return out
}

// signature is a value's shape: letters as a, digits as 9, everything else kept,
// runs counted — `AB-123-CD` is a2-93-a2, an AWS secret a40.
func signature(v string) string {
	var b strings.Builder
	var last rune
	n := 0
	flush := func() {
		if n == 0 {
			return
		}
		b.WriteRune(last)
		if n > 1 {
			b.WriteString(strconv.Itoa(n))
		}
	}
	for _, c := range v {
		switch {
		case unicode.IsLetter(c):
			c = 'a'
		case unicode.IsDigit(c):
			c = '9'
		}
		if c == last {
			n++
			continue
		}
		flush()
		last, n = c, 1
	}
	flush()
	return b.String()
}

// matrix is the country half of the report: what each locale finds, and how well,
// scored on that locale's patterns alone. The same category scores differently per
// country — one French plate is one notation, every European plate together is
// forty — which is the question a deployment choosing its locales is asking.
type matrix struct {
	Columns  []column
	Rows     []matrixRow
	Everyone []string // labels of the personal data found whatever the locale
}

type column struct{ Code, Label string }

type matrixRow struct {
	Name, Note string
	Cells      []int // a score per column, 0 where the locale finds nothing
}

func countryMatrix() matrix {
	var m matrix
	local := map[pii.Category]bool{}
	for _, p := range pii.LocalePatterns(pii.LocaleCodes()) {
		if !local[p.Category] {
			local[p.Category] = true
			m.Columns = append(m.Columns, column{string(p.Category), pii.Label(p.Category)})
		}
	}
	for _, p := range pii.InternationalPatterns() {
		if l := pii.Label(p.Category); !local[p.Category] && !pii.IsSecret(p.Category) && !slices.Contains(m.Everyone, l) {
			m.Everyone = append(m.Everyone, l)
		}
	}

	row := func(name, note string, codes []string) matrixRow {
		scores := map[string]int{}
		for _, r := range assess(pii.LocalePatterns(codes)) {
			scores[r.Code] = r.Score
		}
		cells := make([]int, len(m.Columns))
		for i, c := range m.Columns {
			cells[i] = scores[c.Code]
		}
		return matrixRow{Name: name, Note: note, Cells: cells}
	}
	eu := pii.EuropeanLocales()
	m.Rows = append(m.Rows, row(pii.LocaleEurope, fmt.Sprintf("all %d European locales together", len(eu)), eu))
	for _, code := range pii.LocaleCodes() {
		m.Rows = append(m.Rows, row(code, "", []string{code}))
	}
	return m
}

type shape struct{ anchored, open, spaces bool }

// valueShape reads the part of a pattern's expression that is the value. A pattern
// with a Group matches context around it — `password=` before a generic secret — and
// a literal there anchors the match, not the value: counted, it scored the hardest
// category in the catalogue as one of the easiest.
//
// A pattern with Find instead of an expression (the licence plates) has no structure
// to read and is taken as an unanchored, bounded value.
func valueShape(p pii.Pattern) shape {
	if p.Regex == nil {
		return shape{}
	}
	re, err := syntax.Parse(p.Regex.String(), syntax.Perl)
	if err != nil {
		return shape{}
	}
	value := re
	if p.Group > 0 {
		if g := capture(re, p.Group); g != nil {
			value = g
		}
	}
	value = value.Simplify()
	var s shape
	walk(value, &s)
	best := fixedRun(value).best
	s.anchored = best >= minAnchor || (best >= minShortAnchor && !s.open && minLength(value) >= minFixedLength)
	return s
}

// minLength is the fewest characters a match of the node can have.
func minLength(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune)
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return 1
	case syntax.OpCapture, syntax.OpPlus:
		return minLength(re.Sub[0])
	case syntax.OpRepeat:
		return re.Min * minLength(re.Sub[0])
	case syntax.OpConcat:
		n := 0
		for _, sub := range re.Sub {
			n += minLength(sub)
		}
		return n
	case syntax.OpAlternate:
		n := minLength(re.Sub[0])
		for _, sub := range re.Sub[1:] {
			n = min(n, minLength(sub))
		}
		return n
	}
	return 0
}

func capture(re *syntax.Regexp, n int) *syntax.Regexp {
	if re.Op == syntax.OpCapture && re.Cap == n {
		return re
	}
	for _, sub := range re.Sub {
		if g := capture(sub, n); g != nil {
			return g
		}
	}
	return nil
}

// run measures, for one node, the stretches of fixed positions every match must
// contain — a fixed position being a literal character or a class of a few. A
// prefix is a run and not a literal because the parser factors and classes break
// literals up: gh[pousr]_ is four fixed positions holding no literal longer than two,
// and eyJ|ewo is parsed as e(?:yJ|wo). Counting literals scored both as unanchored.
type run struct {
	full     bool // every position of the node is fixed
	n        int  // its length, when full
	pre, suf int  // the fixed positions it is guaranteed to open and close on
	best     int  // the longest stretch it is guaranteed to contain
}

// smallClass is how many characters a class may allow and still count as fixed:
// [pousr] names a vendor's token kinds, [a-f0-9] is the value.
const smallClass = 6

// A space is value, not prefix, wherever it is written — the one after a street type
// made addresses look anchored. A digit is value only where the pattern offers a
// choice of them: [01] is a month and 19|20 a century, but the 8 in r8_ and the 1 in
// doo_v1_ are a vendor's.
func breaksRun(c rune) bool { return unicode.IsSpace(c) }

// digitsOnly is a node that can only match digits: 19, [12]\d, 0[1-9]. An alternation
// with such a branch is a choice of numbers, not a prefix. Any digit at all was the
// first rule, and the 3 in AWS's A3T took AKIA|ASIA down with it.
func digitsOnly(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpLiteral, syntax.OpCharClass:
		return len(re.Rune) > 0 && !slices.ContainsFunc(re.Rune, func(c rune) bool { return c < '0' || c > '9' })
	case syntax.OpConcat, syntax.OpCapture, syntax.OpRepeat, syntax.OpPlus, syntax.OpStar, syntax.OpQuest:
		return len(re.Sub) > 0 && !slices.ContainsFunc(re.Sub, func(sub *syntax.Regexp) bool { return !digitsOnly(sub) })
	}
	return false
}

func fixedRun(re *syntax.Regexp) run {
	fixed := func(n int) run { return run{full: true, n: n, pre: n, suf: n, best: n} }
	switch re.Op {
	case syntax.OpLiteral:
		out := fixed(0)
		for _, c := range re.Rune {
			if breaksRun(c) {
				out.full, out.suf = false, 0
				continue
			}
			if out.full {
				out.pre++
				out.n++
			}
			out.suf++
			out.best = max(out.best, out.suf)
		}
		return out
	case syntax.OpCharClass:
		size := 0
		for i := 0; i < len(re.Rune); i += 2 {
			size += int(re.Rune[i+1]-re.Rune[i]) + 1
			for c := re.Rune[i]; c <= re.Rune[i+1] && size <= smallClass; c++ {
				if breaksRun(c) || unicode.IsDigit(c) {
					return run{}
				}
			}
		}
		if size <= smallClass {
			return fixed(1)
		}
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText,
		syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return fixed(0)
	case syntax.OpCapture:
		return fixedRun(re.Sub[0])
	case syntax.OpPlus:
		r := fixedRun(re.Sub[0])
		return run{pre: r.pre, suf: r.suf, best: r.best}
	case syntax.OpRepeat:
		if re.Min >= 1 {
			r := fixedRun(re.Sub[0])
			return run{pre: r.pre, suf: r.suf, best: r.best}
		}
	case syntax.OpConcat:
		out := fixed(0)
		for _, sub := range re.Sub {
			r := fixedRun(sub)
			out.best = max(out.best, r.best, out.suf+r.pre)
			if out.full {
				out.pre += r.pre
			}
			if r.full {
				out.suf += r.n
				out.n += r.n
			} else {
				out.full, out.suf = false, r.suf
			}
		}
		return out
	case syntax.OpAlternate:
		// Only as anchored as the least anchored branch: xox[bpa]- keeps its
		// prefix, a list of street types keeps nothing.
		if slices.ContainsFunc(re.Sub, digitsOnly) {
			return run{}
		}
		out := fixedRun(re.Sub[0])
		for _, sub := range re.Sub[1:] {
			r := fixedRun(sub)
			out = run{full: out.full && r.full && out.n == r.n, n: min(out.n, r.n),
				pre: min(out.pre, r.pre), suf: min(out.suf, r.suf), best: min(out.best, r.best)}
		}
		return out
	}
	return run{}
}

// walk notes an open length (a star, a plus, an unbounded repeat) and a span that can
// cross a space. A negated class reaching the end of Unicode also holds a space, but
// it is how an expression says "up to the delimiter", not "across words": counted, it
// made an email address free text.
func walk(re *syntax.Regexp, s *shape) {
	switch re.Op {
	case syntax.OpStar, syntax.OpPlus:
		s.open = true
	case syntax.OpRepeat:
		s.open = s.open || re.Max == -1
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		s.spaces = true
	case syntax.OpCharClass:
		if n := len(re.Rune); n > 0 && re.Rune[n-1] < 0x10FFFF {
			for i := 0; i < n; i += 2 {
				if re.Rune[i] <= ' ' && re.Rune[i+1] >= ' ' {
					s.spaces = true
				}
			}
		}
	}
	for _, sub := range re.Sub {
		walk(sub, s)
	}
}
