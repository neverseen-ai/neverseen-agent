package detector

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The corpus measures what the catalogue actually finds, per category, on text
// that reads like prose rather than a lone identifier.
//
// It exists because a unit test answers "does this expression match this
// string?" and never "how much do we miss, and how much do we invent?". Each
// suite declares the floors it holds; a change that drops below one fails the
// run.
//
// Individual misses and false positives are logged rather than failed — a run
// that stopped at the first one would never reach the percentages the suite
// exists to report.
//
// This corpus is inherited from Agent Veil (MIT, see NOTICE) and is the
// specification the engine in this repository was written against: the code is
// independent, the behaviour it has to reproduce is not.
//
//	make bench-accuracy   the per-category report
//	make score            the per-category floor
type corpusFile struct {
	// Locale is the selection this suite runs under, spelled as the
	// configuration spells it.
	Locale string `yaml:"locale"`

	// The floors this suite holds, in percent. Pointers so a file that omits
	// them is rejected rather than silently gated at zero.
	MinPrecision *float64 `yaml:"min_precision"`
	MinRecall    *float64 `yaml:"min_recall"`

	Cases []corpusCase `yaml:"cases"`
}

type corpusCase struct {
	ID   string `yaml:"id"`
	Text string `yaml:"text"`

	// Gate reports whether this case counts towards the floors. A pointer, so an
	// omitted field means "gating" and an explicit `gate: false` opts out.
	Gate *bool `yaml:"gate"`

	// Note records why a non-gating case is not gating. Required for those, so a
	// known gap cannot be parked here without saying what it is.
	Note string `yaml:"note"`

	// Expect lists every span that must be found. An empty list makes the case a
	// precision case: nothing at all may be flagged.
	Expect []corpusSpan `yaml:"expect"`
}

func (c corpusCase) gating() bool { return c.Gate == nil || *c.Gate }

type corpusSpan struct {
	Category string `yaml:"category"`
	Value    string `yaml:"value"`
}

// span is a (category, value) pair, counted as a multiset so a case can expect
// the same value twice.
type span struct{ category, value string }

type tally struct{ tp, fp, fn int }

func (t tally) precision() float64 {
	if t.tp+t.fp == 0 {
		return 100
	}
	return 100 * float64(t.tp) / float64(t.tp+t.fp)
}

func (t tally) recall() float64 {
	if t.tp+t.fn == 0 {
		return 100
	}
	return 100 * float64(t.tp) / float64(t.tp+t.fn)
}

func TestAccuracyCorpus(t *testing.T) {
	for _, path := range corpusFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			suite := loadCorpus(t, path)
			d := detectorFor(t, suite.Locale)

			// gated is what the floors are checked against; overall also counts
			// the cases that opted out, so the report still shows them.
			var gated, overall tally
			byCategory := map[string]*tally{}
			bump := func(category string, f func(*tally)) {
				if byCategory[category] == nil {
					byCategory[category] = &tally{}
				}
				f(byCategory[category])
			}

			for _, c := range suite.Cases {
				want, got := caseSpans(d, c)

				for s, n := range want {
					hit := min(n, got[s])
					overall.tp += hit
					overall.fn += n - hit
					bump(s.category, func(t *tally) { t.tp += hit; t.fn += n - hit })
					if c.gating() {
						gated.tp += hit
						gated.fn += n - hit
					}
					if hit < n {
						t.Logf("%s%s: missed %s %q", c.ID, gateSuffix(c), s.category, s.value)
					}
				}

				for s, n := range got {
					extra := n - want[s]
					if extra <= 0 {
						continue
					}
					overall.fp += extra
					bump(s.category, func(t *tally) { t.fp += extra })
					if c.gating() {
						gated.fp += extra
					}
					t.Logf("%s%s: false positive %s %q", c.ID, gateSuffix(c), s.category, s.value)
				}

				// A non-gating case that has started passing is a gap that
				// closed: say so, so the flag is removed rather than left to rot.
				if !c.gating() && caseHolds(want, got) {
					t.Logf("%s now passes — drop its `gate: false` and its note", c.ID)
				}
			}

			t.Log("\n" + accuracyReport(byCategory, overall))
			if gated != overall {
				t.Logf("gated subset (the floors apply to this): tp=%d fp=%d fn=%d precision=%.1f%% recall=%.1f%%",
					gated.tp, gated.fp, gated.fn, gated.precision(), gated.recall())
			}

			if p := gated.precision(); p < *suite.MinPrecision {
				t.Errorf("precision %.1f%% is below the %.1f%% floor this suite holds", p, *suite.MinPrecision)
			}
			if r := gated.recall(); r < *suite.MinRecall {
				t.Errorf("recall %.1f%% is below the %.1f%% floor this suite holds", r, *suite.MinRecall)
			}
		})
	}
}

