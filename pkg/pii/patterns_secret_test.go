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

// The AWS secret has no prefix of its own, so the name in front of it is the whole
// of the evidence — and splitting the expression to drop its `(?i)` is exactly the
// change that loses a spelling silently. It lost the Capitalised one: written
// `Aws_Secret_Access_Key` or `Secret_Access_Key`, a key reached no
// CatAWSSecretKey pattern and fell through to SECRET_GENERIC, the one category
// NEVERSEEN_SECRET_LEVEL grades — so a credential masked unconditionally became a
// credential masked only if the deployment's level let it through, and the report
// lost the vendor's name.
//
// Every spelling the comment above the table promises, asserted together: any one
// of them alone is a test that passes over a table missing the other two.
func TestAWSSecretKeyIsReadInEveryNameSpelling(t *testing.T) {
	const secret = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN"
	if len(secret) != 40 {
		t.Fatalf("the test value is not the forty-character shape: %d", len(secret))
	}

	for _, name := range []string{
		"aws_secret", "AWS_SECRET", "Aws_Secret",
		"aws_secret_access_key", "AWS_SECRET_ACCESS_KEY", "Aws_Secret_Access_Key",
		"secret_access_key", "SECRET_ACCESS_KEY", "Secret_Access_Key",
	} {
		text := "config: " + name + " = " + secret + " and the rest of the line"
		found := false
		for _, p := range SecretPatterns() {
			if p.Category != CatAWSSecretKey {
				continue
			}
			m := p.Regex.FindStringSubmatch(text)
			if m == nil {
				continue
			}
			found = true
			// Group 1 is the value: a pattern that claimed the name as well would
			// mask the field's own key and break the paste it was meant to save.
			if m[p.Group] != secret {
				t.Errorf("%s: the pattern %q claimed %q, not the key", name, p.Label, m[p.Group])
			}
		}
		if !found {
			t.Errorf("%s = <forty characters> reached no %s pattern, so it falls through to the graded catch-all",
				name, CatAWSSecretKey)
		}
	}
}
