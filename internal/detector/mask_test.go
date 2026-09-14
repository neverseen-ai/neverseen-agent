package detector

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// Masking is only half the product. The other half is that the caller gets its
// own data back, and these tests are about the round trip rather than about
// either direction on its own.

func TestMaskAndUnmaskRoundTrip(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}})

	const original = "Écrire à claire@example.fr, NIR 184037511600176, tel 06 12 34 56 78."

	masked, minted, replaced := d.MaskOnce(original)
	if replaced != 3 {
		t.Fatalf("replaced %d values, want 3: %q", replaced, masked)
	}
	for _, value := range []string{"claire@example.fr", "184037511600176", "06 12 34 56 78"} {
		if strings.Contains(masked, value) {
			t.Errorf("%q survived masking: %q", value, masked)
		}
	}

	if got := Unmask(masked, minted); got != original {
		t.Errorf("the round trip did not return the original:\n got %q\nwant %q", got, original)
	}
}

// The second tier of vendor prefixes, through the whole round trip rather than
// through Scan alone. The corpus says the span is right and the notation table
// says the category is; neither says the token renders under the vendor's own
// prefix, or that what went out comes back.
//
// Cerebras is here for a second reason. "csk-<48>" contains openAILegacyRe's
// "sk-<20,>", which claimed it from offset 1 and rendered it as
// "c[OPENAI_KEY_1]" — a fragment masked, the first character in clear. What
// stops that is the tier scoring 98, equal to OpenAI's, so arbitration falls
// through to the longer span. A row that only checked "something was masked"
// would have passed over it.
func TestVendorPrefixTokensRoundTrip(t *testing.T) {
	d := New(Config{})

	for _, tc := range []struct {
		value string
		token string
	}{
		{"figd_-DPHCUNWF0ZOR7FW12V626DN16I5MC9QL8KP8Q", "[FIGMA_TOKEN_1]"},
		{"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", "[NOTION_TOKEN_1]"},
		{"sbp_a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0", "[SUPABASE_TOKEN_1]"},
		{"csk-i5skoewqkur3jq64nq6puxcmlzkruykqh7dx297gq8zxqyxj", "[CEREBRAS_TOKEN_1]"},
	} {
		t.Run(tc.token, func(t *testing.T) {
			original := "Voici la clé " + tc.value + " à révoquer."

			masked, minted, replaced := d.MaskOnce(original)
			if replaced != 1 {
				t.Fatalf("replaced %d values, want 1: %q", replaced, masked)
			}
			// The whole token, under the vendor's prefix: a span short by one
			// character reads as a mask that worked and leaks the rest.
			if want := "Voici la clé " + tc.token + " à révoquer."; masked != want {
				t.Errorf("masked to %q, want %q", masked, want)
			}
			if got := Unmask(masked, minted); got != original {
				t.Errorf("the round trip did not return the original:\n got %q\nwant %q", got, original)
			}
		})
	}
}

// One value, one token — for as long as the session lives. Without it a model is
// told about three different people where the text named one, and the vault
// fills up with duplicates of the same person.
func TestMaskGivesOneValueOneToken(t *testing.T) {
	d := New(Config{})

	pass := d.NewPass(nil)
	masked, replaced := d.Mask("write to ab@x.fr, then to ab@x.fr again, and to cd@y.fr", pass)

	if replaced != 3 {
		t.Fatalf("replaced %d values, want 3 (repeats included)", replaced)
	}
	if minted := pass.Minted(); len(minted) != 2 {
		t.Errorf("minted %d entries, want 2 — the repeat must reuse its token: %v", len(minted), minted)
	}

	tokens := pii.FindTokens(masked)
	if len(tokens) != 3 {
		t.Fatalf("the masked text carries %d tokens, want 3: %q", len(tokens), masked)
	}
	if tokens[0] != tokens[1] {
		t.Errorf("the same address got two tokens, %q and %q", tokens[0], tokens[1])
	}
	if tokens[1] == tokens[2] {
		t.Errorf("two different addresses share the token %q", tokens[1])
	}
}

// A conversation is several requests. A value seen on an earlier turn has to keep
// the token it had, which is what seeding a pass from the session's mapping does.
func TestMaskReusesASessionsExistingTokens(t *testing.T) {
	d := New(Config{})

	first := d.NewPass(nil)
	maskedFirst, _ := d.Mask("write to claire@example.fr", first)
	stored := first.Minted()

	second := d.NewPass(stored)
	maskedSecond, _ := d.Mask("remind claire@example.fr tomorrow", second)

	tokenFirst := pii.FindTokens(maskedFirst)
	tokenSecond := pii.FindTokens(maskedSecond)
	if len(tokenFirst) != 1 || len(tokenSecond) != 1 {
		t.Fatalf("expected one token per turn, got %v and %v", tokenFirst, tokenSecond)
	}
	if tokenFirst[0] != tokenSecond[0] {
		t.Errorf("the second turn renumbered the value: %q then %q", tokenFirst[0], tokenSecond[0])
	}
	if len(second.Minted()) != 0 {
		t.Errorf("the second turn minted %v, but the value was already known", second.Minted())
	}
}

