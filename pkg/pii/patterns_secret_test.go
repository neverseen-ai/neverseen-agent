package pii

import "testing"

// Two patterns of one vendor have to be told apart in a report, or the label says
// nothing the category code did not: all eight Buildkite shapes read "Buildkite
// credential", which is what the comment above the loop promised was not so.
func TestVendorPatternsOfOneCategoryCarryDistinctLabels(t *testing.T) {
	labels := make(map[Category]map[string]bool)
	for _, p := range AllPatterns() {
		if p.Category != CatBuildkiteSecret && p.Category != CatVercelSecret {
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