// caseSpans returns what a case declares and what the detector finds, as
// multisets.
//
// Both the percentage floors and the per-category baseline count from this one
// function, so the two gates can never disagree about what a case contains — a
// corpus that scored differently depending on which gate read it would make one
// of them a fiction.
func caseSpans(d *Detector, c corpusCase) (want, got map[span]int) {
	want = map[span]int{}
	for _, e := range c.Expect {
		want[span{e.Category, e.Value}]++
	}

	got = map[span]int{}
	for _, m := range d.Scan(c.Text) {
		got[span{string(m.Category), m.Value}]++
	}
	return want, got
}

func corpusFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/corpus/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus files found: %v", err)
	}
	return files
}

func detectorFor(t *testing.T, locale string) *Detector {
	t.Helper()
	locales, err := ParseLocales(locale)
	if err != nil {
		t.Fatalf("locale %q: %v", locale, err)
	}
	return New(Config{Locales: locales})
}

func loadCorpus(t *testing.T, path string) corpusFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	suite, problems := parseCorpus(raw)
	if len(problems) > 0 {
		t.Fatalf("invalid corpus %s:\n  - %s", path, strings.Join(problems, "\n  - "))
	}
	return suite
}

// parseCorpus decodes and validates one corpus file, returning every problem it
// finds rather than the first.
//
// The validation is the point. A mistyped key that YAML silently drops, or an
// expected value that does not occur in its own case text, turns a suite green
// while it measures nothing — the failure a corpus exists to prevent.
func parseCorpus(raw []byte) (corpusFile, []string) {
	var suite corpusFile

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true) // a key we do not model is a typo, not an extension
	if err := dec.Decode(&suite); err != nil {
		return suite, []string{fmt.Sprintf("parse: %v", err)}
	}

	var problems []string
	if len(suite.Cases) == 0 {
		problems = append(problems, "declares no cases")
	}
	if suite.MinPrecision == nil || suite.MinRecall == nil {
		problems = append(problems, "must declare min_precision and min_recall — the floors are the gate")
	}

	seen := map[string]bool{}
	for i, c := range suite.Cases {
		where := c.ID
		if where == "" {
			where = fmt.Sprintf("case #%d", i+1)
			problems = append(problems, where+": has no id")
		}
		if c.ID != "" && seen[c.ID] {
			problems = append(problems, where+": duplicate case id")
		}
		seen[c.ID] = true

		if strings.TrimSpace(c.Text) == "" {
			problems = append(problems, where+": has no text")
		}
		if !c.gating() && strings.TrimSpace(c.Note) == "" {
			problems = append(problems, where+": `gate: false` requires a note saying which gap it records")
		}

		for _, e := range c.Expect {
			if e.Value == "" {
				problems = append(problems, fmt.Sprintf("%s: expects %s with an empty value", where, e.Category))
				continue
			}
			// The check that catches most corpus mistakes: a span that is not a
			// substring of its own text can never be produced, so the case is a
			// permanent, meaningless miss.
			if !strings.Contains(c.Text, e.Value) {
				problems = append(problems, fmt.Sprintf(
					"%s: expected %s value %q does not occur in the case text", where, e.Category, e.Value))
			}
			if !knownCategory(e.Category) {
				problems = append(problems, fmt.Sprintf("%s: expects unknown category %q", where, e.Category))
			}
		}
	}
	return suite, problems
}

// caseHolds reports whether a case's detections match its expectations exactly.
func caseHolds(want, got map[span]int) bool {
	if len(want) != len(got) {
		return false
	}
	for s, n := range want {
		if got[s] != n {
			return false
		}
	}
	return true
}

func gateSuffix(c corpusCase) string {
	if c.gating() {
		return ""
	}
	return " [known gap]"
}

func accuracyReport(byCategory map[string]*tally, overall tally) string {
	names := make([]string, 0, len(byCategory))
	for name := range byCategory {
		names = append(names, name)
	}
	sort.Strings(names)

	out := fmt.Sprintf("%-24s %5s %4s %4s %10s %8s\n", "CATEGORY", "TP", "FP", "FN", "PRECISION", "RECALL")
	for _, name := range names {
		c := byCategory[name]
		out += fmt.Sprintf("%-24s %5d %4d %4d %9.1f%% %7.1f%%\n", name, c.tp, c.fp, c.fn, c.precision(), c.recall())
	}
	out += fmt.Sprintf("%-24s %5d %4d %4d %9.1f%% %7.1f%%", "TOTAL",
		overall.tp, overall.fp, overall.fn, overall.precision(), overall.recall())
	return out
}

