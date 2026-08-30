package detector

import (
	"sort"
	"strings"
	"testing"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

// The sample is the reference an operator reads to check their own data shape is
// covered, so a gap in it reads as a gap in the engine. These tests are what
// keep it a complete reference rather than a snapshot of whatever the catalogue
// looked like the day it was written.

// Every category a deployment's patterns can emit must appear in the sample it
// serves. Swept from the live catalogue, so a category added without a sample
// line fails here on the commit that adds it.
func TestSampleExercisesEveryCategory(t *testing.T) {
	// Each registered locale on its own, then all of them together: a locale
	// whose section is missing from the assembled document would otherwise hide
	// behind another locale's coverage of a shared category.
	specs := append([]string{"none"}, pii.LocaleCodes()...)
	specs = append(specs, strings.Join(pii.LocaleCodes(), ","))

	for _, spec := range specs {
		t.Run(spec, func(t *testing.T) {
			d := detectorFor(t, spec)

			found := map[pii.Category]bool{}
			for _, m := range d.Scan(d.Sample()) {
				found[m.Category] = true
			}

			var missing []string
			for _, p := range d.catalogue().patterns {
				if !found[p.Category] {
					missing = append(missing, string(p.Category)+" ("+p.Label+")")
				}
			}
			sort.Strings(missing)

			for _, cat := range missing {
				t.Errorf("the sample for %q carries nothing detected as %s — an operator reading it "+
					"would conclude the engine does not look for it", spec, cat)
			}
		})
	}
}

// The notations, one row each, written out rather than derived. A derived table
// agrees with whatever the detector does — including a form it silently stopped
// reading — so this one names the value and the category it must be read as.
//
// Widening or narrowing a pattern means adding or moving a row here.
func TestSampleShowsEveryNotation(t *testing.T) {
	tests := []struct {
		locale string
		want   []struct {
			value string
			cat   pii.Category
		}
	}{
		{
			locale: "none",
			want: notations(
				"claire.moreau@example.fr", pii.CatEmail,
				"claire+juridique@example.fr", pii.CatEmail,
				"andré.muller@example.fr", pii.CatEmail,
				"4532015112830366", pii.CatCreditCard,
				"4532 0151 1283 0366", pii.CatCreditCard,
				"4532-0151-1283-0366", pii.CatCreditCard,
				"FR1420041010050500013M02606", pii.CatIBAN,
				"FR14 2004 1010 0505 0001 3M02 606", pii.CatIBAN,
				"DE89370400440532013000", pii.CatIBAN,
				"BE68539007547034", pii.CatIBAN,
				"NL91ABNA0417164300", pii.CatIBAN,
				"192.168.13.42", pii.CatIPAddr,
				"507f1f77bcf86cd799439011", pii.CatMongoID,
				"1987-03-14", pii.CatDOB,
				"1987/03/14", pii.CatDOB,
			),
		},
		{
			locale: "fr",
			want: notations(
				"2 69 05 49 588 157 80", pii.CatNIR,
				"184037511600176", pii.CatNIR,
				"443061841", pii.CatSIREN,
				"732 829 320", pii.CatSIREN,
				"552 100 554 00013", pii.CatSIRET,
				"06 12 34 56 78", pii.CatPhone,
				"01.45.67.89.10", pii.CatPhone,
				"+33 1 42 68 53 00", pii.CatPhone,
				"+33 (0)1 42 68 53 00", pii.CatPhone,
				"12 rue de la Paix, 75002 Paris", pii.CatAddress,
				"12 r. de la Paix, 75002 Paris", pii.CatAddress,
				"Route de Lyon, 38000 Grenoble", pii.CatAddress,
				"13290 Aix Les Milles", pii.CatPostalCode,
				"AB-123-CD", pii.CatLicPlate,
				// The eleven ways the same day-first date is written. Every one
				// of them is a separate branch or a separate optional group in
				// the expression, and a table with only the tidy forms in it
				// lets the others be dropped unnoticed.
				"23 février 2004", pii.CatDOB,
				"23 Février 2004", pii.CatDOB,
				"23 fevrier 2004", pii.CatDOB,
				"23/02/2004", pii.CatDOB,
				"23/2/2004", pii.CatDOB,
				"23-02-2004", pii.CatDOB,
				"23-2-2004", pii.CatDOB,
				"23.02.2004", pii.CatDOB,
				"23.2.2004", pii.CatDOB,
				"23 02 2004", pii.CatDOB,
				"23 2 2004", pii.CatDOB,
				// And the leading zero, optional on both sides.
				"9 mars 2004", pii.CatDOB,
				"09 mars 2004", pii.CatDOB,
				"9/3/2004", pii.CatDOB,
				"09/3/2004", pii.CatDOB,
				"9/03/2004", pii.CatDOB,
				// The ordinal, which only the first of the month takes.
				"1er mars 2019", pii.CatDOB,
			),
		},
		{
			locale: "gb",
			want: notations(
				"943 476 5919", pii.CatNHSNumber,
				"9434765919", pii.CatNHSNumber,
				"AB 12 34 56 C", pii.CatNINO,
				"AB123456C", pii.CatNINO,
				"AB123456", pii.CatNINO,
				"SW1A 1AA", pii.CatPostalCode,
				"M1 1AE", pii.CatPostalCode,
				"EC1A 1BB", pii.CatPostalCode,
				"B33 8TH", pii.CatPostalCode,
				"DN55 1PT", pii.CatPostalCode,
				"CR2 6XH", pii.CatPostalCode,
				"020 7946 0958", pii.CatPhone,
				"0161 496 0000", pii.CatPhone,
				"07700 900123", pii.CatPhone,
				"02079460958", pii.CatPhone,
				"+44 20 7946 0958", pii.CatPhone,
				// The address takes the town and the postcode with it: a token
				// that restored only the street would leave the pair that
				// identifies the household in clear beside it.
				"10 Downing Street, London SW1A 2AA", pii.CatAddress,
				"221B Baker Street, London NW1 6XE", pii.CatAddress,
				"42 Wellington Crescent", pii.CatAddress,
				"8 High St, Manchester M1 2AB", pii.CatAddress,
			),
		},
		{
			locale: "us",
			want: notations(
				"123-45-6789", pii.CatSSN,
				"12-3456789", pii.CatEIN,
				"021000021", pii.CatRoutingNumber,
				"011000015", pii.CatRoutingNumber,
				"(555) 234-5678", pii.CatPhone,
				"555-234-5678", pii.CatPhone,
				"5552345678", pii.CatPhone,
				"+1 555 234 5678", pii.CatPhone,
				"123 Main St, Springfield, IL 62704", pii.CatAddress,
				"456 Oak Avenue", pii.CatAddress,
				"IL 62704", pii.CatPostalCode,
				"62704-1234", pii.CatPostalCode,
				// Month first, which is this locale's reading of a date and
				// nobody else's.
				"03/14/1987", pii.CatDOB,
				"03-14-1987", pii.CatDOB,
				"03.14.1987", pii.CatDOB,
				"3/14/1987", pii.CatDOB,
				"12/25/2024", pii.CatDOB,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.locale, func(t *testing.T) {
			d := detectorFor(t, tt.locale)
			sample := d.Sample()

			got := map[string]pii.Category{}
			for _, m := range d.Scan(sample) {
				got[m.Value] = m.Category
			}

			for _, w := range tt.want {
				// A notation absent from the sample text is the first failure to
				// report: the row is asserting something the document does not
				// even show.
				if !strings.Contains(sample, w.value) {
					t.Errorf("the sample does not show %q, so it demonstrates nothing about that notation", w.value)
					continue
				}
				cat, found := got[w.value]
				if !found {
					t.Errorf("%q is in the sample but is not detected: the sample shows a form the engine misses", w.value)
					continue
				}
				if cat != w.cat {
					t.Errorf("%q is read as %s, want %s", w.value, cat, w.cat)
				}
			}
		})
	}
}

// The twelve month names, one assertion each.
//
// Not rows in the notation table, because twelve near-identical lines would bury
// it — but they need their own check all the same: they are twelve alternatives
// in one expression, and a typo in any of them is a whole month of dates going
// out in clear with every other month still working.
func TestSampleShowsEveryMonthName(t *testing.T) {
	d := detectorFor(t, "fr")
	sample := d.Sample()

	found := map[string]pii.Category{}
	for _, m := range d.Scan(sample) {
		found[m.Value] = m.Category
	}

	// Accented where the month carries one, since that is how it is written.
	months := []string{
		"9 janvier 2004", "17 février 1998", "1er mars 2019", "4 avril 1977",
		"12 mai 1985", "30 juin 1962", "8 juillet 2001", "22 août 1993",
		"3 septembre 1970", "15 octobre 1988", "26 novembre 1955", "31 décembre 1978",
	}
	for _, month := range months {
		if !strings.Contains(sample, month) {
			t.Errorf("the sample does not show %q", month)
			continue
		}
		if cat := found[month]; cat != pii.CatDOB {
			t.Errorf("%q is read as %q, want %s", month, cat, pii.CatDOB)
		}
	}

	// And the unaccented spellings, which a keyboard without a compose key
	// produces and a form field therefore receives.
	for _, month := range []string{"5 aout 1999", "7 decembre 1980", "3 fevrier 1971"} {
		if !strings.Contains(sample, month) {
			t.Errorf("the sample does not show %q", month)
			continue
		}
		if cat := found[month]; cat != pii.CatDOB {
			t.Errorf("%q is read as %q, want %s — an unaccented month is still a month",
				month, cat, pii.CatDOB)
		}
	}
}

// Every street-type spelling each locale accepts, one assertion each.
//
// Same argument as the month names, and a larger surface: the three patterns
// carry sixty-six spellings between them, and the sample used to show six. A
// typo in any of the other sixty loses that street type silently and for good —
// every "impasse" address in a French deployment going out in clear while every
// "rue" address is masked, with nothing failing.
//
// The lists are written out rather than read from the pattern constants. Reading
// them from the constant is the trap: a typo would appear in both the expression
// and the expectation, and the test would agree with the bug.
func TestSampleShowsEveryStreetType(t *testing.T) {
	tests := []struct {
		locale    string
		addresses []string
	}{
		{
			locale: "fr",
			addresses: []string{
				"1 avenue de l'Exemple", "2 allée de l'Exemple", "3 boulevard de l'Exemple",
				"4 chemin de l'Exemple", "5 cours de l'Exemple", "6 impasse de l'Exemple",
				"7 place de l'Exemple", "8 quai de l'Exemple", "9 route de l'Exemple",
				"10 rue de l'Exemple", "11 square de l'Exemple",
				// The abbreviations, which is what an address field actually holds.
				"12 av. de l'Exemple", "13 all. de l'Exemple", "14 bd de l'Exemple",
				"15 bd. de l'Exemple", "16 ch. de l'Exemple", "17 crs de l'Exemple",
				"18 imp. de l'Exemple", "19 pl. de l'Exemple", "20 rte de l'Exemple",
				"21 r. de l'Exemple", "22 sq. de l'Exemple",
				// And the completed house number.
				"12 bis rue de la Paix", "14 ter avenue de la Paix", "16 quater place de la Paix",
			},
		},
		{
			locale: "gb",
			addresses: []string{
				"1 Example Street", "2 Example Road", "3 Example Avenue", "4 Example Lane",
				"5 Example Close", "6 Example Drive", "7 Example Place", "8 Example Court",
				"9 Example Crescent", "10 Example Gardens", "11 Example Terrace",
				"12 Example Square", "13 Example Mews", "14 Example Grove",
				"15 Example Parade", "16 Example Way", "17 Example St", "18 Example Rd",
				"19 Example Ave",
			},
		},
		{
			locale: "us",
			addresses: []string{
				"1 Example Street", "2 Example Avenue", "3 Example Boulevard", "4 Example Road",
				"5 Example Drive", "6 Example Lane", "7 Example Court", "8 Example Place",
				"9 Example Terrace", "10 Example Parkway", "11 Example Circle",
				"12 Example Highway", "13 Example Way",
				"14 Example St", "15 Example Ave", "16 Example Blvd", "17 Example Rd",
				"18 Example Dr", "19 Example Ln", "20 Example Ct", "21 Example Pl",
				"22 Example Ter", "23 Example Pkwy", "24 Example Cir", "25 Example Hwy",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.locale, func(t *testing.T) {
			d := detectorFor(t, tt.locale)
			sample := d.Sample()

			found := map[string]pii.Category{}
			for _, m := range d.Scan(sample) {
				found[m.Value] = m.Category
			}

			for _, address := range tt.addresses {
				if !strings.Contains(sample, address) {
					t.Errorf("the sample does not show %q, so it demonstrates nothing about that street type", address)
					continue
				}
				if cat := found[address]; cat != pii.CatAddress {
					t.Errorf("%q is read as %q, want %s", address, cat, pii.CatAddress)
				}
			}
		})
	}
}

// The postcode notations, which vary more than a postcode looks like it should.
func TestSampleShowsEveryPostcodeNotation(t *testing.T) {
	tests := []struct {
		locale string
		codes  []string
	}{
		{
			locale: "fr",
			codes: []string{
				"69001 Lyon",            // one word
				"13100 Aix-en-Provence", // hyphenated
				"13290 Aix Les Milles",  // several words
				"91150 Étampes",         // an accented capital, which the anchor has to admit
				"01000 Bourg",           // the lowest department
				"98000 Monaco",          // and the highest
			},
		},
		{
			locale: "gb",
			codes: []string{
				// All six layouts. The third character is what varies — absent, a
				// digit, or a letter — and a table with one of them lets the
				// others be dropped.
				"SW1A 1AA", "M1 1AE", "EC1A 1BB", "B33 8TH", "DN55 1PT", "CR2 6XH",
			},
		},
		{
			locale: "us",
			codes: []string{
				// A ZIP is only identifying with something anchoring it, so each
				// row carries its anchor: a state, or the four-digit add-on.
				"IL 62704", "CA 90210", "NY 10001", "TX 75001", "DC 20500", "62704-1234",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.locale, func(t *testing.T) {
			d := detectorFor(t, tt.locale)
			sample := d.Sample()

			found := map[string]pii.Category{}
			for _, m := range d.Scan(sample) {
				found[m.Value] = m.Category
			}

			for _, code := range tt.codes {
				if !strings.Contains(sample, code) {
					t.Errorf("the sample does not show %q", code)
					continue
				}
				if cat := found[code]; cat != pii.CatPostalCode {
					t.Errorf("%q is read as %q, want %s", code, cat, pii.CatPostalCode)
				}
			}
		})
	}
}

// The credentials are locale-independent, so they get one table rather than one
// per locale. The vendor prefix is what each row is about.
func TestSampleShowsEveryCredential(t *testing.T) {
	d := detectorFor(t, "none")
	sample := d.Sample()

	got := map[string]pii.Category{}
	for _, m := range d.Scan(sample) {
		got[m.Value] = m.Category
	}

	want := notations(
		"sk-proj-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH", pii.CatOpenAIKey,
		"sk-svcacct-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH", pii.CatOpenAIKey,
		"sk-admin-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH", pii.CatOpenAIKey,
		"sk-abcdefghijklmnopqrstuvwxyz0123", pii.CatOpenAIKey,
		"sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", pii.CatAnthropicKey,
		"AIzaabcdefghijklmnopqrstuvwxyzABCDEFGHI", pii.CatGoogleKey,
		"AKIAIOSFODNN7EXAMPLE", pii.CatAWSAccessKey,
		"ASIAY34FZKBOKMUTVV7A", pii.CatAWSAccessKey,
		"ABIAY34FZKBOKMUTVV7A", pii.CatAWSAccessKey,
		"ACCAY34FZKBOKMUTVV7A", pii.CatAWSAccessKey,
		"A3TY34FZKBOKMUTVV7AB", pii.CatAWSAccessKey,
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN", pii.CatAWSSecretKey,
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789", pii.CatGitHubToken,
		"gho_0123456789abcdefghijklmnopqrstuvwxyz", pii.CatGitHubToken,
		"github_pat_11ABCDEFG0abcdefghijkl_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789AB", pii.CatGitHubToken,
		"glpat-abcdefghijklmnopqrst", pii.CatGitLabToken,
		"xoxb-0123456789-a", pii.CatSlackToken,
		"xapp-0123456789-z", pii.CatSlackToken,
		"sk_live_abcdefghijklmnopqrstuvwx", pii.CatStripeKey,
		"sk_test_abcdefghijklmnopqrstuvwx", pii.CatStripeKey,
		"sk_prod_abcdefghijklmnopqrstuvwx", pii.CatStripeKey,
		"rk_live_abcdefghijklmnopqrstuvwx", pii.CatStripeKey,
		"SG.abcdefghijklmnopqrstuv.abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", pii.CatSendGridKey,
		"SK0123456789abcdef0123456789abcdef", pii.CatTwilioKey,
		"SKFEDCBA9876543210FEDCBA9876543210", pii.CatTwilioKey,
		"npm_abcdefghijklmnopqrstuvwx", pii.CatNPMToken,
		"pypi-abcdefghijklmnopqrstuvwx", pii.CatPyPIToken,
		"dckr_pat_abcdefghijklmnopqrstuvwx", pii.CatDockerToken,
		"hf_abcdefghijklmnopqrstuvwx", pii.CatHFToken,
		"r8_abcdefghijklmnopqrstuvwx", pii.CatReplicateToken,
		"-----BEGIN OPENSSH PRIVATE KEY-----", pii.CatPEMKey,
		"-----BEGIN PGP PRIVATE KEY BLOCK-----", pii.CatPEMKey,
		"eyJabcdefghijkl.eyJabcdefghijklmn.abcdefghijklmnopqrst", pii.CatJWT,
		// The scheme is consumed but left out of the span, so the line reads
		// "postgres://[CONN_STR_1]" — the scheme is not a secret, and it is most
		// of what makes the line answerable.
		"admin:s3cr3t@db.example.com:5432/app", pii.CatConnStr,
		"guest:gu3st@broker.internal:5672/", pii.CatConnStr,
		":p4ssonly@redis.internal:6379", pii.CatConnStr,
		"user:p4ss@api.partner.com/v1/orders", pii.CatConnStr,
		"hunter2-correct-horse", pii.CatGenericSecret,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", pii.CatHexSecret,
	)

	for _, w := range want {
		if !strings.Contains(sample, w.value) {
			t.Errorf("the sample does not show %q", w.value)
			continue
		}
		cat, found := got[w.value]
		if !found {
			t.Errorf("%q is in the sample but is not detected", w.value)
			continue
		}
		if cat != w.cat {
			t.Errorf("%q is read as %s, want %s", w.value, cat, w.cat)
		}
	}
}

// notations turns alternating value/category arguments into rows, so a table of
// forty of them reads as a list rather than as forty braces.
func notations(pairs ...any) []struct {
	value string
	cat   pii.Category
} {
	var out []struct {
		value string
		cat   pii.Category
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, struct {
			value string
			cat   pii.Category
		}{pairs[i].(string), pairs[i+1].(pii.Category)})
	}
	return out
}
