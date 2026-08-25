package vault

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func newTestVault(t *testing.T) (*Vault, *Memory) {
	t.Helper()

	store := NewMemory()
	v, err := New(store, nil, DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	return v, store
}

func TestSaveAndLoad(t *testing.T) {
	v, _ := newTestVault(t)

	want := map[string]string{
		"[EMAIL_1]": "claire@example.fr",
		"[NIR_1]":   "184037511600176",
	}
	if err := v.Save("s1", want); err != nil {
		t.Fatal(err)
	}

	got := v.Load("s1")
	if len(got) != len(want) {
		t.Fatalf("loaded %d entries, want %d: %v", len(got), len(want), got)
	}
	for token, original := range want {
		if got[token] != original {
			t.Errorf("%s = %q, want %q", token, got[token], original)
		}
	}
}

// A session that was never written to must come back empty rather than as
// somebody else's mapping.
func TestLoadUnknownSession(t *testing.T) {
	v, _ := newTestVault(t)

	if got := v.Load("never-used"); len(got) != 0 {
		t.Errorf("an unknown session returned %v", got)
	}
}

func TestSessionsDoNotSeeEachOther(t *testing.T) {
	v, _ := newTestVault(t)

	if err := v.Save("alice", map[string]string{"[EMAIL_1]": "alice@example.fr"}); err != nil {
		t.Fatal(err)
	}
	if err := v.Save("bob", map[string]string{"[EMAIL_2]": "bob@example.fr"}); err != nil {
		t.Fatal(err)
	}

	if got := v.Load("alice"); got["[EMAIL_2]"] != "" {
		t.Errorf("alice can read bob's mapping: %v", got)
	}
	if got := v.Load("bob"); got["[EMAIL_1]"] != "" {
		t.Errorf("bob can read alice's mapping: %v", got)
	}
}

// Later turns add to a session rather than replacing it, or a conversation would
// lose the values it established on its first turn.
func TestSaveMergesIntoASession(t *testing.T) {
	v, _ := newTestVault(t)

	if err := v.Save("s1", map[string]string{"[EMAIL_1]": "claire@example.fr"}); err != nil {
		t.Fatal(err)
	}
	if err := v.Save("s1", map[string]string{"[NIR_1]": "184037511600176"}); err != nil {
		t.Fatal(err)
	}

	got := v.Load("s1")
	if len(got) != 2 {
		t.Errorf("the second save replaced the first: %v", got)
	}
}

// The originals must not be readable in the store. It is a narrow guarantee —
// the store is in memory, so this is about a core dump rather than about anything
// with access to the process — but it is the guarantee that lets the mapping move
// to a shared store later.
func TestOriginalsAreNotReadableInTheStore(t *testing.T) {
	v, store := newTestVault(t)

	const original = "claire@example.fr"
	if err := v.Save("s1", map[string]string{"[EMAIL_1]": original}); err != nil {
		t.Fatal(err)
	}

	for token, stored := range store.Load("s1") {
		if strings.Contains(stored, original) {
			t.Errorf("%s holds the original in clear: %q", token, stored)
		}
	}
}

// Two tokens for the same original must not seal to the same bytes. Identical
// ciphertext would tell anyone reading the store that two tokens are the same
// person, which is most of what the mapping was protecting.
func TestIdenticalOriginalsSealDifferently(t *testing.T) {
	v, store := newTestVault(t)

	if err := v.Save("s1", map[string]string{
		"[EMAIL_1]": "claire@example.fr",
		"[EMAIL_2]": "claire@example.fr",
	}); err != nil {
		t.Fatal(err)
	}

	stored := store.Load("s1")
	if stored["[EMAIL_1]"] == stored["[EMAIL_2]"] {
		t.Error("the same original sealed to identical bytes under two tokens")
	}
}

// An entry sealed under another key is dropped rather than reported. It means the
// process restarted under a new ephemeral key, and the right answer is that the
// token is simply unknown — not that the request fails over bookkeeping.
func TestEntriesSealedUnderAnotherKeyAreDropped(t *testing.T) {
	store := NewMemory()

	first, err := New(store, testKey(t), DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Save("s1", map[string]string{"[EMAIL_1]": "claire@example.fr"}); err != nil {
		t.Fatal(err)
	}

	second, err := New(store, testKey(t), DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Load("s1"); len(got) != 0 {
		t.Errorf("a mapping sealed under another key was read as %v", got)
	}
}

func TestKeyLength(t *testing.T) {
	if _, err := New(NewMemory(), make([]byte, 16), DefaultTTL); err == nil {
		t.Error("a 16-byte key was accepted for AES-256")
	}
	if _, err := New(NewMemory(), make([]byte, 32), DefaultTTL); err != nil {
		t.Errorf("a 32-byte key was refused: %v", err)
	}
	// Nil means "generate one", which is what keeps the encrypting path the only
	// path there is.
	if _, err := New(NewMemory(), nil, DefaultTTL); err != nil {
		t.Errorf("a generated key was refused: %v", err)
	}
}

// The lifetime is a privacy setting as much as a cache setting: it is the window
// in which the originals exist at all.
func TestSessionsExpire(t *testing.T) {
	store := NewMemory()
	v, err := New(store, nil, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	if err := v.Save("s1", map[string]string{"[EMAIL_1]": "claire@example.fr"}); err != nil {
		t.Fatal(err)
	}
	if got := v.Load("s1"); len(got) != 1 {
		t.Fatalf("the mapping was not stored: %v", got)
	}

	// Reading through the store's own clock rather than sleeping on the test's:
	// the expiry is what is under test, not the scheduler.
	store.mu.Lock()
	store.sessions["s1"].expires = time.Now().Add(-time.Second)
	store.mu.Unlock()

	if got := v.Load("s1"); len(got) != 0 {
		t.Errorf("an expired session was still readable: %v", got)
	}
	if got := store.Sessions(); got != 0 {
		t.Errorf("the store reports %d live sessions, want 0", got)
	}
}

// A write is what purges expired sessions, so a busy process keeps the map
// bounded without a background sweep.
func TestWritingPurgesExpiredSessions(t *testing.T) {
	store := NewMemory()

	store.Merge("old", map[string]string{"[EMAIL_1]": "x"}, time.Hour)
	store.mu.Lock()
	store.sessions["old"].expires = time.Now().Add(-time.Second)
	store.mu.Unlock()

	store.Merge("new", map[string]string{"[EMAIL_2]": "y"}, time.Hour)

	store.mu.Lock()
	_, stillThere := store.sessions["old"]
	store.mu.Unlock()
	if stillThere {
		t.Error("an expired session survived a write")
	}
	if got := store.Sessions(); got != 1 {
		t.Errorf("the store reports %d live sessions, want 1", got)
	}
}

func TestSaveNothingIsNotAnError(t *testing.T) {
	v, _ := newTestVault(t)

	if err := v.Save("s1", nil); err != nil {
		t.Errorf("saving an empty mapping failed: %v", err)
	}
	if got := v.Load("s1"); len(got) != 0 {
		t.Errorf("saving nothing created a session: %v", got)
	}
}

func testKey(t *testing.T) []byte {
	t.Helper()

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}
