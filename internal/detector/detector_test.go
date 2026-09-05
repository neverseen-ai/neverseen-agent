package detector

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

func TestParseLocales(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    []string
		wantErr string // substring the error must carry; "" means it must succeed
	}{
		{name: "empty means no country set", spec: "", want: nil},
		{name: "blank means no country set", spec: "   ", want: nil},
		{name: "one code", spec: "fr", want: []string{"fr"}},
		{name: "several codes", spec: "fr,gb,us", want: []string{"fr", "gb", "us"}},
		{name: "spacing and case are forgiven", spec: " FR , Gb ", want: []string{"fr", "gb"}},
		{name: "a trailing comma is not an error", spec: "fr,", want: []string{"fr"}},
		{name: "none selects the locale-independent sets", spec: "none", want: nil},
		{name: "none alongside a country is redundant, not wrong", spec: "none,fr", want: []string{"fr"}},
		{
			// The message has to name the codes that exist, and name them from
			// the registry: a hand-written list in the message goes stale the
			// first time a locale is added.
			name: "an unknown code is refused and the known ones are named",
			spec: "zz", wantErr: `unknown PII locale "zz"`,
		},
		{
			name: "one bad code in a good list still fails",
			spec: "fr,zz", wantErr: "unknown PII locale",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLocales(tt.spec)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseLocales(%q) accepted an invalid selection", tt.spec)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not mention %q", err, tt.wantErr)
				}
				for _, code := range pii.LocaleCodes() {
					if !strings.Contains(err.Error(), code) {
						t.Errorf("error %q does not name the registered locale %q", err, code)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseLocales(%q): %v", tt.spec, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseLocales(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestFromEnv(t *testing.T) {
	t.Run("an unset environment scans no country's identifiers", func(t *testing.T) {
		t.Setenv(EnvLocale, "")
		t.Setenv(EnvAllowList, "")

		d, err := FromEnv()
		if err != nil {
			t.Fatalf("FromEnv: %v", err)
		}
		if got := d.Locales(); len(got) != 0 {
			t.Errorf("locales = %v, want none: a wrong country is worse than no country", got)
		}

		// The locale-independent sets are still on. This is the invariant that
		// must survive every configuration: turning off a country cannot turn
		// off email detection.
		if got := d.Scan("write to claire@example.fr"); len(got) != 1 || got[0].Category != pii.CatEmail {
			t.Errorf("scan found %v, want one email", got)
		}
	})

	t.Run("a locale is loaded", func(t *testing.T) {
		t.Setenv(EnvLocale, "gb")

		d, err := FromEnv()
		if err != nil {
			t.Fatalf("FromEnv: %v", err)
		}
		if got := d.Scan("NHS number 9434765919 on file"); len(got) != 1 || got[0].Category != pii.CatNHSNumber {
			t.Errorf("scan found %v, want one NHS number", got)
		}
	})

	t.Run("an invalid locale fails rather than falling back", func(t *testing.T) {
		// Falling back to a default would scan the wrong country's data with no
		// way for the operator to notice.
		t.Setenv(EnvLocale, "zz")

		if _, err := FromEnv(); err == nil {
			t.Fatal("FromEnv accepted an unknown locale")
		}
	})

	t.Run("the allow list is read", func(t *testing.T) {
		t.Setenv(EnvLocale, "none")
		t.Setenv(EnvAllowList, "claire@example.fr")

		d, err := FromEnv()
		if err != nil {
			t.Fatalf("FromEnv: %v", err)
		}
		if got := d.Scan("write to claire@example.fr"); len(got) != 0 {
			t.Errorf("scan found %v, want nothing: the value is allow-listed", got)
		}
	})
}

func TestAllowList(t *testing.T) {
	tests := []struct {
		name      string
		allow     string
		text      string
		wantFound bool
	}{
		{
			name:  "an exact value is not masked",
			allow: "claire@example.fr", text: "write to claire@example.fr",
		},
		{
			// The comparison ignores case and spacing, so an operator who
			// declares an account in one spelling covers the others. Anything
			// stricter means the operator declares a value, watches it get
			// masked anyway, and has nothing to go on.
			name:  "spacing is ignored",
			allow: "FR1420041010050500013M02606", text: "pay FR14 2004 1010 0505 0001 3M02 606 today",
		},
		{
			name:  "case is ignored",
			allow: "CLAIRE@EXAMPLE.FR", text: "write to claire@example.fr",
		},
		{
			name:  "a different value is still masked",
			allow: "paul@example.fr", text: "write to claire@example.fr",
			wantFound: true,
		},
		{
			name:  "an empty list masks everything",
			allow: "", text: "write to claire@example.fr",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New(Config{AllowList: parseValueList(tt.allow)})

			got := d.Scan(tt.text)
			if found := len(got) > 0; found != tt.wantFound {
				t.Errorf("scan found %v; wanted a match: %v", got, tt.wantFound)
			}
		})
	}
}

// Scan reports in reading order, so a caller numbering tokens as it walks the
// result gets [EMAIL_1] for the first address a reader meets rather than the
// last.
func TestScanReportsInReadingOrder(t *testing.T) {
	d := New(Config{})

	got := d.Scan("first ab@x.fr then cd@y.fr and last ef@z.fr")
	if len(got) != 3 {
		t.Fatalf("found %d matches, want 3: %v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Start >= got[i].Start {
			t.Fatalf("matches are not in reading order: %d then %d", got[i-1].Start, got[i].Start)
		}
	}
	if got[0].Value != "ab@x.fr" || got[2].Value != "ef@z.fr" {
		t.Errorf("got %q … %q, want the first and last addresses", got[0].Value, got[2].Value)
	}
}

