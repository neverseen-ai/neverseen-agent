package detector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

// Adding a locale is one entry in the registry plus a pattern file — and then
// three things that are easy to forget and silent when forgotten: a corpus
// suite, a line in the documented configuration, and a regenerated score floor.
//
// These tests fail on the commit that forgets any of them. Without the first,
// the locale's categories are measured by nothing and score 100% by vacuity.
// Without the second, an operator cannot discover the locale exists.

func TestEveryLocaleHasACorpusSuite(t *testing.T) {
	declared := map[string]string{} // locale code -> the file declaring it

	for _, path := range corpusFiles(t) {
		suite := loadCorpus(t, path)
		for _, code := range strings.Split(suite.Locale, ",") {
			if code = strings.TrimSpace(code); code != "" && code != "none" {
				declared[code] = filepath.Base(path)
			}
		}
	}

	for _, l := range pii.Locales() {
		if file, ok := declared[l.Code]; !ok {
			t.Errorf("locale %q has no corpus suite: every category it owns would be "+
				"measured by nothing and score 100%% by vacuity — add testdata/corpus/%s.yaml "+
				"declaring `locale: %s`, then run `make score-update`", l.Code, l.Code, l.Code)
		} else {
			t.Logf("locale %q is measured by %s", l.Code, file)
		}
	}
}

// The documented configuration is what an operator reads to find out a locale
// exists. One that is not named there is one nobody will ever select.
func TestEveryLocaleIsDocumented(t *testing.T) {
	const path = "../../.env.example"

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := string(raw)

	for _, l := range pii.Locales() {
		// Matched as a whole word at the start of its description line, so a
		// locale code appearing by accident inside a sentence does not count as
		// documentation of it.
		if !strings.Contains(doc, "\n#   "+l.Code+" ") {
			t.Errorf("locale %q is not documented in %s: add a line describing what it detects",
				l.Code, path)
		}
	}
}

// The other half of that pact — every documented variable being one some code
// reads — needs to see the whole binary's settings at once, so it lives with the
// command rather than here.

// A locale that reads dates has to read them in the notation its country writes.
//
// This is the test that would have caught the gap it was written for: a UK
// deployment masked the NHS number, the National Insurance number, the postcode,
// the address and the telephone, and forwarded "14/03/1987" in clear, because the
// day-first pattern lived in the French set alone.
//
// Nothing else could have. The sample sweep and the corpus both check that what a
// locale *does* detect is documented and measured, and neither can ask about a
// shape no pattern claims — an absent category is absent from the expectations
// too. So the question is asked here, from the outside: for each locale, a date
// written the way that country writes it must be recognised.
func TestEveryLocaleReadsItsOwnDateNotation(t *testing.T) {
	for _, tc := range []struct{ locale, date string }{
		// Day first in both, and the same notation: the United Kingdom writes a
		// date the way France does.
		{"fr", "23/02/2004"},
		{"gb", "14/03/1987"},
		// Month first, which is this locale's reading and nobody else's.
		{"us", "03/14/1987"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			d := New(Config{Locales: []string{tc.locale}})

			var found bool
			for _, m := range d.Scan("date of birth " + tc.date + " on file") {
				if m.Category == pii.CatDOB && m.Value == tc.date {
					found = true
				}
			}
			if !found {
				t.Errorf("a %s deployment does not recognise %q as a date of birth, "+
					"so it forwards one in clear", tc.locale, tc.date)
			}
		})
	}
}
