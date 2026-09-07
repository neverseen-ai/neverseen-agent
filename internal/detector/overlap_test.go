package detector

import (
	"testing"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// Overlap resolution decides which category names a stretch of text, and every
// rule in it is there because its absence caused a leak. The table exercises the
// rules directly, on synthetic matches, because reaching some of them through
// real text means finding a string that happens to trip two patterns at once.

func TestResolveOverlaps(t *testing.T) {
	// A helper rather than literals, so a case reads as its rule.
	at := func(cat pii.Category, start, end int) Match {
		score, _ := pii.Score(cat, "")
		if score == 0 {
			score = 60 // a category whose checksum cannot pass on an empty value
		}
		return Match{Category: cat, Start: start, End: end, Confidence: score}
	}
	scored := func(cat pii.Category, start, end, score int) Match {
		m := at(cat, start, end)
		m.Confidence = score
		return m
	}

	tests := []struct {
		name string
		in   []Match
		want []pii.Category // in reading order
	}{
		{
			name: "matches that do not touch are all kept",
			in:   []Match{at(pii.CatEmail, 0, 10), at(pii.CatIPAddr, 20, 30)},
			want: []pii.Category{pii.CatEmail, pii.CatIPAddr},
		},
		{
			// The leak this rule exists for: a postal code inside an address.
			// With position deciding — "whichever starts later wins" — the
			// postcode evicted the address containing it and the street went to
			// the provider in clear.
			name: "a stronger match keeps its span against one starting further right",
			in:   []Match{at(pii.CatAddress, 0, 30), at(pii.CatPostalCode, 18, 30)},
			want: []pii.Category{pii.CatAddress},
		},
		{
			// A credential outranks the scale rather than sitting on it. Several
			// ordinary categories score above a connection string, so this span
			// resolved to an email — a reversible token over the password, which
			// the response path then expanded back into a live secret.
			name: "a credential wins against a higher-scoring ordinary category",
			in: []Match{
				scored(pii.CatEmail, 5, 20, 95),
				scored(pii.CatConnStr, 0, 40, 92),
			},
			want: []pii.Category{pii.CatConnStr},
		},
		{
			name: "confidence decides between two ordinary categories",
			in: []Match{
				scored(pii.CatIPAddr, 0, 12, 75),
				scored(pii.CatEmail, 0, 12, 95),
			},
			want: []pii.Category{pii.CatEmail},
		},
		{
			name: "at equal confidence the longer span wins, because it covers more of the value",
			in: []Match{
				scored(pii.CatEmail, 0, 8, 95),
				scored(pii.CatEmail, 0, 20, 95),
			},
			want: []pii.Category{pii.CatEmail},
		},
		{
			// Transitive overlap: A touches B, B touches C, A does not touch C.
			// All three compete as one cluster, and the two that fit are kept.
			name: "a chain of overlaps is resolved as one cluster",
			in: []Match{
				scored(pii.CatEmail, 0, 10, 95),
				scored(pii.CatIPAddr, 8, 20, 75),
				scored(pii.CatMongoID, 18, 30, 85),
			},
			want: []pii.Category{pii.CatEmail, pii.CatMongoID},
		},
		{
			name: "a single match is returned untouched",
			in:   []Match{at(pii.CatEmail, 0, 10)},
			want: []pii.Category{pii.CatEmail},
		},
		{
			name: "nothing in, nothing out",
			in:   nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOverlaps(tt.in)

			if len(got) != len(tt.want) {
				t.Fatalf("kept %d matches, want %d: %v", len(got), len(tt.want), categoriesOf(got))
			}
			for i := range tt.want {
				if got[i].Category != tt.want[i] {
					t.Errorf("match %d is %q, want %q (kept %v)", i, got[i].Category, tt.want[i], categoriesOf(got))
				}
			}
			// Whatever survives must be disjoint: the anonymiser splices at
			// these offsets and overlapping spans would corrupt the result.
			for i := 1; i < len(got); i++ {
				if got[i].Start < got[i-1].End {
					t.Errorf("survivors overlap: %d-%d then %d-%d",
						got[i-1].Start, got[i-1].End, got[i].Start, got[i].End)
				}
			}
		})
	}
}

func categoriesOf(matches []Match) []pii.Category {
	out := make([]pii.Category, len(matches))
	for i, m := range matches {
		out[i] = m.Category
	}
	return out
}

// The rule that matters most, exercised through real text rather than synthetic
// matches: a credentialed URL contains something that reads as an email address,
// and the credential has to win. If it does not, the password is tokenized
// reversibly and handed back in clear on the way out.
func TestCredentialWinsOverEmailInRealText(t *testing.T) {
	d := New(Config{})

	got := d.Scan("DATABASE_URL=postgres://admin:s3cr3t@db.example.com:5432/app")
	if len(got) == 0 {
		t.Fatal("nothing detected in a connection string")
	}
	for _, m := range got {
		if m.Category == pii.CatEmail {
			t.Errorf("the credential resolved to an email match (%q): its password would be "+
				"tokenized reversibly and expanded back on the way out", m.Value)
		}
	}
	if got[0].Category != pii.CatConnStr {
		t.Errorf("first match is %q, want %q", got[0].Category, pii.CatConnStr)
	}
}
