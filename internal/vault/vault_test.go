package vault

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
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

// token renders an index as the bracket token the detector would, for a prefix.
func token(prefix string) func(int64) string {
	return func(index int64) string { return fmt.Sprintf("[%s_%d]", prefix, index) }
}

// A value keeps its name after its session's mapping has expired.
//
// The model remembers a token longer than the mapping does — inside a thinking
// block's signature, which the agent cannot read — so a pause that expired the
// mapping and renamed the value on the next turn left the model writing a name
// nothing expanded any more.
func TestANameOutlivesTheMapping(t *testing.T) {
	v, store := newTestVault(t)

	first := v.Name("s1", "AB-123-CD", "PLATE", token("PLATE"))
	second := v.Name("s1", "EF-456-GH", "PLATE", token("PLATE"))
	if err := v.Save("s1", map[string]string{first: "AB-123-CD", second: "EF-456-GH"}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.sessions["s1"].expires = time.Now().Add(-time.Second)
	store.mu.Unlock()

	// The precondition, so the assertion below is about the names and not about a
	// mapping that never expired.
	if got := v.Load("s1"); len(got) != 0 {
		t.Fatalf("the mapping did not expire: %v", got)
	}
	if got := v.Name("s1", "EF-456-GH", "PLATE", token("PLATE")); got != second {
		t.Errorf("after the mapping expired the value was named %s, want %s", got, second)
	}

	// Per session, as the mapping is: another conversation numbers the value in its
	// own sequence, and a name shared between two would tell the provider they are
	// about the same vehicle.
	if got := v.Name("s2", "EF-456-GH", "PLATE", token("PLATE")); got != "[PLATE_1]" {
		t.Errorf("another session named the value %s, want its own [PLATE_1]", got)
	}
}

// Many requests in flight that all meet one value for the first time give it one
// name. Looked up and claimed in two steps, each minted its own, and after a pause
// every conversation but the last to save read its name back unexpanded.
func TestConcurrentClaimsGiveOneValueOneName(t *testing.T) {
	v, _ := newTestVault(t)

	const n = 200
	got := make(chan string, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { got <- v.Name("s1", "AB-123-CD", "PLATE", token("PLATE")) })
	}
	wg.Wait()
	close(got)

	names := map[string]bool{}
	for name := range got {
		names[name] = true
	}
	if len(names) != 1 {
		t.Errorf("one value was given %d names: %v", len(names), names)
	}
	if next := v.Name("s1", "EF-456-GH", "PLATE", token("PLATE")); next != "[PLATE_2]" {
		t.Errorf("the next value was named %s, want [PLATE_2]: a claim that lost the race spent an index", next)
	}
}

// Many requests in flight that all meet different values never give two of them
// one index, which is what the process-wide counters guaranteed before the
// numbering moved here.
func TestConcurrentClaimsNeverRepeatAnIndex(t *testing.T) {
	v, _ := newTestVault(t)

	const n = 200
	got := make(chan string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { got <- v.Name("s1", fmt.Sprintf("user%d@example.fr", i), "EMAIL", token("EMAIL")) })
	}
	wg.Wait()
	close(got)

	seen := make(map[string]bool, n)
	for name := range got {
		if seen[name] {
			t.Fatalf("%s was handed out twice", name)
		}
		seen[name] = true
	}
}

