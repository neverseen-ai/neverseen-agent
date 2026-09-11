package detector

import (
	"sort"
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
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
				// Both Mastercard BIN ranges. Only 51-55 was read, so a 2-series
				// card — issued since 2017 — went to the model in clear.
				"5425233430109903", pii.CatCreditCard,
				"2221000000000009", pii.CatCreditCard,
				"FR1420041010050500013M02606", pii.CatIBAN,
				"FR14 2004 1010 0505 0001 3M02 606", pii.CatIBAN,
				"DE89370400440532013000", pii.CatIBAN,
				"BE68539007547034", pii.CatIBAN,
				"NL91ABNA0417164300", pii.CatIBAN,
				"192.168.13.42", pii.CatIPAddr,
				"507f1f77bcf86cd799439011", pii.CatMongoID,
				// Both families, because the category that held them was labelled
				// "IP address" while only one of them was ever read.
				// Unique local addresses, the IPv6 analogue of RFC 1918. The
				// documentation block would have been the obvious choice and is
				// exactly what IPAddressCheck now refuses: a sample cannot
				// demonstrate detection with a range the detector declines.
				"fd00:1234:5678:0000:0000:8a2e:0370:7334", pii.CatIPv6,
				"fd00:1234::1", pii.CatIPv6,
				"fd00:1234:5678::8a2e:370:7334", pii.CatIPv6,
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
				// Day first, which the United Kingdom shares with France and
				// reads the opposite way round from the United States. Every
				// notation is a separate branch of the expression, and a table
				// with only the tidy forms in it lets the others be dropped
				// unnoticed.
				"14/03/1987", pii.CatDOB,
				"14-03-1987", pii.CatDOB,
				"14.03.1987", pii.CatDOB,
				"14 March 1987", pii.CatDOB,
				// The leading zero, optional on both sides.
				"4/3/1987", pii.CatDOB,
				"04/03/1987", pii.CatDOB,
				"4 March 1987", pii.CatDOB,
				// The expression is case-folded, so a month somebody typed in
				// lower case is still a month.
				"3 september 1970", pii.CatDOB,
				"3 SEPTEMBER 1970", pii.CatDOB,
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
		"https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX", pii.CatSlackToken,
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
		// The whole block, body included. The header alone masked the delimiter
		// and forwarded the key material after it.
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEpQIBAAKCAQEAn6/O8li+SX4m98LLYt/PKSzEmQ++ZBD7Loh9P13f4yQ92EF3\nyxR5MsXFu9PRsrYQA7/4UTPHiC4y2sAVCBg4C2yyBpUEtMQjyCESi6Y=\n-----END RSA PRIVATE KEY-----", pii.CatPEMKey,
		"eyJabcdefghijkl.eyJabcdefghijklmn.abcdefghijklmnopqrst", pii.CatJWT,
		"eyJabcdefghijkl==.eyJabcdefghijklmn==.abcdefghijklmnopqrst", pii.CatJWT,
		"ewogICJhbGciOiAiSFMyNTYiCn0.ewogICJzdWIiOiAiMTIzNCIKfQ.abcdefghijklmnopqrst", pii.CatJWT,
		// The scheme is consumed but left out of the span, so the line reads
		// "postgres://[CONN_STR_1]" — the scheme is not a secret, and it is most
		// of what makes the line answerable.
		"admin:s3cr3t@db.example.com:5432/app", pii.CatConnStr,
		"guest:gu3st@broker.internal:5672/", pii.CatConnStr,
		":p4ssonly@redis.internal:6379", pii.CatConnStr,
		"user:p4ss@api.partner.com/v1/orders", pii.CatConnStr,
		"hunter2-correct-horse", pii.CatGenericSecret,
		"zedoh3OUNbSakBymo7yplBf6CGV4aMM", pii.CatGenericSecret,
		"Bymo7yplBf6CGV4aMM9zedoh3OUNbSa", pii.CatGenericSecret,
		"akBymo7yplBf6CGV4aMM9zedoh3OUNb", pii.CatGenericSecret,
		"9zedoh3OUNbSakBymo7yplBf6CGV4aMM", pii.CatGenericSecret,
		"edoh3OUNbSakBymo7yplBf6CGV4aMM9z", pii.CatGenericSecret,
		"doh3OUNbSakBymo7yplBf6CGV4aMM9ze", pii.CatGenericSecret,
		// The padding inside the quotes is not part of the value, which is the
		// whole point of the row: the span replaced must be the credential and
		// not the spaces somebody typed around it.
		"MM9zedoh3OUNbSakBymo7yplBf6CGV4", pii.CatGenericSecret,
		"ymo7yplBf6CGV4aMM9zedoh3OUNbSak", pii.CatGenericSecret,
		"4aMM9zedoh3OUNbSakBymo7yplBf6CGV", pii.CatGenericSecret,
		"YWRtaW46aHVudGVyMg==", pii.CatGenericSecret,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", pii.CatHexSecret,

		// The twelve shapes qualified by hand in docs/secret-shapes-to-label.md.
		// Each was a rule refusing a real credential on evidence about the *form*
		// of the value — punctuation, length, the absence of a digit — and each is
		// a row here because that is what stops the sample agreeing with whatever
		// the detector happens to do next.
		"Wh4t?Really", pii.CatGenericSecret,
		"Wh4t?Really1", pii.CatGenericSecret,
		"a;b;c1234x", pii.CatGenericSecret,
		"red,blue1x", pii.CatGenericSecret,
		"abc12345,def67890", pii.CatGenericSecret,
		"pa(ren)th1s", pii.CatGenericSecret,
		"[brackets]1", pii.CatGenericSecret,
		"correcthorse", pii.CatGenericSecret,
		"changeme", pii.CatGenericSecret,
		"mcjrx4", pii.CatGenericSecret,
		"abcdef.ghijkl", pii.CatGenericSecret,

		// The second tier of vendor prefixes, one row per notation. Generated once
		// against the catalogue and then written down: what makes these rows worth
		// having is that they stop agreeing with the detector the moment a prefix,
		// a length or a score moves.
		"A3-UJZDE8-GXD6NCF10EP-F91DH-ODZDO-C9IS0", pii.CatOnePasswordSecret,
		"ops_eyJspNxnyVmihA/2O76UMFxFkM/R5Kjp1vRt+1fjORS/6ilI8ihN5KXSc7Tvo/hBKqFYY/kv5ZJr3J1TWDtkwtDDb+xHKas1VOqg6YYZYn9ZhyiA4uoRgnatmUdjAWtGSU8po+799NksnRH9ucAUsdMlHUvTCQCyEZDz/TddJ8HyS5SUkCnD8zRA9a9SkpXz9w3QlY7Zkuvqdt7s8Stqcbnr3yBdGBLEPH1qhT61qtc4xatws8phP9nhFyJfm", pii.CatOnePasswordSecret,
		"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", pii.CatIntra42Secret,
		"p8e-629be2u66mr26846p7q9m2i0hz2uep1e", pii.CatAdobeSecret,
		"AGE-SECRET-KEY-1DN8FHFSGAWXEL2W2ME46VK59HP4AUPC4JY8WX9S3ZT3GMSEFL593RTMY3P", pii.CatAgeSecret,
		"AIK_CI_oFzQFm2OEQ3HdAVja76R", pii.CatAikidoSecret,
		"AIK_SECRET_nIChtP8HKQDLM7ToThwNScgrLRWzBQCABugjMgeP7cGq0pbqfi14ZgTsNOVM14tu", pii.CatAikidoSecret,
		"patOizwd1iaeOV4qB.fbc6313b8ff28a25441b305fa46c9201ac2ce6b6a331ce649e3eb7ea1cabe5d6", pii.CatAirtableSecret,
		"LTAI1cjDoBoirPfQAdzEv", pii.CatAlibabaSecret,
		"STS.7g5iFqhEvveQzE2Q", pii.CatAlibabaSecret,
		"apify_api_PuwNOvpdf2YEe6rSxCnopMEmJVQpvsTnkI", pii.CatApifySecret,
		"AKCpAeDfRrGsNrfSthSdddxH5jMTF7eBSdE0g9cRYN687NElFJvhQ8XIm0ogR4HtXOf54fZB", pii.CatArtifactorySecret,
		"cmVmdKA8frcZTuJaWYUH1VAUwV1ZH87MtA5vSQXEZY3lEX7bwR2DRGD1qSo7JPRb", pii.CatArtifactorySecret,
		"$aact_prod_oYv2DzaKG05Rk_GQV81r", pii.CatAsaasSecret,
		"$aact_hmlg_kmghzem9yPVUJa-c5q52", pii.CatAsaasSecret,
		"sc_i9mpf.lv9f.acc-pxq-mb0y07.nyrvd5r+xi67-nfrpyz21tbic14/5a", pii.CatAuthressSecret,
		"ext_ez732.pgoj.acc_7g3f9caio-.cti-q7-1hget7/myqo_aa8t3rup47p", pii.CatAuthressSecret,
		"scauth_9pb0t.dbm5.acc-fqo1xo5cv0.xzmas6en5mtmo3oqsg=5=lo50d_jzd", pii.CatAuthressSecret,
		"authress_nbj0d.dlz2.acc-hfkvml73ct.yxv2kgafrfw0h9nywt1fd4mx82mux4", pii.CatAuthressSecret,
		"Endpoint=https://example-config.azconfig.io;Id=abcd;Secret=abcdefghijklmnopqrstuvwxyz0123456789", pii.CatAzureAppConfigSecret,
		"Endpoint=sb://uu3.servicebus.windows.net/;SharedAccessKeyName=P;SharedAccessKey=KZyUf0IE9pU2NJhKaM1/5WdR16ePllji", pii.CatAzureServiceBusSecret,
		"BSAvghZ4fXfeTkYpIygfdM7ENA8", pii.CatBraveSearchSecret,
		"xkeysib-5C7Ef653Cc3be1c61D641ac6ed0Cd712Cc28Fdb3DAc8CCFA444168C28E093dbe-ZRsdM3IVV8iwO2y2", pii.CatBrevoSecret,
		"bkaa_d5vFldPGYYJvW5hANsbEvrSFagEaBp0vXnJaE-9I0MyTLUyi0kn1Gnt11CuZyzaA3U2OLzu6UQB", pii.CatBuildkiteSecret,
		"bkua_d9jzfx6kjwsk7kegy5mtic4udyfkozm4lncz7kyw", pii.CatBuildkiteSecret,
		// Both lengths of the user token. The fifty-three-character one is the
		// notation the alternation's order used to cut short at forty.
		"bkua_hqekxwqaflliz8x7f5qjrhow1n5946k2ruadyq1nj3e6vp749o96q", pii.CatBuildkiteSecret,
		"cnvcaptFyfePpX6N1NF2XV54wca_7E56w8ZniqT3Ul4ffqkOkgWrdioy", pii.CatCanvaSecret,
		// "csk-" against openAILegacyRe's "sk-": at a score below OpenAI's this
		// came out as CatOpenAIKey with the leading "c" in clear, which is the row
		// that says the tie at 98 is doing its job.
		"csk-i5skoewqkur3jq64nq6puxcmlzkruykqh7dx297gq8zxqyxj", pii.CatCerebrasSecret,
		"CCIPAT_xvWfColNV9ds0HqtO93L7Q_uacojs106xdi5ocbdawtg7w8o0tinx4kiapj2gej", pii.CatCircleciSecret,
		"4b1d3qyRZzQ9ADp0j5Wmplcm7hufPK5ACDiBZLPKD6", pii.CatClickhouseSecret,
		"CLOJARS_ga9mj0m760l6tetd48ay13f2logqochvqdr917qsnf6akqpmkumyvpy8447a", pii.CatClojarsSecret,
		"v1.0-a71306cfebaddf5eaabebcbc-50c6d100dbbc39ded034472a523b5493a7a7d59b0c3f7a03ba59d9f952f3019fdc9d45d66c7a50327f618eb54e84f8821e481023ee145f1402dfd06ee33720dd2068ba67138ae26a17", pii.CatCloudflareSecret,
		"csa_711fd8742d716f2798a7f4a69db20fty88Mh", pii.CatCloudsmithSecret,
		"CCDB1_WG2kdiNtegBoy1XhVav8dN_rLZgw7HunWoDQRYZDAEa6aosrWlQGOTvZ89hOz9Z", pii.CatCockroachDBSecret,
		"configcat-sdk-1/7bVQIY8cSt07lQ8tdiwg2X/9Ajtfmp9_2KuTmxHKpRsBB", pii.CatConfigcatSecret,
		"dapi0c32de1f85e06fc3090c8dd271e99b98-2", pii.CatDatabricksSecret,
		"AstraCS:sfPfKim3vAK1UdskfqS1", pii.CatDataStaxAstraSecret,
		"ddp_dXba9rELoXopBBnCrv7VzGgefw5JCNtaoIVG", pii.CatDenoSecret,
		"dvc_client_3qXVexhj", pii.CatDevcycleSecret,
		"dvc_mobile_x6NSbVbQ", pii.CatDevcycleSecret,
		"dvc_server_jD0SSW0f", pii.CatDevcycleSecret,
		"apk_user_zqisa/PqYomQLFzzGzmNAFY8HwSKbF6WMXE1MBvRnhmX1EoC3G/FP1z5IBxT80NK8bTB2ABPLbPQ8Cjf5XGuSKl/6gGEBHBKxnnV+Hov48VSOuU19x5iqljH", pii.CatDevinSecret,
		"apk_qBTn2fwxwd5kAphi2UFkSSj/sK+wZdnHy7agBx6LtIdyhp9ZYbYLXlutzTfF/vNv7KToDsjCMEa+bhj2", pii.CatDevinSecret,
		"cog_g4iqcvmlyfbdcx57ezhfquofzl4kxpolcqwdbdq6dgjuamt4g6ux", pii.CatDevinSecret,
		"doo_v1_26d596f81ea80bf1c5e8d6ac84419d5e41bf8e8e2771ea234f29d489deb093d2", pii.CatDigitaloceanSecret,
		"dop_v1_057211d637fb3ea84e8a3f57b702fef1f0cc92f0e030ac7b5439ca79e21f5bf5", pii.CatDigitaloceanSecret,
		"dor_v1_a58cd5146b3d98aea1c1ffd32aad02a818d5dfb2d892ddd6e11e86fa67b6b546", pii.CatDigitaloceanSecret,
		"dp.pt.pv1uz9du7jwp1axg7leu1m6boi0z3cccrr8cgqh7a1p", pii.CatDopplerSecret,
		"duffel_test_cshtwkhd=6rf3-8j2h6is0_srpf8s3_oym9x39t44tb", pii.CatDuffelSecret,
		"dt0c01.pvom68yzawkpu9u5rsnsdbk9.ew2d7y2wg7oj0vwimr7g4ri0ga09h5zj0rhy23swswz79yua5y2tl8tj1yofvupu", pii.CatDynatraceSecret,
		"EZAKn1abdq5t8t81771y3wcw2ae7og0x6z9jm05z2v7fkxuxet6lhsv60k", pii.CatEasypostSecret,
		"EZTK7s6n6m0ldgwc0aat9atzgabml59r86jm0hjk76gbgek7531daujpwr", pii.CatEasypostSecret,
		"essu_VEiMIsY5xCGcyF4GefcFUWoA6m1g-Ifxc0nz_CfLWVtwXAlyuOqxqzIP2sfx", pii.CatElasticSecret,
		"EXOmDswpBcrQbvZjpTifmrI1YiJ", pii.CatExoscaleSecret,
		"EAAC3pkxwnzynt46no2iq2x8pz6nih6f8rybjtayfloumge9x6tmetfosizswz3irlbxw0b3pzwglshroczck1mtjyc9tlo57q1wahsc", pii.CatFacebookSecret,
		"figd_-DPHCUNWF0ZOR7FW12V626DN16I5MC9QL8KP8Q", pii.CatFigmaSecret,
		"FLWSECK_TEST-hbf335cg1ee7", pii.CatFlutterwaveSecret,
		"FLWSECK_TEST-7hha86e31eeh2d95fe64gd1a37gbb01g-X", pii.CatFlutterwaveSecret,
		"FlyV1 n5OUp47ulVJFB7=KqhN=3=YpBtLkgfKRDDySlvX+VNnpwXtodvRvgeHFNzGb_2_UmKSdUR4zLF49YbvAE,2SkJH,1rI4BWVwlA4s", pii.CatFlyIOSecret,
		"fio-u--m4f8u7318jz=fdv=t--0x4itv7bmo2fj_x9_0x7p-2zqholm9hoqgm7q5o93o8-", pii.CatFrameIOSecret,
		"ApiKey-v1 gcntfy-ShV-2d2e433e-c56f-24b1-c71b-106e934d263b-5ba0837b-bf1b-3ba3-178b-6e0e30f32854", pii.CatGCNotifySecret,
		"AQ.Ab8RN6lwSgi4BDrT_9EEJXy8U5ydJuqbnQFbVu7q7xtoAq9qdC", pii.CatGoogleGeminiSecret,
		"eyJrIjoif6FSSixiIhtREMZ2MukeSJmrufszqHrp9vfesTRa", pii.CatGrafanaSecret,
		"glc_A6z5ymVISmngrJYKWmt7t2I+oWjgCVieCbGz5ZkM", pii.CatGrafanaSecret,
		"glsa_MPuD9ImDFEz04kVuIAMRip4AoU7BNUU3_A83079eF", pii.CatGrafanaSecret,
		"pat.h1flQ-ZG7bdOOh1QulctAs.2bbdb4a78f19e8b8480f3b47.zWf7bNihdIGnJXlq8MxV", pii.CatHarnessSecret,
		"sat.twudSF4-BSX6BPdnbiZShD.cdc70808d77b6ad89f65f849.sfvaF35pkuRNM9CnLd4Y", pii.CatHarnessSecret,
		"hvb.7S_dTZAuS-Zut2x8AzFTmHJSp9KWBO3aMGrqvLm3733ymt0wtOC3XJtmxyu8y4_mcz4en3BNDwSVn9iuNtGmhgzFAkGGlH_xGaM7CVF0oCboQn5_cCASeOX0YCN1j438Jw00BgB7Fp", pii.CatVaultSecret,
		"hvs.kV3bbH_uy8qM3AsYaLcW4PDRiqgkKfLNuoliMdVwY1pp7M_4Xn3DWzP9WYJof5Hzt4XJUtv2tIEpc1ke4M4innZMcW", pii.CatVaultSecret,
		"HRKU-AAqK5UnThC3ej1hCjJclXObRHOG4up14htTd26E8ef_hS0msieJ-9Irs9ym1", pii.CatHerokuSecret,
		"ico-qre9cmGdAYJ8xrauScPDIsJvSA3VTrzB", pii.CatInfracostSecret,
		"ion_GXWqzhhTcqFRZScsHcoeuzLwhJArIXfhqPnXhVzYQB", pii.CatIonicSecret,
		"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", pii.CatLangSmithSecret,
		"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", pii.CatLangSmithSecret,
		"lip_SjVxYxdHFO2Ek0AG", pii.CatLichessSecret,
		"lin_api_5fn3dmv4d90i0djuvm7al8r7qfuyqt9z60dttpy1", pii.CatLinearSecret,
		"mlsn.2iQTMIDNipX7dqftlJX7zVMd6tjqDu", pii.CatMailersendSecret,
		"mercury_production_kar_Ea8k0UCROycSMtNzlndZ7ucN4NDLb2oHDI34E0mf_yrucrem", pii.CatMercurySecret,
		"mergify_application_key_XBV-clbUSaM7MZLG1cg42THRFU5ldoTnhpbTdyEp", pii.CatMergifySecret,
		"https://example.webhook.office.com/webhookb2/abcdef01-abcd-abcd-abcd-abcdef012345@abcdef01-abcd-abcd-abcd-abcdef012345/IncomingWebhook/abcdef0123456789abcdef0123456789/abcdef01-abcd-abcd-abcd-abcdef012345", pii.CatMicrosoftTeamsSecret,
		"sk-api-ZkzaQeeMBNG_adLVThD2yOlPKbdfHfJrMFbWmrK7XBo00ELfSVTsRaZcqIA9E-qIIZGu0LsU--RhmG7V3xmOIgdeZ6e-GyyrwzLdr2nAm_CO810m6SqbKty", pii.CatMinimaxSecret,
		"mdb_sa_sk_7ElqLiX40ePbFwXxiqTuVcsyn-oYUyBAWNf6gtMw", pii.CatMongoDBAtlasSecret,
		"napi_I7w5QqaEgnVcR9SXTqtorY8hzrD6pffXsBD414rHjYcTwg5JumvdC8UeIA875RJM", pii.CatNeonSecret,
		"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", pii.CatNotionSecret,
		"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", pii.CatNVIDIASecret,
		"API-ZZVZZ5VWLJ870SINVE0E6AP1ZN", pii.CatOctopusDeploySecret,
		"os_v2_app_rijophscysiyrernotgxfxbehuna5i4rd4cc5h6osvvonnsbolbr3xerfhzy2odxvqe6i355mvmhzksmeb4mmqmsbbewn2aqwkuwtgc", pii.CatOneSignalSecret,
		"api_live_ca.Wt1D6NrNTu8_Kro8QNgx", pii.CatOnfidoSecret,
		"api_live.atgCYj3xU3RRBObwDBL7", pii.CatOnfidoSecret,
		"api_live_us.FaJpr7_aAfatwNMQZ464", pii.CatOnfidoSecret,
		"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", pii.CatOpenRouterSecret,
		"sha256~lTmlEmlVJMNLs-QyakjfoBX60Akchdr3hxL4GrGMSdP", pii.CatOpenShiftSecret,
		"pdl_live_apikey_ygk2k4urpa08bvo8wvapvf8kgc_02UboVXEiH9dKNhDpqiP86_a76", pii.CatPaddleSecret,
		"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", pii.CatPerplexitySecret,
		"persona_production_-jeezteee8aexej9h56r", pii.CatPersonaSecret,
		"pcsk_6xcL5_GQTZassLcu4G37dVU1NBY1yOG2NzWqVRnA2ME5FKyqqlTqQLCJeG1DYQpFklODE", pii.CatPineconeSecret,
		"pina_lBiQtuWRvgvuVOfVkwDc", pii.CatPinterestSecret,
		"pscale_tkn_yCXUE8HagmWVEKd84_oo6_lZp_9wD24h", pii.CatPlanetscaleSecret,
		"pscale_oauth_pyiIU48ERhj-C9BWoh3hEv-OBmk9H76q", pii.CatPlanetscaleSecret,
		"pscale_pw_j5OmAJUip89Gx-b-d8eD=rUsXPfVxDc6", pii.CatPlanetscaleSecret,
		"polar_at_K5bEk4RYmoZIzDVBu9dI", pii.CatPolarSecret,
		"polar_oat_9v_bbY8Zn6icpE0Wr0Cv", pii.CatPolarSecret,
		"polar_pat_UeATh68xRhePj1TRRpHV", pii.CatPolarSecret,
		"phx_D2vk50GCtI0mg3ncLjKwr1jWMo5F-Vy3jGWxGE0UG", pii.CatPosthogSecret,
		"phc_jh8BPb48Rx7PD3lA0ZrDVUW-UqCBIoerZ1j86QTS3", pii.CatPosthogSecret,
		"PMAK-4f9af65d3010532fc8b0a72a-cafc1af1f21aadcc0e94c5437924bc2f2c", pii.CatPostmanSecret,
		"pnu_eNdSqiY3UvvGFjmM7JZdWj1SBysTbotZeZEg", pii.CatPrefectSecret,
		"prf_cli_ITY57dL83RBYbN6eh2qH", pii.CatProofSecret,
		"pul-a1a13080f032efb1843643b4c3b41ef18a04d593", pii.CatPulumiSecret,
		"ramp_sec_xEGqEnYbeEQzqgOcU2e8taxtXicx7u7UnDGxdFo7RIC286jI", pii.CatRampSecret,
		"rdme_e3ctev17fjzgdcsi7geuk80kply1vxhp39hfqy4ols3zmim5g6vpbq64juulvm0daowaqc", pii.CatReadmeSecret,
		"rpa_5C8UO2U04R8XTXnWZYSH8OA6rawox4", pii.CatRedirectPizzaSecret,
		"rnd_kw6P06pzD4uKwJ0TQgpUYb1TIPit", pii.CatRenderSecret,
		"rootly_4b5f4eb84980451cdd4aa15cc9b086396394535dc987a10055db87ae7cf35d1b", pii.CatRootlySecret,
		"rubygems_157f6c70434f9ae6ffad5bb0a08e0ee8a7e221708bca4f12", pii.CatRubygemsSecret,
		"rpa_O7LOLMH3NR16D5A2FE90JU3KN8V0PMOK0W1TTKN2FJMlUH", pii.CatRunpodSecret,
		"00gSLae1cxlfe8R!8Z8S-VdJtxIzMt2qtyT7AF9tz3mUASuzpcrUzXkORDp94_juCsp9OqgxhCvxIuBjqk_UwCJYaHRSndcH", pii.CatSalesforceSecret,
		"samsara_api_bQHuu66G8Jjj7Fx7Jb1MCvf2uY", pii.CatSamsaraSecret,
		"tk-us-2lwqMekhupecPvo7unxzTzUp3PY0G5D9dwvxtSh5e4b54cRY", pii.CatScalingoSecret,
		"sgp_g8J3D6yjhJfLsYKspAgz7ysg8A2zXatqMkYuqaV9e9l7nKU5YMR5Nyqyn0AlsUUp", pii.CatSegmentSecret,
		"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", pii.CatSentrySecret,
		"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", pii.CatSentrySecret,
		"sm_aat_eM1SBh1V5rGjBx3Q", pii.CatSettlemintSecret,
		"sm_pat_b9bdBNIPykxUxJiw", pii.CatSettlemintSecret,
		"sm_sat_65xqIjkkjjhLYZhk", pii.CatSettlemintSecret,
		"shippo_live_D466d53125abB1eBaBFBc3601E3bB9b24Bb7fAcC", pii.CatShippoSecret,
		"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", pii.CatShopifySecret,
		"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", pii.CatShopifySecret,
		"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", pii.CatShopifySecret,
		"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", pii.CatShopifySecret,
		"sgp_aec51B8e9Cdd0c9B_aebFcD6E562865AD4A3EeFF456B7C94e4a1197fb", pii.CatSourcegraphSecret,
		"EAAALJp5V8FWLLZeG9PB5TN6Ul", pii.CatSquareSecret,
		"sq0atp-UAD3GUcIhRU0e3NDRR8nx_", pii.CatSquareSecret,
		"sbp_gxmr5civ02s0jujlkwrdpvcld11mjx6hhr26zqbz", pii.CatSupabaseSecret,
		"sb_secret_xXwBvOpqQEYaCdlMZed8pPEpL6Peb4n", pii.CatSupabaseSecret,
		"tskey-api-1uBdOqze2fqewEmi897B", pii.CatTailscaleSecret,
		"eyJGw7dW8xUNh.4LnY2NvdW50X2lk7bAInRlbXBvcmFsLmlvILLICJrZXlfaWQiOiXvA306lsvVM-Ovlacxtq.jkKvOupRqOrU1CuczAUZ", pii.CatTemporalSecret,
		"tss_5uzhdW6VvHDwcpzF-8ZW", pii.CatThunderstoreSecret,
		"tgp_v1_IWXhRVolR9ORjnmZc4oQu-5VHNKESiIWCCd4L6eXZor", pii.CatTogetherAISecret,
		"unkey_mBIVXE6EBnuHDKsSqRT6", pii.CatUnkeySecret,
		"ucat_lv5tDzScoHZx0p3kIEJ5yxgZ", pii.CatUpcloudSecret,
		"vtwn_9Sw7w6ZcjifRnyFcMb4v", pii.CatValTownSecret,
		"vck_uQ8LCDTcKLYJRl14geoGM0nHOM2Ibj-lX3Ck6pmjKM-rdvOolnvf0je3", pii.CatVercelSecret,
		"vca_7gaRQBKgWuhYz7WMmNX81FYyy2ZvkzzyYxSr7EKeJWui68qnvXWVLTb9", pii.CatVercelSecret,
		"vcr_rNTScqkmKiayB3cw7B4wAMdzgeDM71Lf5kbHvEPC_SzT7iszUYLq3Ylp", pii.CatVercelSecret,
		"vci_GvNEqghj35577oOWOfQaRa-qYq59FWHW5JI5DC90L0dRG0ern_1yHBpE", pii.CatVercelSecret,
		"vcp_3ZcqBDMH2_-vMwoBxh0I-wN_MzN-3DO8mF1jA8fs7wNlGqnezD36S9mF", pii.CatVercelSecret,
		"waka_sajudpbkqpyo7ujgp27ywj2l9sxb7r5dhkaz", pii.CatWakatimeSecret,
		"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", pii.CatWeightsAndBiasesSecret,
		"wrkafe-eyJvTfCPZnA.npMk7U4NLszXUaJA.LzKQf6G05ODyrZe3s6uQxIl1klPb3p4kY9mwLP5I42g-hyNdU3YA9wrwPKyTn0Qk", pii.CatWorkatoSecret,
		"zpka_u23s4ilq6b0br85xn1b30mffotym0x31_bc37293e", pii.CatZuploSecret,
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

// The other half of the sample, and it had none until the generic rules were
// widened.
//
// The sample now shows three shapes that must come out *intact*, because that is
// what says where the frontier runs: `secret = config.password` names where a
// password is read from rather than being one, `token = user?.token2` is an
// optional access, and `secret_level: string` is a type. On the /test page they
// are the lines a reader compares the masked ones against.
//
// Shown and not asserted, they are decoration that could start being masked
// without anybody noticing — which is the same trap as an expectation derived from
// the detector, one step along: the page would still look like a reference while
// having stopped being one.
func TestSampleShowsWhereTheLimitRuns(t *testing.T) {
	for _, spec := range append([]string{"none"}, pii.LocaleCodes()...) {
		t.Run(spec, func(t *testing.T) {
			d := detectorFor(t, spec)
			sample := d.Sample()

			masked := map[string]bool{}
			for _, m := range d.Scan(sample) {
				masked[m.Value] = true
			}

			for _, w := range []struct{ value, why string }{
				{"config.password", "it names where the password was read from, so it is code reading one"},
				{"user?.token2", "optional chaining is code"},
				{"string", "a word the language reserved is not a password"},
			} {
				if !strings.Contains(sample, w.value) {
					t.Errorf("the sample no longer shows %q — the page has lost the line that "+
						"says where the frontier runs", w.value)
					continue
				}
				if masked[w.value] {
					t.Errorf("%q is masked, and the sample presents it as a value that is not: %s",
						w.value, w.why)
				}
			}
		})
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
