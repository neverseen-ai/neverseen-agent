package detector

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
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

// One value, one token — for as long as the session lives. Without it a model is
// told about three different people where the text named one, and the vault
// fills up with duplicates of the same person.
func TestMaskGivesOneValueOneToken(t *testing.T) {
	d := New(Config{})

	pass := d.NewPass(nil)
	masked, replaced := d.Mask("write to a@x.fr, then to a@x.fr again, and to b@y.fr", pass)

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

	t.Run("a stand-in is not expanded on the way back", func(t *testing.T) {
		// Fake mode is one-way at the proxy on purpose: the point of a stand-in
		// is that it reads as prose, and expanding it would undo what the
		// deployment asked for. The same filter is what stops a masked
		// credential from being turned back into a live secret.
		masked, minted, _ := d.MaskOnce("écrire à claire@example.fr")

		if got := Unmask(masked, minted); got != masked {
			t.Errorf("a stand-in was expanded back: %q became %q", masked, got)
		}
		// The mapping still holds the pair, so a caller that wants the reverse
		// can do it; the proxy simply does not.
		if len(minted) != 1 {
			t.Errorf("the mapping should still record the pair, got %v", minted)
		}
	})
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
