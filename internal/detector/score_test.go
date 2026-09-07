package detector

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// The score baseline counts the corpus per category and commits the result as a
// floor.
//
// It exists because the suites' percentage floors cannot see the failure that
// matters most: a category nothing measures. An empty tally scores 100%
// precision and 100% recall by construction, so a category with no case at all
// is indistinguishable from one that is genuinely perfect. Half of Agent Veil's
// catalogue sat in exactly that state — 21 of 41 categories exercised, every
// suite still reporting 100% / 100%.
//
// Counts, never percentages. A ratio moves for two unrelated reasons — the
// detector changed, or the corpus did — and the diff cannot tell them apart. A
// line going from "tp": 3 to "tp": 0 says which, and cannot be edited down
// without saying so in the diff.
//
// The numbers are a floor to ratchet, never a goal, and some may record bugs: an
// fn above zero is a miss we accept today, written down so it is noticed rather
// than rediscovered by an incident.
//
//	make score          report and gate
//	make score-update   rewrite the floor — then explain the delta in the PR
const scoreBaselinePath = "testdata/score-baseline.json"

var updateScore = flag.Bool("update-score", false,
	"rewrite "+scoreBaselinePath+" from the current run instead of gating against it")

// categoryScore is one category's committed floor.
type categoryScore struct {
	TP int `json:"tp"`
	FP int `json:"fp"`
	FN int `json:"fn"`
}

type scoreBaseline struct {
	Comment string                   `json:"_comment"`
	Checks  map[string]categoryScore `json:"checks"`

	// Negatives counts the cases that expect nothing at all.
	//
	// Without it the gate is one-sided. A category's floor is carried by its tp,
	// and a precision case contributes none: deleting every `expect: []` case
	// leaves tp unchanged, fp at zero and the run green, while the catalogue's
	// false-positive rate stops being measured entirely. That is the same loss
	// of measurement the per-category floor exists to catch, on the side the
	// counts cannot see.
	Negatives int `json:"negatives"`
}

const scoreBaselineComment = "Per-category floor for `make score`. Counts, not percentages. " +
	"Regenerate with `make score-update` and explain the delta in the PR body. " +
	"An fn above zero records a miss we accept today, so a baseline is a floor to ratchet, never a goal."

func TestScoreCorpus(t *testing.T) {
	live, negatives := scoreCorpus(t)
	required := patternedCategories()

	if *updateScore {
		writeScoreBaseline(t, live, negatives)
		t.Logf("rewrote %s from this run", scoreBaselinePath)
	}

	base := loadScoreBaseline(t)
	t.Log("\n" + scoreReport(live, base, required))
	t.Logf("negative cases: %d (floor %d)", negatives, base.Negatives)

	for _, v := range compareScore(base.Checks, live, required) {
		t.Error(v)
	}
	if negatives < base.Negatives {
		t.Errorf("negative cases fell from %d to %d — precision stops being measured "+
			"without a single count moving, which is what this floor exists to catch",
			base.Negatives, negatives)
	}
}

// scoreCorpus runs every suite under its own locale and totals the result per
// category. The suites are counted together because a category belongs to a
// locale, not to a file: the NIR is only reachable with "fr" on, the CCCD only
// with "vn".
func scoreCorpus(t *testing.T) (map[string]tally, int) {
	t.Helper()

	live := map[string]tally{}
	negatives := 0
	add := func(category string, f func(*tally)) {
		c := live[category]
		f(&c)
		live[category] = c
	}

	for _, path := range corpusFiles(t) {
		suite := loadCorpus(t, path)
		d := detectorFor(t, suite.Locale)

		for _, c := range suite.Cases {
			// Non-gating cases count here on purpose. A known gap is a miss we
			// accept, and the floor is where an accepted miss belongs: recorded
			// as an fn that must not grow, rather than dropped and forgotten.
			want, got := caseSpans(d, c)
			if len(c.Expect) == 0 {
				negatives++
			}

			for s, n := range want {
				hit := min(n, got[s])
				add(s.category, func(t *tally) { t.tp += hit; t.fn += n - hit })
			}
			for s, n := range got {
				if extra := n - want[s]; extra > 0 {
					add(s.category, func(t *tally) { t.fp += extra })
				}
			}
		}
	}
	return live, negatives
}

