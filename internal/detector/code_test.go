package detector

import (
	"strings"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// The note that decided the threshold.
//
// It is five lines, two of them ending on a colon or a bracket, one carrying
// parentheses — enough to clear "at least a third of five". Loosening the rule
// that far buys eight points of recall on source files and reads this as code,
// which would stop the telephone number and the date being masked.
//
// It is the shape a person actually sends: a bulleted note, nothing but personal
// data. Code read as prose costs over-masking; prose read as code costs a value
// going out in clear, and only one of those is recoverable.
func TestABulletedNoteIsNotCode(t *testing.T) {
	const note = `Notes from the call:
- the client is Claire Moreau (06 12 34 56 78)
- invoice 2024-03-14 is unpaid
- her address changed to 12 rue de la Paix, 75002 Paris
- follow up on Friday`

	if looksLikeCode(note) {
		t.Fatal("a bulleted note read as code, so its personal data would go out in clear")
	}

	// The consequence, asserted rather than implied.
	d := New(Config{Locales: []string{"fr"}})
	var found []string
	for _, m := range d.Scan(note) {
		found = append(found, string(m.Category))
	}
	for _, want := range []string{"PHONE", "ADDRESS"} {
		if !containsString(found, want) {
			t.Errorf("%s was not masked in a note that is nothing but personal data: got %v",
				want, found)
		}
	}
}

// Prose stays prose whatever it is about.
//
// Each of these is multi-line, several carry punctuation, and one is a form-shaped
// record with a colon on every line — which is the shape closest to configuration
// that a person still writes by hand.
func TestProseIsNotReadAsCode(t *testing.T) {
	for name, text := range map[string]string{
		"a letter": "Bonjour,\n\nJe vous écris au sujet du dossier de Claire Moreau,\n" +
			"née le 23/02/2004 à Lyon.\nSon numéro est le 06 12 34 56 78.\n\nCordialement,",
		"a record laid out as fields": "Please review the customer record below:\n\n" +
			"Name: Claire Moreau\nDate of birth: 14/03/1987\n" +
			"Address: 10 Downing Street, London SW1A 2AA\nNHS: 943 476 5919\n\nIs anything missing?",
		"a short message": "Hi team,\n\nThe deploy went out at nine this morning\n" +
			"and the latency graph looks fine.\n\nThanks,\nSam",
		"the sample document": pii.Sample(pii.LocaleCodes()),
	} {
		t.Run(name, func(t *testing.T) {
			if looksLikeCode(text) {
				t.Error("read as code, so the values in it would stop being masked")
			}
		})
	}
}

// Source in three languages, read as source.
func TestSourceIsReadAsCode(t *testing.T) {
	for name, text := range map[string]string{
		"go": "func Forward(ctx context.Context, cfg Config) error {\n" +
			"\treq, err := http.NewRequest(http.MethodPost, cfg.URL, nil)\n" +
			"\tif err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}",
		"typescript": "export class BillingClient {\n" +
			"  constructor(private readonly config: BillingConfig) {}\n\n" +
			"  async charge(id: string): Promise<Response> {\n" +
			"    return fetch(this.config.url, { method: 'POST' });\n  }\n}",
		"a fenced block": "Here is the failing bit:\n\n```\nconst x = 1\n```\n\nWhat is wrong?",
		"a shell script": "#!/bin/sh\nset -eu\necho \"building\"\nmake build",
	} {
		t.Run(name, func(t *testing.T) {
			if !looksLikeCode(text) {
				t.Error("read as prose, so it goes on being over-masked")
			}
		})
	}
}

// Too short to judge is prose, which is the direction that keeps masking.
//
// A tool result of "ok", a one-line question and a single line of source cannot be
// told apart by shape, and a masking agent's tie-break is to mask.
func TestTooShortToJudgeIsProse(t *testing.T) {
	for _, text := range []string{
		"ok",
		"const port = 8080;",
		"Her number is 06 12 34 56 78",
		"a = 1;\nb = 2;",
	} {
		if looksLikeCode(text) {
			t.Errorf("%q was judged code on too little text", text)
		}
	}
}

// A date and a telephone number inside source are a changelog entry and a byte
// array; the same values in prose are somebody's.
func TestCodeRelaxesOnlyWhereTheShapeIsNoise(t *testing.T) {
	d := New(Config{Locales: []string{"fr", "us"}})

	const code = "// Release history, most recent first.\n" +
		"var releases = []Release{\n" +
		"\t{Version: \"1.4.0\", Date: \"2024-03-14\"},\n" +
		"\t{Version: \"1.3.0\", Date: \"2024-01-02\"},\n" +
		"}\n\n" +
		"var frameHeader = []byte{01, 02, 03, 04, 05}\n"

	if got := d.Scan(code); len(got) != 0 {
		t.Errorf("a changelog and a byte array were masked as personal data: %v", show(got))
	}

	// The same shapes, written the way a person writes them.
	const prose = "Bonjour,\n\nElle est née le 14/03/1987 et son numéro\n" +
		"est le 06 12 34 56 78. Merci de la rappeler.\n\nCordialement,"

	var cats []string
	for _, m := range d.Scan(prose) {
		cats = append(cats, string(m.Category))
	}
	for _, want := range []string{"DOB", "PHONE"} {
		if !containsString(cats, want) {
			t.Errorf("%s was not masked in prose: got %v", want, cats)
		}
	}
}

// The invariant the whole stage is built around.
//
// A coding agent reading a settings file is the moment a real key appears, and it
// is exactly the moment this signal says "code". If any credential were ever
// marked noisy, the relaxation would fire precisely where it must not.
//
// Asserted twice: over the registry, so a future entry cannot quietly set the flag
// on a secret, and through the engine, so the plumbing is exercised rather than
// the declaration alone.
func TestNoCredentialIsEverRelaxedInCode(t *testing.T) {
	for _, cat := range pii.Categories() {
		if pii.IsSecret(cat) && pii.NoisyInCode(cat) {
			t.Errorf("%s is a credential and is marked noisy in code, so a key in a "+
				"configuration file a coding agent just read would stop being masked", cat)
		}
	}

	// And through the engine, on something that unmistakably reads as code and
	// carries a key — a settings object of the kind a coding agent pastes whole.
	d := New(Config{Locales: []string{"fr"}})
	const settings = "export const config = {\n" +
		"  database: 'postgres://app:hunter2@db.internal:5432/app',\n" +
		"  anthropicKey: 'sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789',\n" +
		"  retries: 3,\n" +
		"};\n"

	if !looksLikeCode(settings) {
		t.Fatal("the fixture no longer reads as code, so this test proves nothing")
	}

	var secrets []string
	for _, m := range d.Scan(settings) {
		if pii.IsSecret(m.Category) {
			secrets = append(secrets, string(m.Category))
		}
	}
	if !containsString(secrets, "SECRET_ANTHROPIC_KEY") {
		t.Errorf("a key inside code stopped being masked, which is the one place the code "+
			"signal must change nothing: got %v", secrets)
	}
}

// EMAIL and IP_ADDRESS are deliberately not relaxed, and the reasons differ.
//
// An address in a fixture is still somebody's — measuring third-party packages
// found real maintainers' addresses in their metadata. And a private IP is a
// topology, which IPAddressCheck keeps on purpose; a pasted configuration is code,
// so marking IP_ADDRESS would undo that decision exactly where it was made.
func TestWhatIsNotRelaxedInCode(t *testing.T) {
	for _, cat := range []pii.Category{pii.CatEmail, pii.CatIPAddr, pii.CatIPv6} {
		if pii.NoisyInCode(cat) {
			t.Errorf("%s is marked noisy in code; see the registry entry for why it must not be", cat)
		}
	}

	d := New(Config{Locales: []string{"fr"}})
	const code = "const config = {\n" +
		"  maintainer: 'claire.moreau@example.fr',\n" +
		"  database: '10.4.2.17',\n" +
		"  retries: 3,\n" +
		"};\n"

	if !looksLikeCode(code) {
		t.Fatal("the fixture no longer reads as code, so this test proves nothing")
	}

	var cats []string
	for _, m := range d.Scan(code) {
		cats = append(cats, string(m.Category))
	}
	for _, want := range []string{"EMAIL", "IP_ADDRESS"} {
		if !containsString(cats, want) {
			t.Errorf("%s stopped being masked inside code: got %v", want, cats)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func show(ms []Match) string {
	var out []string
	for _, m := range ms {
		out = append(out, string(m.Category)+"="+m.Value)
	}
	return strings.Join(out, ", ")
}
