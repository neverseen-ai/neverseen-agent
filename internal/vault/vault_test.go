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

	// Dropped rather than merely reported empty: the originals are what the
	// lifetime is about, and a session kept in the map with an expiry in the past
	// is the mapping still in memory.
	store.mu.Lock()
	_, stillThere := store.sessions["s1"]
	store.mu.Unlock()
	if stillThere {
		t.Error("an expired session was read as empty but kept in the store")
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
	_, oldThere := store.sessions["old"]
	_, newThere := store.sessions["new"]
	live := len(store.sessions)
	store.mu.Unlock()

	if oldThere {
		t.Error("an expired session survived a write")
	}
	// The other half, because a purge that took everything with it would pass the
	// assertion above: the session the write was for has to be there afterwards.
	if !newThere || live != 1 {
		t.Errorf("the store holds %d session(s) after the write, want only \"new\"", live)
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

// The lifetime runs from the last use, not from the last mint.
//
// A conversation that introduced its values early mints nothing afterwards, and
// every later request saves an empty set. Refreshed only on a mint, the mapping
// expired DefaultTTL after the first request of a conversation still in progress,
// and the answer reached the caller with raw tokens.
func TestSavingNothingKeepsTheSessionAlive(t *testing.T) {
	v, store := newTestVault(t)

	if err := v.Save("s1", map[string]string{"[EMAIL_1]": "claire@example.fr"}); err != nil {
		t.Fatal(err)
	}
	// The mint is old: the session is a second from expiring on its own clock.
	store.mu.Lock()
	store.sessions["s1"].expires = time.Now().Add(time.Second)
	store.mu.Unlock()

	// A request that reused every value and minted nothing.
	before := time.Now()
	if err := v.Save("s1", nil); err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	s, ok := store.sessions["s1"]
	var expires time.Time
	if ok {
		expires = s.expires
	}
	store.mu.Unlock()
	if !ok {
		t.Fatal("saving nothing dropped the session")
	}
	if expires.Before(before.Add(DefaultTTL)) {
		t.Errorf("the session expires at %v, want a full lifetime from %v: an empty save did not refresh it",
			expires, before)
	}
	if got := v.Load("s1"); got["[EMAIL_1]"] != "claire@example.fr" {
		t.Errorf("the mapping is gone after an empty save: %v", got)
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

// Forget is what a change of substitution mode costs, and the cost has to be real.
//
// The mapping is consulted before the mode is, so a value already seen keeps the
// shape it was first given. That is what lets a conversation straddling the change
// round-trip — and it is also why a click on "fake" changed nothing anybody could
// observe until this existed: on a workstation where nothing sends a session header,
// one unnamed session carries every value the agent has handled since it started.
//
// So the trade is that replacements minted before the change stop being restored.
// Asserted rather than described, because a Forget that quietly kept a session would
// put the control back to appearing not to work.
func TestForgetDropsEveryMapping(t *testing.T) {
	store := NewMemory()
	v, err := New(store, nil, DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}

	if err := v.Save("s1", map[string]string{"[EMAIL_1]": "claire@example.fr"}); err != nil {
		t.Fatal(err)
	}
	if err := v.Save("s2", map[string]string{"[IBAN_1]": "FR1420041010050500013M02606"}); err != nil {
		t.Fatal(err)
	}

	v.Forget()

	// Every session, not only the one that was current: the mode is one setting for
	// the whole agent, and a purge that spared a session would leave that
	// conversation minting the old shape.
	for _, session := range []string{"s1", "s2"} {
		if got := v.Load(session); len(got) != 0 {
			t.Errorf("%s survived Forget: %v", session, got)
		}
	}

	// Still usable afterwards. Forget replaces the map rather than nilling it, so
	// the next exchange mints into a live store rather than panicking on the first
	// write.
	if err := v.Save("s1", map[string]string{"[EMAIL_2]": "paul@example.fr"}); err != nil {
		t.Fatalf("the vault was unusable after Forget: %v", err)
	}
	if got := v.Load("s1"); len(got) != 1 {
		t.Errorf("a value minted after Forget was not stored: %v", got)
	}
}

// A value keeps its name after its session's mapping has expired.
//
// The model remembers a token longer than the mapping does — inside a thinking
// block's signature, which the agent cannot read — so a pause that expired the
// mapping and renamed the value on the next turn left the model writing a name
// nothing expanded any more.
func TestRecallOutlivesTheMapping(t *testing.T) {
	v, store := newTestVault(t)

	if err := v.Save("s1", map[string]string{"[PLATE_381]": "AB-123-CD"}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.sessions["s1"].expires = time.Now().Add(-time.Second)
	store.mu.Unlock()

	// The precondition, so the assertion below is about Recall and not about a
	// mapping that never expired.
	if got := v.Load("s1"); len(got) != 0 {
		t.Fatalf("the mapping did not expire: %v", got)
	}
	if got, ok := v.Recall("s1", "AB-123-CD"); !ok || got != "[PLATE_381]" {
		t.Errorf("Recall = %q, %v after the mapping expired, want [PLATE_381]", got, ok)
	}

	// Per session, as the mapping is: a name recalled in another conversation
	// would tell the provider the two are about the same vehicle.
	if got, ok := v.Recall("s2", "AB-123-CD"); ok {
		t.Errorf("another session recalled %q", got)
	}
	if got, ok := v.Recall("s1", "EF-456-GH"); ok {
		t.Errorf("a value never saved recalled %q", got)
	}
}

// What Recall keeps past the mapping's lifetime must not be the value, or the
// lifetime would stop being the window the originals exist in memory.
func TestRecallDoesNotHoldTheValue(t *testing.T) {
	v, _ := newTestVault(t)

	if err := v.Save("s1", map[string]string{"[PLATE_381]": "AB-123-CD"}); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	for fingerprint, n := range v.names {
		if strings.Contains(fingerprint, "AB-123-CD") || strings.Contains(n.masked, "AB-123-CD") {
			t.Errorf("the names hold the value in clear: %q → %q", fingerprint, n.masked)
		}
	}
	v.mu.Unlock()

	// Keyed, not merely hashed: a plate is a small enough space to hash every
	// candidate of, so an unkeyed digest would be the value by another name. Only a
	// key of the vault's own makes two vaults disagree about the same value.
	other, _ := newTestVault(t)
	if v.fingerprint("s1", "AB-123-CD") == other.fingerprint("s1", "AB-123-CD") {
		t.Error("two vaults fingerprint a value alike: the fingerprint is not keyed")
	}
}

// The names of a session idle for namesTTL are dropped, because a session can be a
// single request and a book that kept every one would grow with the traffic for the
// life of the process. A session still making requests keeps every name, however
// long ago it was minted: a value its live mapping holds is reused without Recall
// being asked, so nothing but the session's own activity says the name is in use.
func TestRecallForgetsTheNamesOfAnIdleSession(t *testing.T) {
	v, _ := newTestVault(t)

	if err := v.Save("idle", map[string]string{"[PLATE_381]": "AB-123-CD"}); err != nil {
		t.Fatal(err)
	}
	if err := v.Save("busy", map[string]string{"[PLATE_382]": "EF-456-GH"}); err != nil {
		t.Fatal(err)
	}

	// Both sessions last seen past namesTTL, and the sweep due.
	v.mu.Lock()
	for session := range v.active {
		v.active[session] = time.Now().Add(-namesTTL - time.Hour)
	}
	v.swept = time.Time{}
	v.mu.Unlock()

	// "busy" makes a request that mints nothing — every value already in its
	// mapping — and that alone keeps its names.
	if err := v.Save("busy", nil); err != nil {
		t.Fatal(err)
	}

	if got, ok := v.Recall("idle", "AB-123-CD"); ok {
		t.Errorf("a session idle for longer than namesTTL kept its name: %q", got)
	}
	if got, ok := v.Recall("busy", "EF-456-GH"); !ok || got != "[PLATE_382]" {
		t.Errorf("a session still in use lost its name: %q, %v", got, ok)
	}
	v.mu.Lock()
	_, idleKept := v.active["idle"]
	v.mu.Unlock()
	if idleKept {
		t.Error("the idle session was swept from the names but kept in active")
	}
}

// A mode change drops the names with the mapping, or a value seen before the change
// would be handed back the shape the operator just switched away from.
func TestForgetDropsWhatRecallRemembers(t *testing.T) {
	v, _ := newTestVault(t)

	if err := v.Save("s1", map[string]string{"[PLATE_381]": "AB-123-CD"}); err != nil {
		t.Fatal(err)
	}
	v.Forget()

	if got, ok := v.Recall("s1", "AB-123-CD"); ok {
		t.Errorf("Recall = %q after Forget", got)
	}
}
