package pii

import (
	"regexp"
	"strings"
	"testing"
)

// The catalogue's own consistency. Every failure here is silent at runtime — a
// value masked under a nameless token, a category scored by a default nobody
// chose, two categories sharing one prefix and therefore one numbering — which
// is why validateCatalogue panics at initialisation rather than letting a
// deployment discover it.

// checkCatalogue is that guard, so it gets its own table: one that accepts
// everything looks exactly like a catalogue with nothing wrong in it.
func TestCheckCatalogue(t *testing.T) {
	ok := regexp.MustCompile(`x`)

	// One sound entry, and a helper that breaks exactly one field of it. Written
	// this way so a new required field fails every case that should fail rather
	// than being quietly absent from four hand-written literals.
	sound := CategoryInfo{Prefix: "A", Score: 50, Group: GroupPersonal, Label: "a value"}
	without := func(break_ func(*CategoryInfo)) CategoryInfo {
		broken := sound
		break_(&broken)
		return broken
	}

	tests := []struct {
		name     string
		patterns []Pattern
		registry map[Category]CategoryInfo
		want     string // substring the error must carry; "" means it must pass
	}{
		{
			name:     "a sound catalogue passes",
			patterns: []Pattern{{Regex: ok, Category: "A", Label: "a"}},
			registry: map[Category]CategoryInfo{"A": sound},
		},
		{
			// The failure that shipped in the project this replaces: a pattern
			// emitting a category nothing registered, whose tokens rendered as
			// "[_1]" and whose matches were scored by a default.
			name:     "a pattern emitting an unregistered category",
			patterns: []Pattern{{Regex: ok, Category: "GHOST", Label: "ghost pattern"}},
			registry: map[Category]CategoryInfo{"A": sound},
			want:     "not in the registry",
		},
		{
			name:     "a registered category with no prefix",
			patterns: nil,
			registry: map[Category]CategoryInfo{"A": without(func(i *CategoryInfo) { i.Prefix = "" })},
			want:     "no token prefix",
		},
		{
			// "1PASSWORD_TOKEN" opened on a digit, so the token it rendered was not
			// one by tokenRe and the rehydrator filed a credential's replacement as
			// a stand-in.
			name:     "a registered category whose prefix does not make a token",
			patterns: nil,
			registry: map[Category]CategoryInfo{"A": without(func(i *CategoryInfo) { i.Prefix = "1PASSWORD_TOKEN" })},
			want:     "does not make a token",
		},
		{
			name:     "a registered category with no score",
			patterns: nil,
			registry: map[Category]CategoryInfo{"A": without(func(i *CategoryInfo) { i.Score = 0 })},
			want:     "outside 1-100",
		},
		{
			name:     "a score above the scale",
			patterns: nil,
			registry: map[Category]CategoryInfo{"A": without(func(i *CategoryInfo) { i.Score = 101 })},
			want:     "outside 1-100",
		},
		{
			// A category with no label: every surface listing it would have to
			// invent a name, and two of them would invent different ones.
			name:     "a registered category with no label",
			patterns: nil,
			registry: map[Category]CategoryInfo{"A": without(func(i *CategoryInfo) { i.Label = "" })},
			want:     "no label",
		},
		{
			// A category with no group is absent from every surface that lists the
			// catalogue by family — which is the only way forty of them are listed.
			name:     "a registered category with no group",
			patterns: nil,
			registry: map[Category]CategoryInfo{"A": without(func(i *CategoryInfo) { i.Group = "" })},
			want:     "no group",
		},
		{
			name:     "a category naming a group that does not exist",
			patterns: nil,
			registry: map[Category]CategoryInfo{"A": without(func(i *CategoryInfo) { i.Group = "invented" })},
			want:     "not registered",
		},
		{
			// Two identical rows in the menu that switches them, and no way for
			// somebody unticking one to know which they got.
			name:     "two categories sharing a label",
			patterns: nil,
			registry: map[Category]CategoryInfo{
				"A": sound,
				"B": {Prefix: "B", Score: 50, Group: GroupPersonal, Label: "a value"},
			},
			want: "share the label",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkCatalogue(tt.patterns, tt.registry)

			if tt.want == "" {
				if err != nil {
					t.Fatalf("a sound catalogue was rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("the validator accepted an unsound catalogue")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

// The live catalogue has to satisfy its own validator. The init function already
// panics if it does not, but a test says so by name rather than by the whole
// package failing to load.
func TestLiveCatalogueIsSound(t *testing.T) {
	if err := checkCatalogue(AllPatterns(), categoryRegistry); err != nil {
		t.Fatalf("the catalogue in this package is unsound: %v", err)
	}
}

// A prefix is what numbers a category's tokens. Two categories sharing one would
// share the counter behind it, so an email and a phone number would come out as
// [CONTACT_1] and [CONTACT_2] with nothing saying which was which — and on the
// way back, one token could expand to either.
func TestCategoryPrefixesAreUnique(t *testing.T) {
	byPrefix := map[string]Category{}
	for _, cat := range Categories() {
		info, _ := Info(cat)
		if other, taken := byPrefix[info.Prefix]; taken {
			t.Errorf("categories %q and %q share the prefix %q: their tokens would share a numbering",
				other, cat, info.Prefix)
			continue
		}
		byPrefix[info.Prefix] = cat
	}
}

// Every credential category must be marked as one. The flag is what makes a
// credential outrank the confidence scale in overlap resolution, and a
// credential that loses its span to an ordinary category is tokenized under that
// category instead — reversible, and expanded back into a live secret on the way
// out.
func TestSecretCategoriesAreMarked(t *testing.T) {
	for _, cat := range Categories() {
		named := strings.HasPrefix(string(cat), "SECRET_")
		if IsSecret(cat) != named {
			t.Errorf("category %q: named as a credential = %v, but Secret = %v — "+
				"the flag decides overlap resolution, so the two must agree", cat, named, IsSecret(cat))
		}
	}
}

// Score is the single place a candidate's confidence is decided, including the
// rule that a failed checksum is not a weak match but no match at all.
func TestScore(t *testing.T) {
	t.Run("a category with no checksum scores its base", func(t *testing.T) {
		got, ok := Score(CatEmail, "claire@example.fr")
		if !ok || got != 95 {
			t.Errorf("Score(EMAIL) = %d, %v; want 95, true", got, ok)
		}
	})

	t.Run("a passing checksum scores its base", func(t *testing.T) {
		got, ok := Score(CatNIR, "184037511600176")
		if !ok || got != 95 {
			t.Errorf("Score(NIR, valid) = %d, %v; want 95, true", got, ok)
		}
	})

	t.Run("a failing checksum is not reportable at any threshold", func(t *testing.T) {
		// Not "reportable but weak". Fifteen digits failing the NIR key are not
		// a NIR, and no sensitivity setting should be able to turn them into
		// one — which is what scoring them 30 rather than rejecting them left
		// open.
		if got, ok := Score(CatNIR, "269054958815781"); ok {
			t.Errorf("Score(NIR, invalid) = %d, %v; want 0, false", got, ok)
		}
	})

	t.Run("an unregistered category is not reportable", func(t *testing.T) {
		if got, ok := Score("NOT_A_CATEGORY", "x"); ok {
			t.Errorf("Score(unregistered) = %d, %v; want 0, false", got, ok)
		}
	})
}

func TestPrefixAndInfo(t *testing.T) {
	if got := Prefix(CatEmail); got != "EMAIL" {
		t.Errorf("Prefix(EMAIL) = %q, want %q", got, "EMAIL")
	}
	if got := Prefix("NOT_A_CATEGORY"); got != "" {
		t.Errorf("Prefix(unregistered) = %q, want empty", got)
	}
	if _, ok := Info("NOT_A_CATEGORY"); ok {
		t.Error("Info reported an unregistered category as known")
	}
}