// A conversation replays its whole history every turn, and a provider bills less
// and answers faster when a request's prefix repeats the previous one's. So the
// stability asserted above has to hold at the scale of a body, not just of a
// value: re-masking the earlier turn on the next one must come out byte for byte
// the same, and the values new to the later turn must take indices after it
// rather than renumber anything inside it.
//
// The single-value half is TestMaskReusesASessionsExistingTokens. This is the
// half the cached prefix actually rests on — see the request-path invariants in
// CLAUDE.md. Both substitution modes, because a stand-in is rendered from the
// index and a token is not, so they can fail apart.
func TestMaskRepeatsTheEarlierTurnByteForByte(t *testing.T) {
	const (
		firstTurn = "write to claire@example.fr, copy ab@x.fr, and call claire@example.fr back\n"
		added     = "then add cd@y.fr and copy ab@x.fr once more"
	)

	for _, mode := range []Substitution{SubstitutionToken, SubstitutionFake} {
		t.Run(mode.String(), func(t *testing.T) {
			d := New(Config{Locales: []string{"en"}, Substitution: mode})

			first := d.NewPass(nil)
			maskedFirst, _ := d.Mask(firstTurn, first)
			stored := first.Minted()
			if len(stored) == 0 {
				t.Fatal("the first turn masked nothing, so the test asserts nothing")
			}

			// The second turn is the first one replayed with more after it, which
			// is the shape of every turn but the first.
			second := d.NewPass(stored)
			maskedSecond, _ := d.Mask(firstTurn+added, second)

			if !strings.HasPrefix(maskedSecond, maskedFirst) {
				t.Errorf("the second turn rewrote the first one, forfeiting the cached prefix from that point on\n first:  %q\n second: %q",
					maskedFirst, maskedSecond)
			}
			if len(second.Minted()) == 0 {
				t.Error("the second turn minted nothing, so it never exercised minting beside a seeded mapping")
			}
			for masked, original := range stored {
				if again, ok := second.Minted()[masked]; ok {
					t.Errorf("%q was minted again on the second turn, for %q then %q", masked, original, again)
				}
			}
		})
	}
}

