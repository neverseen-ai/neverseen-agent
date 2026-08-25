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
