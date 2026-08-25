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
			for _, p := range d.patterns {
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
				"23/02/2004", pii.CatDOB,
				"23-02-2004", pii.CatDOB,
				"23.02.2004", pii.CatDOB,
				"23 03 2004", pii.CatDOB,
				"23 février 2004", pii.CatDOB,
				"1er mars 2004", pii.CatDOB,
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
		"sk-abcdefghijklmnopqrstuvwxyz0123", pii.CatOpenAIKey,
		"sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", pii.CatAnthropicKey,
		"AIzaabcdefghijklmnopqrstuvwxyzABCDEFGHI", pii.CatGoogleKey,
		"AKIAIOSFODNN7EXAMPLE", pii.CatAWSAccessKey,
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN", pii.CatAWSSecretKey,
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789", pii.CatGitHubToken,
		"gho_0123456789abcdefghijklmnopqrstuvwxyz", pii.CatGitHubToken,
		"glpat-abcdefghijklmnopqrst", pii.CatGitLabToken,
		"xoxb-0123456789-a", pii.CatSlackToken,
		"xapp-0123456789-z", pii.CatSlackToken,
		"sk_live_abcdefghijklmnopqrstuvwx", pii.CatStripeKey,
		"SG.abcdefghijklmnopqrstuv.abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", pii.CatSendGridKey,
		"SK0123456789abcdef0123456789abcdef", pii.CatTwilioKey,
		"npm_abcdefghijklmnopqrstuvwx", pii.CatNPMToken,
		"pypi-abcdefghijklmnopqrstuvwx", pii.CatPyPIToken,
		"dckr_pat_abcdefghijklmnopqrstuvwx", pii.CatDockerToken,
		"hf_abcdefghijklmnopqrstuvwx", pii.CatHFToken,
		"r8_abcdefghijklmnopqrstuvwx", pii.CatReplicateToken,
		"-----BEGIN OPENSSH PRIVATE KEY-----", pii.CatPEMKey,
		"eyJabcdefghijkl.eyJabcdefghijklmn.abcdefghijklmnopqrst", pii.CatJWT,
		"postgres://admin:s3cr3t@db.example.com:5432/app", pii.CatConnStr,
		"amqp://guest:gu3st@broker.internal:5672/", pii.CatConnStr,
		"redis://:p4ssonly@redis.internal:6379", pii.CatConnStr,
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