// The validator is the only thing standing between a typo and a suite that
// reports 100% while measuring nothing, so it gets its own table: one that
// accepts everything produces the same empty output as a corpus with nothing
// wrong in it.
func TestParseCorpus_RejectsBadCorpora(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string // substring the reported problem must carry
	}{
		{
			// The mistake a schema-less loader cannot see: YAML drops a key
			// nothing maps to, so "expects" becomes no expectation at all and
			// the case passes for the wrong reason.
			name: "unknown field",
			yaml: `
locale: none
min_precision: 100
min_recall: 100
cases:
  - id: typo
    text: "mail claire@example.com"
    expects:
      - {category: EMAIL, value: "claire@example.com"}
`,
			want: "field expects not found",
		},
		{
			name: "expected value absent from the text",
			yaml: `
locale: none
min_precision: 100
min_recall: 100
cases:
  - id: ghost
    text: "mail claire@example.com"
    expect:
      - {category: EMAIL, value: "paul@example.com"}
`,
			want: "does not occur in the case text",
		},
		{
			name: "unknown category",
			yaml: `
locale: none
min_precision: 100
min_recall: 100
cases:
  - id: bad-cat
    text: "mail claire@example.com"
    expect:
      - {category: E_MAIL, value: "claire@example.com"}
`,
			want: "unknown category",
		},
		{
			name: "missing floors",
			yaml: `
locale: none
cases:
  - id: ok
    text: "mail claire@example.com"
    expect: []
`,
			want: "must declare min_precision and min_recall",
		},
		{
			name: "duplicate case id",
			yaml: `
locale: none
min_precision: 100
min_recall: 100
cases:
  - id: dup
    text: "un"
    expect: []
  - id: dup
    text: "deux"
    expect: []
`,
			want: "duplicate case id",
		},
		{
			name: "case without id",
			yaml: `
locale: none
min_precision: 100
min_recall: 100
cases:
  - text: "sans id"
    expect: []
`,
			want: "has no id",
		},
		{
			name: "empty text",
			yaml: `
locale: none
min_precision: 100
min_recall: 100
cases:
  - id: vide
    text: "   "
    expect: []
`,
			want: "has no text",
		},
		{
			// A known gap parked without an explanation rots: nobody can later
			// tell whether it is still a gap or a case someone gave up on.
			name: "known gap without a note",
			yaml: `
locale: none
min_precision: 100
min_recall: 100
cases:
  - id: gap
    text: "mail claire@example.com"
    gate: false
    expect: []
`,
			want: "requires a note",
		},
		{
			name: "no cases at all",
			yaml: `
locale: none
min_precision: 100
min_recall: 100
cases: []
`,
			want: "declares no cases",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, problems := parseCorpus([]byte(tt.yaml))
			if len(problems) == 0 {
				t.Fatal("the validator accepted an invalid corpus")
			}
			if !strings.Contains(strings.Join(problems, "\n"), tt.want) {
				t.Errorf("problems %q do not mention %q", problems, tt.want)
			}
		})
	}

	t.Run("a valid corpus is accepted", func(t *testing.T) {
		suite, problems := parseCorpus([]byte(`
locale: none
min_precision: 100
min_recall: 100
cases:
  - id: ok
    text: "mail claire@example.com"
    expect:
      - {category: EMAIL, value: "claire@example.com"}
`))
		if len(problems) > 0 {
			t.Fatalf("valid corpus rejected: %v", problems)
		}
		if len(suite.Cases) != 1 || !suite.Cases[0].gating() {
			t.Errorf("a case with no gate field must default to gating: %+v", suite.Cases)
		}
	})

	t.Run("a known gap with a note is accepted and is not gating", func(t *testing.T) {
		suite, problems := parseCorpus([]byte(`
locale: none
min_precision: 100
min_recall: 100
cases:
  - id: gap
    text: "mail claire@example.com"
    gate: false
    note: "records a shape the catalogue cannot reach yet"
    expect: []
`))
		if len(problems) > 0 {
			t.Fatalf("rejected: %v", problems)
		}
		if suite.Cases[0].gating() {
			t.Error("`gate: false` did not opt the case out of the floors")
		}
	})
}