// Every match's offsets must bracket exactly its value. Anonymisation splices
// the replacement in at these offsets, so a span that is off by one either
// leaves a character of the original in clear or eats one of the text around it.
func TestMatchOffsetsBracketTheValue(t *testing.T) {
	const text = "Mail claire@example.fr, NIR 2 69 05 49 588 157 80, carte 4532015112830366, " +
		"tel 06 12 34 56 78, IBAN FR14 2004 1010 0505 0001 3M02 606 et 12 rue de la Paix, 75002 Paris."

	d := New(Config{Locales: []string{"fr"}})

	matches := d.Scan(text)
	if len(matches) == 0 {
		t.Fatal("nothing detected in a body full of identifiers")
	}
	for _, m := range matches {
		if m.Start < 0 || m.End > len(text) || m.Start >= m.End {
			t.Errorf("%s: offsets %d-%d are not a span of the text", m.Category, m.Start, m.End)
			continue
		}
		if got := text[m.Start:m.End]; got != m.Value {
			t.Errorf("%s: offsets %d-%d cover %q but the value is %q", m.Category, m.Start, m.End, got, m.Value)
		}
	}
}

// Mixing locales is what a company operating in several countries does, and it
// is the configuration no corpus suite can cover: each suite declares one
// locale, so a collision between two only appears with both enabled.
//
// Every case here is a value one country issues that another country's pattern
// is tempted by. The value must still be found, and found under the right name —
// a mislabelled match is masked either way, but it tells the operator the wrong
// thing about what their data holds.
func TestMixedLocalesKeepTheRightCategory(t *testing.T) {
	d := New(Config{Locales: []string{"fr", "gb", "us"}})

	tests := []struct {
		name string
		text string
		want pii.Category
	}{
		{
			// Nine digits opening on a zero. The UK telephone pattern claimed
			// this when its compact branch had no length floor, and since both
			// score 90 the earlier locale won the tie: a US bank routing number
			// was reported as a London landline.
			name: "a US routing number is not a UK telephone number",
			text: "Wire to routing 021000021 today.",
			want: pii.CatRoutingNumber,
		},
		{
			// Nine digits again, this time passing Luhn rather than the ABA
			// weights. France loads first, which is what settles it.
			name: "a French SIREN stays a SIREN",
			text: "Fournisseur SIREN 443061841 au contrat.",
			want: pii.CatSIREN,
		},
		{
			// Ten digits in a 3-3-4 group is both an NHS number and a US
			// telephone layout. The mod-11 check digit is what decides.
			name: "an NHS number is not a US telephone number",
			text: "Patient 943 476 5919 was seen today.",
			want: pii.CatNHSNumber,
		},
		{
			name: "a French NIR is untouched by the other sets",
			text: "Assuré 184037511600176 enregistré.",
			want: pii.CatNIR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.Scan(tt.text)
			if len(got) != 1 {
				t.Fatalf("found %d matches, want exactly 1: %v", len(got), categoriesOf(got))
			}
			if got[0].Category != tt.want {
				t.Errorf("%q was reported as %s, want %s", got[0].Value, got[0].Category, tt.want)
			}
		})
	}
}

// A pattern that over-matches by design is scanned one hit at a time, resuming
// after the refined end. Scanning them all at once and refining afterwards
// resumes after the greedy end instead, so a second value immediately after the
// first is never seen — and goes out in clear.
func TestRefinedSpansFindTheSecondValue(t *testing.T) {
	d := New(Config{})

	got := d.Scan("Settle DE89370400440532013000EUR and BE68539007547034EUR today.")
	if len(got) != 2 {
		t.Fatalf("found %d IBANs, want 2: %v", len(got), got)
	}
	for i, want := range []string{"DE89370400440532013000", "BE68539007547034"} {
		if got[i].Value != want {
			t.Errorf("IBAN %d is %q, want %q", i, got[i].Value, want)
		}
	}
}

// A quoted secret ends on a consumed quote, so every match of that pattern ends
// short of the group. Resuming with a full rescan after each one made the scan
// quadratic — 300 of them took 1.8s — and the fix must still find every one, and
// still find the second of two keys the first one's boundary consumed.
func TestPatternSpansFindEveryQuotedSecretOnce(t *testing.T) {
	d := New(Config{})

	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "\"api_key_%d\": \"hunter2-correct-horse-%03d\",\n", i, i)
	}
	got := d.Scan(b.String())
	if len(got) != 300 {
		t.Fatalf("found %d secrets, want 300", len(got))
	}
	for i, m := range got {
		if want := fmt.Sprintf("hunter2-correct-horse-%03d", i); m.Value != want {
			t.Errorf("secret %d is %q, want %q", i, m.Value, want)
		}
	}

	keys := "4b1d3qyRZzQ9ADp0j5Wmplcm7hufPK5ACDiBZLPKD6,4b1dAqyRZzQ9ADp0j5Wmplcm7hufPK5ACDiBZLPKD7"
	got = d.Scan(keys)
	if len(got) != 2 {
		t.Fatalf("found %d ClickHouse keys in %q, want 2: %v", len(got), keys, got)
	}
}
