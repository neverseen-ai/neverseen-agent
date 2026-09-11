package pii

import "testing"

// Two patterns of one vendor have to be told apart in a report, or the label says
// nothing the category code did not: all eight Buildkite shapes read "Buildkite
// credential", which is what the comment above the loop promised was not so.
//
// Every category in the table, not the two that first showed the failure: the
// label was built from the literal prefix, so any vendor issuing two shapes behind
// one prefix — Flutterwave's `FLWSECK_TEST-`, Sourcegraph's `sgp_` — reproduced it
// silently.
func TestVendorPatternsOfOneCategoryCarryDistinctLabels(t *testing.T) {
	categories := make(map[Category]bool, len(vendorPrefixes))
	for _, v := range vendorPrefixes {
		categories[v.Category] = true
	}

	labels := make(map[Category]map[string]bool)
	for _, p := range AllPatterns() {
		if !categories[p.Category] {
			continue
		}
		if labels[p.Category] == nil {
			labels[p.Category] = make(map[string]bool)
		}
		if labels[p.Category][p.Label] {
			t.Errorf("two patterns of %s carry the label %q", p.Category, p.Label)
		}
		labels[p.Category][p.Label] = true
	}
	if len(labels[CatBuildkiteSecret]) < 2 {
		t.Fatalf("expected several Buildkite patterns, found %d", len(labels[CatBuildkiteSecret]))
	}
}

// The whole fifty-three-character Buildkite user token is the span. Written with
// the shorter branch first, Go's leftmost-first alternation stopped at forty and
// the last thirteen characters of the key went out in clear.
func TestBuildkiteUserTokenIsMaskedWhole(t *testing.T) {
	const token = "bkua_d9jzfx6kjwsk7kegy5mtic4udyfkozm4lncz7kywabcdefghijklm"
	if len(token) != len("bkua_")+53 {
		t.Fatalf("the test value is not the fifty-three-character shape: %d", len(token)-len("bkua_"))
	}

	whole := false
	for _, p := range SecretPatterns() {
		if p.Category != CatBuildkiteSecret {
			continue
		}
		switch got := p.Regex.FindString("Le ticket porte " + token + " en clair."); got {
		case "":
		case token:
			whole = true
		default:
			t.Errorf("the pattern %q claimed %q, which leaves %d characters in clear",
				p.Label, got, len(token)-len(got))
		}
	}
	if !whole {
		t.Error("no Buildkite pattern claims the fifty-three-character user token whole")
	}
}