// patternedCategories returns every category the catalogue can actually emit.
//
// Derived from the catalogue rather than listed here: a category added to
// pkg/pii with no corpus case behind it must fail this gate on the commit that
// adds it, which a hand-maintained list would not do. The category registry is
// the wrong source — it also holds CUSTOM, which comes from configuration
// rather than from a regex, so requiring a case for it would require a case
// nobody can write.
func patternedCategories() []string {
	seen := map[string]bool{}
	for _, p := range pii.AllPatterns() {
		seen[string(p.Category)] = true
	}

	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// knownCategory reports whether the corpus names a category the engine can
// emit. The registry is the catalogue's own list, so a renamed category breaks
// the corpus loudly instead of turning its cases into silent misses.
func knownCategory(name string) bool {
	_, ok := pii.Info(pii.Category(name))
	return ok
}

// compareScore reports every way the current run falls below the committed
// floor. A pure function of its three inputs, so it can be tested on its own: a
// comparator that quietly accepts everything produces the same empty output as
// a run with nothing wrong in it.
func compareScore(base map[string]categoryScore, live map[string]tally, required []string) []string {
	var violations []string

	for _, cat := range required {
		l, measured := live[cat]
		if !measured || l.tp+l.fn == 0 {
			violations = append(violations, fmt.Sprintf(
				"%s: no corpus case exercises it — it scores 100%%/100%% by vacuity, which is what "+
					"this gate exists to catch; add a case under testdata/corpus/", cat))
			continue
		}

		b, known := base[cat]
		if !known {
			violations = append(violations, fmt.Sprintf(
				"%s: measured (tp=%d fp=%d fn=%d) but absent from %s — run `make score-update`",
				cat, l.tp, l.fp, l.fn, scoreBaselinePath))
			continue
		}

		if l.tp < b.TP {
			violations = append(violations, fmt.Sprintf(
				"%s: tp fell from %d to %d — either detection broke, or the cases that measured it were removed",
				cat, b.TP, l.tp))
		}
		if l.fp > b.FP {
			violations = append(violations, fmt.Sprintf(
				"%s: fp rose from %d to %d — the catalogue now flags something it did not", cat, b.FP, l.fp))
		}
		if l.fn > b.FN {
			violations = append(violations, fmt.Sprintf(
				"%s: fn rose from %d to %d — a value the corpus declares is no longer found", cat, b.FN, l.fn))
		}
	}

	// A category scored but no longer required is one the catalogue stopped
	// emitting. That is a real change and the floor should not keep asserting
	// it, so it is reported rather than silently carried.
	req := map[string]bool{}
	for _, c := range required {
		req[c] = true
	}
	for cat := range base {
		if !req[cat] {
			violations = append(violations, fmt.Sprintf(
				"%s: in %s but no pattern emits it any more — run `make score-update`", cat, scoreBaselinePath))
		}
	}

	sort.Strings(violations)
	return violations
}

func loadScoreBaseline(t *testing.T) scoreBaseline {
	t.Helper()

	raw, err := os.ReadFile(scoreBaselinePath)
	if err != nil {
		t.Fatalf("read %s: %v (run `make score-update` to create it)", scoreBaselinePath, err)
	}

	var base scoreBaseline
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields() // a key we do not model is a typo, not an extension
	if err := dec.Decode(&base); err != nil {
		t.Fatalf("parse %s: %v", scoreBaselinePath, err)
	}
	if len(base.Checks) == 0 {
		t.Fatalf("%s declares no checks — a floor of nothing gates nothing", scoreBaselinePath)
	}
	if base.Negatives == 0 {
		t.Fatalf("%s declares no negative cases — precision would be ungated", scoreBaselinePath)
	}
	return base
}

func writeScoreBaseline(t *testing.T, live map[string]tally, negatives int) {
	t.Helper()

	base := scoreBaseline{Comment: scoreBaselineComment, Checks: map[string]categoryScore{}, Negatives: negatives}
	for cat, l := range live {
		base.Checks[cat] = categoryScore{TP: l.tp, FP: l.fp, FN: l.fn}
	}

	raw, err := json.MarshalIndent(base, "", " ")
	if err != nil {
		t.Fatalf("encode baseline: %v", err)
	}
	if err := os.WriteFile(filepath.Clean(scoreBaselinePath), append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", scoreBaselinePath, err)
	}
}

// scoreReport is the table `make score` exists to show: one line per category
// the catalogue can emit, so an unmeasured one appears as a row of zeroes rather
// than as an absence nobody reads.
func scoreReport(live map[string]tally, base scoreBaseline, required []string) string {
	out := fmt.Sprintf("%-24s %5s %4s %4s   %s\n", "CATEGORY", "TP", "FP", "FN", "VS FLOOR")

	var total tally
	for _, cat := range required {
		l := live[cat]
		total.tp += l.tp
		total.fp += l.fp
		total.fn += l.fn

		note := ""
		switch b, known := base.Checks[cat]; {
		case l.tp+l.fn == 0:
			note = "UNMEASURED"
		case !known:
			note = "new"
		case l.tp > b.TP || l.fp < b.FP || l.fn < b.FN:
			note = fmt.Sprintf("improved on tp=%d fp=%d fn=%d — ratchet it", b.TP, b.FP, b.FN)
		}
		out += fmt.Sprintf("%-24s %5d %4d %4d   %s\n", cat, l.tp, l.fp, l.fn, note)
	}
	out += fmt.Sprintf("%-24s %5d %4d %4d", "TOTAL", total.tp, total.fp, total.fn)
	return out
}

// compareScore is the whole gate, so it gets its own table: a comparator that
// quietly accepts everything produces the same empty output as a clean run, and
// nothing else in the suite would tell them apart.
func TestCompareScore(t *testing.T) {
	const cat = "EMAIL"
	required := []string{cat}
	floor := map[string]categoryScore{cat: {TP: 3, FP: 1, FN: 2}}

	tests := []struct {
		name     string
		base     map[string]categoryScore
		live     map[string]tally
		required []string
		want     string // substring the violation must carry; "" means none expected
	}{
		{
			name: "holding the floor exactly is not a violation",
			base: floor, required: required,
			live: map[string]tally{cat: {tp: 3, fp: 1, fn: 2}},
		},
		{
			// A floor is a floor, not an equality: beating it must not fail the
			// run, or nobody could ever improve the detector.
			name: "beating the floor is not a violation",
			base: floor, required: required,
			live: map[string]tally{cat: {tp: 5, fp: 0, fn: 0}},
		},
		{
			// The failure this file exists for: an empty tally scores 100%
			// precision and 100% recall, so only an explicit check sees it.
			name: "a required category with no case at all",
			base: floor, required: required,
			live: map[string]tally{},
			want: "no corpus case exercises it",
		},
		{
			// tp=0 with misses is a broken detector, not an unmeasured
			// category — the two need different messages, or the reader chases
			// the wrong bug.
			name: "measured but entirely missed is not reported as unmeasured",
			base: floor, required: required,
			live: map[string]tally{cat: {tp: 0, fn: 3}},
			want: "tp fell from 3 to 0",
		},
		{
			name: "losing true positives",
			base: floor, required: required,
			live: map[string]tally{cat: {tp: 2, fp: 1, fn: 2}},
			want: "tp fell from 3 to 2",
		},
		{
			name: "gaining false positives",
			base: floor, required: required,
			live: map[string]tally{cat: {tp: 3, fp: 4, fn: 2}},
			want: "fp rose from 1 to 4",
		},
		{
			name: "gaining misses",
			base: floor, required: required,
			live: map[string]tally{cat: {tp: 3, fp: 1, fn: 5}},
			want: "fn rose from 2 to 5",
		},
		{
			name: "a category measured but not yet in the floor",
			base: map[string]categoryScore{}, required: required,
			live: map[string]tally{cat: {tp: 3}},
			want: "absent from",
		},
		{
			// A floor that keeps asserting a category no pattern emits is a
			// floor nobody can satisfy: report it, do not carry it.
			name: "a floor for a category the catalogue no longer emits",
			base: map[string]categoryScore{"GONE": {TP: 1}}, required: required,
			live: map[string]tally{cat: {tp: 3, fp: 1, fn: 2}},
			want: "no pattern emits it any more",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareScore(tt.base, tt.live, tt.required)

			if tt.want == "" {
				if len(got) > 0 {
					t.Fatalf("expected no violation, got %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatal("the comparator accepted a run that fell below the floor")
			}
			if !strings.Contains(strings.Join(got, "\n"), tt.want) {
				t.Errorf("violations %v do not mention %q", got, tt.want)
			}
		})
	}
}

// patternedCategories decides which categories the gate demands a case for, so
// an error in it silently narrows the gate to whatever it happens to return.
func TestPatternedCategories(t *testing.T) {
	cats := patternedCategories()
	if len(cats) == 0 {
		t.Fatal("no categories derived from the catalogue — the gate would demand nothing")
	}

	got := map[string]bool{}
	for _, c := range cats {
		got[c] = true
	}

	// CUSTOM is registered but comes from configuration rather than from a
	// regex. Demanding a corpus case for it would demand one nobody can write.
	if got[string(pii.CatCustom)] {
		t.Errorf("%s has no pattern, so the gate must not require a case for it", pii.CatCustom)
	}

	// Every category derived here must be one the corpus is allowed to name, or
	// a case written against it fails the corpus validator instead.
	for _, c := range cats {
		if !knownCategory(c) {
			t.Errorf("%s is emitted by a pattern but is not in the category registry", c)
		}
	}
}