// What the names keep past the mapping's lifetime must not be the value, or the
// lifetime would stop being the window the originals exist in memory.
func TestTheNamesDoNotHoldTheValue(t *testing.T) {
	v, _ := newTestVault(t)

	v.Name("s1", "AB-123-CD", "PLATE", token("PLATE"))
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
// long ago it was minted: a value its live mapping holds is reused without Name
// being asked, so nothing but the session's own activity says the name is in use.
func TestTheNamesOfAnIdleSessionAreSwept(t *testing.T) {
	v, _ := newTestVault(t)

	idle := v.Name("idle", "AB-123-CD", "PLATE", token("PLATE"))
	busy := v.Name("busy", "EF-456-GH", "PLATE", token("PLATE"))
	v.Name("busy", "JK-789-LM", "PLATE", token("PLATE"))

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

	// Read off the book rather than through Name: a swept session numbers from 1
	// again, so its first value comes back as the very name it had.
	v.mu.Lock()
	_, kept := v.names[v.fingerprint("idle", "AB-123-CD")]
	v.mu.Unlock()
	if kept {
		t.Errorf("a session idle for longer than namesTTL kept its name %s", idle)
	}
	if got := v.Name("busy", "EF-456-GH", "PLATE", token("PLATE")); got != busy {
		t.Errorf("a session still in use lost its name: %s, want %s", got, busy)
	}
}

// A mode change forgets the mapping and the names, not the numbering: the
// conversation still remembers the names minted before it, and a counter back at 1
// would hand them to other values.
func TestForgetDropsTheNamesAndKeepsTheNumbering(t *testing.T) {
	v, _ := newTestVault(t)

	before := v.Name("s1", "AB-123-CD", "PLATE", token("PLATE"))
	v.Forget()

	if got := v.Name("s1", "AB-123-CD", "PLATE", token("PLATE")); got == before {
		t.Errorf("the value kept %s across Forget, in the shape just switched away from", got)
	} else if got != "[PLATE_2]" {
		t.Errorf("the value was renamed %s after Forget, want [PLATE_2]", got)
	}
}

// Each session counts on its own, from 1, and per prefix: the numbers a provider
// sees stay small, and the fake stand-ins inside their generators' capacity.
func TestNumberingIsPerSession(t *testing.T) {
	v, _ := newTestVault(t)

	for i, want := range []string{"[PLATE_1]", "[PLATE_2]"} {
		if got := v.Name("s1", fmt.Sprintf("plate %d", i), "PLATE", token("PLATE")); got != want {
			t.Errorf("s1's plate #%d = %s, want %s", i+1, got, want)
		}
	}
	if got := v.Name("s2", "another plate", "PLATE", token("PLATE")); got != "[PLATE_1]" {
		t.Errorf("s2's first plate = %s, want [PLATE_1]: one session's numbering reached another", got)
	}
	if got := v.Name("s1", "claire@example.fr", "EMAIL", token("EMAIL")); got != "[EMAIL_1]" {
		t.Errorf("s1's first address = %s, want [EMAIL_1]: the prefixes share a counter", got)
	}
}

// A restart resumes each session where it stopped. Counting from 1 again gave the
// first plate the new process met a name the conversation remembered as another
// vehicle, and the answer naming that one came back carrying the wrong plate.
func TestNumberingSurvivesARestart(t *testing.T) {
	before, _ := newTestVault(t)
	var stored Numbering
	before.Persist(nil, func(n Numbering) { stored = n })

	entries := map[string]string{}
	for i := range 3 {
		value := fmt.Sprintf("plate %d", i)
		entries[before.Name("s1", value, "PLATE", token("PLATE"))] = value
	}
	if err := before.Save("s1", entries); err != nil {
		t.Fatal(err)
	}
	if stored["s1"].Highest["PLATE"] != 3 || stored["s1"].Last.IsZero() {
		t.Fatalf("the numbering handed over = %+v, want PLATE at 3 and a date", stored)
	}

	after, _ := newTestVault(t)
	after.Persist(stored, func(Numbering) {})
	if got := after.Name("s1", "a new plate", "PLATE", token("PLATE")); got != "[PLATE_4]" {
		t.Errorf("the first plate after the restart = %s, want [PLATE_4]", got)
	}
}

// Written from Save, not from Name — a name leaves the workstation only after the
// Save of the request that minted it — and not on a Save that changed nothing.
func TestNumberingIsWrittenFromSaveAndOnlyWhenItChanged(t *testing.T) {
	v, _ := newTestVault(t)
	writes := 0
	v.Persist(nil, func(Numbering) { writes++ })

	name := v.Name("s1", "AB-123-CD", "PLATE", token("PLATE"))
	if writes != 0 {
		t.Fatalf("Name wrote the numbering %d time(s); a name nobody was told needs no record", writes)
	}
	if err := v.Save("s1", map[string]string{name: "AB-123-CD"}); err != nil {
		t.Fatal(err)
	}
	if err := v.Save("s1", nil); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Errorf("wrote the numbering %d time(s), want 1: a request that minted nothing within the hour changes nothing", writes)
	}

	// An hour on, a request that mints nothing still dates the session, or a
	// conversation reusing its values for a week reads as idle after a restart.
	v.mu.Lock()
	v.active["s1"] = time.Now().Add(-2 * time.Hour)
	v.mu.Unlock()
	if err := v.Save("s1", nil); err != nil {
		t.Fatal(err)
	}
	if writes != 2 {
		t.Errorf("wrote the numbering %d time(s), want 2: a session's date went more than an hour stale on disk", writes)
	}
}

// A session idle for namesTTL loses its numbering with its names, and the drop is
// written, or the file would carry every session it ever held.
func TestAnIdleSessionsNumberingIsSwept(t *testing.T) {
	v, _ := newTestVault(t)
	var stored Numbering
	v.Persist(Numbering{
		"gone": {Last: time.Now().Add(-namesTTL - time.Hour), Highest: map[string]int64{"PLATE": 9}},
		"zero": {Highest: map[string]int64{"PLATE": 4}},
	}, func(n Numbering) { stored = n })

	name := v.Name("s1", "claire@example.fr", "EMAIL", token("EMAIL"))
	if err := v.Save("s1", map[string]string{name: "claire@example.fr"}); err != nil {
		t.Fatal(err)
	}

	for _, session := range []string{"gone", "zero"} {
		if _, kept := stored[session]; kept {
			t.Errorf("the numbering of %q survived the sweep: %+v", session, stored)
		}
	}
	if got := v.Name("gone", "AB-123-CD", "PLATE", token("PLATE")); got != "[PLATE_1]" {
		t.Errorf("a swept session resumed at %s, want [PLATE_1]", got)
	}
}