func TestParseSubstitution(t *testing.T) {
	tests := []struct {
		spec    string
		want    Substitution
		wantErr bool
	}{
		{spec: "", want: SubstitutionToken},
		{spec: "token", want: SubstitutionToken},
		{spec: "fake", want: SubstitutionFake},
		{spec: " FAKE ", want: SubstitutionFake},
		{spec: "redact", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.spec), func(t *testing.T) {
			got, err := ParseSubstitution(tt.spec)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseSubstitution(%q) accepted an unknown mode", tt.spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSubstitution(%q): %v", tt.spec, err)
			}
			if got != tt.want {
				t.Errorf("ParseSubstitution(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestFakeMode(t *testing.T) {
	d := New(Config{Locales: []string{"fr"}, Substitution: SubstitutionFake})

	t.Run("a stand-in replaces the value and is not a bracket token", func(t *testing.T) {
		masked, _, replaced := d.MaskOnce("écrire à claire@example.fr")
		if replaced != 1 {
			t.Fatalf("replaced %d values, want 1", replaced)
		}
		if strings.Contains(masked, "claire@example.fr") {
			t.Errorf("the original survived: %q", masked)
		}
		if tokens := pii.FindTokens(masked); len(tokens) != 0 {
			t.Errorf("fake mode produced a bracket token %v: %q", tokens, masked)
		}
		if !strings.Contains(masked, "@example.org") {
			t.Errorf("the stand-in does not read as an address: %q", masked)
		}
	})

	t.Run("a credential never gets a stand-in", func(t *testing.T) {
		// A stand-in that looks like a working key is a thing somebody will try
		// to use, so credentials take the bracket-token fallback by design.
		masked, _, replaced := d.MaskOnce("export KEY=sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789")
		if replaced == 0 {
			t.Fatal("the credential was not masked at all")
		}
		if len(pii.FindTokens(masked)) == 0 {
			t.Errorf("the credential got a stand-in instead of a token: %q", masked)
		}
	})

	t.Run("a stand-in is expanded on the way back", func(t *testing.T) {
		// Fake mode round-trips, like token mode. What keeps that safe is one step
		// upstream, not here: a credential never gets a stand-in, so a non-token
		// key cannot be one and the value-matching path cannot expand a secret.
		masked, minted, _ := d.MaskOnce("écrire à claire@example.fr")

		if got := Unmask(masked, minted); got != "écrire à claire@example.fr" {
			t.Errorf("Unmask(%q) = %q, want the original text", masked, got)
		}
	})

	t.Run("a credential still travels as a token, and only a token expands it", func(t *testing.T) {
		const key = "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
		masked, minted, _ := d.MaskOnce("export KEY=" + key)

		// The guard the value path relies on: every key in the mapping for a
		// credential is a bracket token, so nothing else can reach it.
		for mask := range minted {
			if !pii.IsToken(mask) {
				t.Errorf("a credential was mapped to a non-token %q", mask)
			}
		}
		if got := Unmask(masked, minted); got != "export KEY="+key {
			t.Errorf("Unmask(%q) = %q, want the original", masked, got)
		}
	})

	t.Run("a stand-in inside another is expanded once, widest first", func(t *testing.T) {
		// Two entries where one contains the other: expanded in turn rather than
		// in one walk, the shorter one lands inside text that has already been
		// expanded and puts a value inside a value.
		known := map[string]string{
			"1 rue de l'Exemple, 99000 Villeneuve": "10 rue jean jaures, 29200 BREST",
			"99000":                                "29200",
		}
		const answer = "il habite 1 rue de l'Exemple, 99000 Villeneuve"

		if got := Unmask(answer, known); got != "il habite 10 rue jean jaures, 29200 BREST" {
			t.Errorf("Unmask = %q, want the address expanded once", got)
		}
	})

	t.Run("a token this process never minted is left alone", func(t *testing.T) {
		known := map[string]string{"contact2@example.org": "claire@example.fr"}
		const answer = "see [EMAIL_9] and contact2@example.org"

		if got := Unmask(answer, known); got != "see [EMAIL_9] and claire@example.fr" {
			t.Errorf("Unmask = %q, want the unknown token untouched", got)
		}
	})
}

// The held-back tail has to cover a stand-in as well as a token, or a streamed
// answer in fake mode restores nothing while a buffered one round-trips.
func TestTailLenCoversBothShapes(t *testing.T) {
	known := map[string]string{
		"[EMAIL_1]":            "claire@example.fr",
		"contact2@example.org": "andre@example.fr",
	}

	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "nothing pending", text: "bonjour", want: 0},
		{name: "the start of a token", text: "écrire à [EMA", want: 4},
		{name: "the start of a stand-in", text: "écrire à contact2@exa", want: 12},
		{name: "a whole stand-in is not held back", text: "écrire à contact2@example.org", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TailLen(tt.text, known); got != tt.want {
				t.Errorf("TailLen(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

// The invariant the indexed design exists for: two different originals never
// share a mask. Exercised across goroutines, because the counters are the only
// state a Detector shares between concurrent requests.
func TestConcurrentPassesNeverShareAMask(t *testing.T) {
	d := New(Config{})

	const workers, each = 8, 25

	var wg sync.WaitGroup
	results := make(chan map[string]string, workers)

	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			pass := d.NewPass(nil)
			for i := range each {
				d.Mask(fmt.Sprintf("write to user%d-%d@example.fr", w, i), pass)
			}
			results <- pass.Minted()
		}(w)
	}
	wg.Wait()
	close(results)

	seenMask := map[string]string{} // mask -> original
	for minted := range results {
		for mask, original := range minted {
			if other, taken := seenMask[mask]; taken && other != original {
				t.Fatalf("mask %q was minted for both %q and %q", mask, other, original)
			}
			seenMask[mask] = original
		}
	}
	if len(seenMask) != workers*each {
		t.Errorf("minted %d distinct masks, want %d", len(seenMask), workers*each)
	}
}

// Unmask leaves alone what it does not know, rather than inventing something for
// it. A token from an expired session means nothing here, and putting a value in
// front of the caller that no data supports would be worse than leaving the
// bookkeeping visible.
func TestUnmaskLeavesUnknownTokensAlone(t *testing.T) {
	const text = "the answer mentions [EMAIL_9] and [NIR_4]"

	if got := Unmask(text, nil); got != text {
		t.Errorf("Unmask with no mapping changed the text: %q", got)
	}
	if got := Unmask(text, map[string]string{"[EMAIL_9]": "claire@example.fr"}); got !=
		"the answer mentions claire@example.fr and [NIR_4]" {
		t.Errorf("Unmask did not expand exactly the known token: %q", got)
	}
}

// A secret masked inside a JSON string must not eat the escaping backslash of the
// quote that closes that string. A tool call's arguments are JSON encoded inside
// a JSON body, so "PASSWORD=secret\"" runs the value to the \" that ends it. A
// value class that admitted the backslash masked `secret\` and left `\"` as a
// bare `"`, turning the arguments into JSON the provider rejects with a 400.
func TestMaskKeepsJSONEscapingIntact(t *testing.T) {
	d := New(Config{})

	// The decoded arguments of an exec_command tool call: a shell command that
	// echoes a password, JSON-escaped once because it sits in a JSON string.
	args := `{"cmd": "echo \"PASSWORD=Sup3rS3cr3tValue123\""}`
	if !json.Valid([]byte(args)) {
		t.Fatalf("the test input is not valid JSON to begin with")
	}

	masked, _, replaced := d.MaskOnce(args)

	if replaced == 0 {
		t.Fatalf("the password was not masked at all:\n%s", masked)
	}
	if strings.Contains(masked, "Sup3rS3cr3tValue123") {
		t.Errorf("the secret survived in clear:\n%s", masked)
	}
	if !json.Valid([]byte(masked)) {
		t.Errorf("masking broke the JSON escaping:\n%s", masked)
	}
}
