// Package vault remembers what a masked value used to be, for as long as a
// conversation lives.
//
// It is the half of the product that makes masking reversible. Without it the
// agent would be a redactor: the model would answer about "[EMAIL_1]" and the
// caller would read "[EMAIL_1]" back.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"maps"
	"sync"
	"time"
)

// DefaultTTL is how long a session's mapping outlives its last use.
//
// Long enough that a conversation left over lunch keeps one identity for each
// value, short enough that a workstation left running overnight does not carry
// yesterday's data into the morning. It is the window in which the originals
// exist in memory at all, so it is a privacy setting as much as a cache setting.
//
// Three hours rather than the thirty minutes it was: a pause longer than this
// lifetime is when an answer comes back carrying a token nothing expands, and
// half an hour is shorter than lunch. Name is what covers a pause longer than
// this one; the lifetime only decides how often it has to.
const DefaultTTL = 3 * time.Hour

// namesTTL is how long Name remembers a session's names after the session's last
// request.
//
// Far longer than the mapping, because it covers the pauses the mapping does not —
// a conversation picked up the next morning, or after a weekend — and holds no
// original to protect. Not forever, because a session can be a single request:
// sessionOf falls back to X-Request-Id, and a client stamping a fresh one on every
// call would otherwise leave every value of every request behind for the life of
// the process.
const namesTTL = 7 * 24 * time.Hour

// Store holds one session's mapping. The mapping is masked → original, because
// that is the direction the response path reads it.
//
// An interface with one implementation, which is usually a smell. It is here
// because the *second* implementation is a known requirement rather than a
// speculative one: a deployment that wants a mapping to survive a restart, or
// to be shared between two agents, needs a Redis-backed one, and the TODO on
// Memory says what else has to change with it.
type Store interface {
	// Load returns a copy of the session's mapping.
	Load(session string) map[string]string
	// Merge adds entries to the session's mapping and refreshes its lifetime.
	Merge(session string, entries map[string]string, ttl time.Duration)
	// Forget drops every session, so nothing minted earlier is served again.
	Forget()
}

// Vault is a Store with the originals encrypted at rest.
type Vault struct {
	store Store
	aead  cipher.AEAD
	ttl   time.Duration

	// names remembers what each value was replaced by, per session, for longer
	// than the mapping lives: fingerprint → name. See Name.
	//
	// Keyed by an HMAC of the value under a key derived from the vault's own,
	// never by the value: the lifetime is the window the originals exist in
	// memory, and this is kept past it. A plain hash would not do, because a plate
	// or a phone number is a small enough space to hash every candidate of.
	//
	// mu also covers the store's writes, so a Forget cannot land between a name and
	// the Save of its mapping without both being dropped together.
	//
	// active is when each session last saved, which is every request it made: a
	// name lives as long as its session does, because a value the live mapping
	// already holds is reused without Name ever being asked — dated per name, a
	// conversation in use for longer than namesTTL would lose names it still needs.
	mark   []byte
	mu     sync.Mutex
	names  map[string]name
	active map[string]time.Time
	swept  time.Time

	// highest is each session's numbering: prefix → the last index it handed out.
	// See next. written receives it whenever it changes in a way a restart must
	// not lose, and dirty says one has happened since it was last handed over.
	highest map[string]map[string]int64
	written func(Book)
	dirty   bool

	// keepNames says the names go into what written receives: only once Persist
	// was handed a fingerprint key that outlives the process, because names
	// fingerprinted under a key the next process cannot know would be bytes on a
	// disk that can never match anything.
	keepNames bool
}

// Book is what a restart needs to go on naming a session's values as the
// conversation remembers them: for each session, when it last made a request, the
// last index it handed out per token prefix — so a new process never gives a name
// it already gave a different value — and the names themselves, so a value the
// conversation replays is handed the name it had before the restart.
//
// Never a value. A name is keyed by the value's fingerprint, an HMAC under a key
// kept beside this file. What that does not protect against has to be said where
// it is written: somebody who can read both files can test a guess — a plate, a
// phone number — and learn whether this agent named it in the week before it last
// ran. The week is the sweep's, and the sweep runs on a request: an agent that is
// stopped leaves the book as it was until it starts again, which is why
// uninstalling deletes it (proxy.ForgetNames). The same reader holds the control
// key, which switches masking off, and the client's own transcripts in clear; the
// book adds nothing for a value that already reached a disk that way, and is the
// one copy for a value typed into a browser chat.
type Book map[string]SessionBook

// SessionBook is one session's half of Book.
type SessionBook struct {
	Last    time.Time        `json:"last"`
	Highest map[string]int64 `json:"highest"`
	// Names is fingerprint (base64) → the replacement it was given.
	Names map[string]string `json:"names,omitempty"`
}

// name is what Name hands back, and the session it belongs to.
type name struct {
	masked  string
	session string
}

// New builds a vault over a store.
//
// The key may be nil, and then one is generated for the life of the process. So
// the mapping is always encrypted and the encrypting path is always the one that
// runs — there is no second, plaintext mode that only shows up in a deployment
// nobody tested.
//
// What that buys today is narrow and worth stating plainly: the store is in
// memory, so this protects the originals in a core dump or a swapped page, not
// against anything with access to the running process. It earns its keep when
// the mapping moves to a shared store, which is why the key can be supplied
// rather than always generated.
func New(store Store, key []byte, ttl time.Duration) (*Vault, error) {
	if key == nil {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate a session key: %w", err)
		}
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("the encryption key is %d bytes, want 32 for AES-256", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("build the cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build AES-GCM: %w", err)
	}

	if ttl <= 0 {
		ttl = DefaultTTL
	}
	// A key of its own for the fingerprints, derived rather than drawn, so the one
	// secret the vault is built from is the only one there is: a configured key
	// gives every agent sharing it the same fingerprints, a generated one gives
	// each process its own. Never the AES key itself — one key, one purpose.
	derive := hmac.New(sha256.New, key)
	derive.Write([]byte("neverseen vault names"))
	mark := derive.Sum(nil)

	return &Vault{
		store: store, aead: aead, ttl: ttl, mark: mark,
		names: make(map[string]name), active: make(map[string]time.Time),
		highest: make(map[string]map[string]int64),
	}, nil
}

// Load returns the session's mapping, masked → original.
//
// An entry that cannot be decrypted is dropped rather than reported. It means
// the process was restarted under a new ephemeral key, which is expected: the
// token is then simply unknown, and the response path leaves it alone instead of
// failing the request over bookkeeping.
func (v *Vault) Load(session string) map[string]string {
	stored := v.store.Load(session)
	if len(stored) == 0 {
		return nil
	}

	out := make(map[string]string, len(stored))
	for masked, sealed := range stored {
		original, err := v.open(sealed)
		if err != nil {
			continue
		}
		out[masked] = original
	}
	return out
}

// Save adds what a pass minted to the session's mapping, and refreshes its
// lifetime even when the pass minted nothing.
//
// A conversation that introduced its values early and then only reuses them
// mints nothing on every later request. Refreshed only on a mint, the mapping
// expired DefaultTTL after the *first* request rather than the last, and the
// answer to a conversation still in progress reached the caller with raw tokens.
func (v *Vault) Save(session string, entries map[string]string) error {
	sealed := make(map[string]string, len(entries))
	for masked, original := range entries {
		box, err := v.seal(original)
		if err != nil {
			return fmt.Errorf("seal a value: %w", err)
		}
		sealed[masked] = box
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.store.Merge(session, sealed, v.ttl)

	now := time.Now()
	// A numbered session's request date is written at most hourly when it mints
	// nothing: dated only by its last mint, a conversation that went on reusing
	// its values for a week would read as idle after a restart, and be swept back
	// to index 1 under names the model still remembers.
	if now.Sub(v.active[session]) > time.Hour && v.highest[session] != nil {
		v.dirty = true
	}
	v.active[session] = now
	v.sweep(now)
	v.write()
	return nil
}

// Name returns what a value is replaced by in a session: the name the session gave
// it before, or a new one, rendered from the session's next index for prefix.
//
// Looked up and claimed in one step under the vault's lock, because two steps let
// two requests in flight that both met a value for the first time mint two names:
// the mapping kept both while it lived, the book remembered only the last, and
// after a pause the conversation that had been told the other one read it back
// unexpanded. Claimed here, the second request is handed the first one's name.
//
// A name is claimed before the Save that stores its mapping, so a request that
// fails in between leaves a name with no mapping — which the next request naming
// the value puts back, under that name.
//
// The book is how a value keeps its name once the mapping has expired. The model
// remembers a token longer than the mapping does: what it reasoned about comes
// back to it on every turn inside the thinking block's signature, encrypted — a
// client replays the signature and an empty text beside it — so the agent can
// neither mask nor restore it, and it still says [PLATE_381] after a pause has
// expired the mapping. The history, replayed in clear, was then masked afresh as
// [PLATE_412]; the model wrote the name it had reasoned with, and the caller read
// "[PLATE_381]" back. Handing the value the name it had before closes that:
// re-masking the history puts the entry back into the mapping, under the name the
// model is still using. It also keeps the prefix a provider caches the same across
// a pause, which a renumbering broke from the first value it renamed.
//
// render runs under the lock; it is the substitution mode applied to an index, and
// takes no lock of its own.
func (v *Vault) Name(session, original, prefix string, render func(index int64) string) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	fingerprint := v.fingerprint(session, original)
	if n, ok := v.names[fingerprint]; ok {
		return n.masked
	}

	// A session's first claim dates it, or a Save of another session sweeping in
	// between would drop this name as belonging to no session in use. Only the
	// first: a claim refreshing the date would hide from Save that the date on
	// disk has gone stale.
	if _, ok := v.active[session]; !ok {
		v.active[session] = time.Now()
	}
	masked := render(v.next(session, prefix))
	v.names[fingerprint] = name{masked: masked, session: session}
	return masked
}

// next hands out a session's next index for a token prefix. Called with mu held.
//
// Per session, and remembered across a restart once Persist is called, because a
// counter that started again at 1 with every process handed out names the model
// had already been told meant something else. A conversation running through a
// restart remembers [PLATE_12] as one vehicle; the new process gave [PLATE_12] to
// the first plate it met, and the answer naming the first came back carrying the
// second — restored, plausible and wrong, with nothing to say so.
//
// Per session rather than per process also keeps the numbers small, and the fake
// stand-ins inside their generators' capacity, which a number grown over every
// conversation since the agent started would not.
//
// Under the vault's lock, so two requests in flight on one session never take the
// same index for two values.
func (v *Vault) next(session, prefix string) int64 {
	h := v.highest[session]
	if h == nil {
		h = make(map[string]int64)
		v.highest[session] = h
	}
	h[prefix]++
	v.dirty = true
	return h[prefix]
}

// Persist resumes the book a previous process wrote, and hands every later change
// to write.
//
// mark is the fingerprint key, and the names travel only with one: given a key
// that outlives the process, the names are restored and written; given nil, only
// the numbering is, and the names last as long as the process — a restart then
// leaves a token minted before it unexpanded, never restored as another value.
//
// Written from Save, never from Name: a name only leaves the workstation after the
// Save of the request that minted it, so a process killed in between loses names
// nobody was ever told — and Save is once per request, where Name is once per value.
// write is called under the vault's lock, so two writes never race to the disk with
// the older one landing last.
func (v *Vault) Persist(from Book, mark []byte, write func(Book)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if mark != nil {
		v.mark = mark
		v.keepNames = true
	}
	for session, n := range from {
		v.highest[session] = maps.Clone(n.Highest)
		// A zero date reads as idle since forever, so a session written without
		// one is swept at the first write rather than kept with no way to age out.
		v.active[session] = n.Last
		if !v.keepNames {
			continue
		}
		for fingerprint, masked := range n.Names {
			// A fingerprint that does not decode is dropped: it can only be a file
			// somebody edited, and it could never match a value anyway.
			if raw, err := base64.RawStdEncoding.DecodeString(fingerprint); err == nil {
				v.names[string(raw)] = name{masked: masked, session: session}
			}
		}
	}
	v.written = write
}

// write hands the book over if it changed. Called with mu held.
//
// TODO: the whole book is handed over, and rewritten, on every request that mints:
// ~100 KB for 1,500 names in the simulation, under the lock, which serialised 24
// concurrent conversations to a 12 ms median. Right for one person at one
// workstation; the upgrade is an append-only journal of new names, compacted when
// the sweep runs.
func (v *Vault) write() {
	if !v.dirty || v.written == nil {
		return
	}
	v.dirty = false
	out := make(Book, len(v.highest))
	for session, h := range v.highest {
		out[session] = SessionBook{Last: v.active[session], Highest: maps.Clone(h)}
	}
	if v.keepNames {
		// Every name's session is numbered — Name numbers before it names — so its
		// entry is already in out.
		for fingerprint, n := range v.names {
			b := out[n.session]
			if b.Names == nil {
				b.Names = make(map[string]string)
			}
			b.Names[base64.RawStdEncoding.EncodeToString([]byte(fingerprint))] = n.masked
			out[n.session] = b
		}
	}
	v.written(out)
}

// sweep drops the names and the numbering of every session idle for namesTTL, at
// most once an hour.
//
// The numbering goes with the names because both answer one question — what has
// this conversation been told — and a session idle for a week is taken to have
// ended. Driven by active, which Name dates on a session's first claim, so a session
// whose first request is still between Name and Save is not swept mid-request.
//
// On a write rather than on a timer, as Memory.Merge purges its sessions: a process
// that keeps writing keeps the map bounded, and one that has stopped has nothing to
// grow. Rate-limited because the map can hold a week of traffic, and walking it on
// every request would cost each one in proportion to everything the agent has seen.
func (v *Vault) sweep(now time.Time) {
	if now.Sub(v.swept) < time.Hour {
		return
	}
	v.swept = now
	for session, last := range v.active {
		if now.Sub(last) > namesTTL {
			delete(v.active, session)
			if _, numbered := v.highest[session]; numbered {
				delete(v.highest, session)
				v.dirty = true
			}
		}
	}
	for fingerprint, n := range v.names {
		if _, ok := v.active[n.session]; !ok {
			delete(v.names, fingerprint)
		}
	}
}

// fingerprint names a value within a session without holding it.
func (v *Vault) fingerprint(session, original string) string {
	h := hmac.New(sha256.New, v.mark)
	h.Write([]byte(session))
	h.Write([]byte{0})
	h.Write([]byte(original))
	return string(h.Sum(nil))
}

// Forget drops every mapping this vault holds.
//
// It exists for one caller: a change of substitution mode. A mapping is consulted
// before the mode is, so a value already seen keeps the shape it was first given —
// which is what lets a conversation straddling the change round-trip. The cost is
// that the change is invisible on the traffic somebody just clicked to change, on a
// workstation where one unnamed session carries everything.
//
// So the mode is the one setting that clears the mapping, and the trade is stated
// where it is made: replacements minted before the change stop being restored, and
// an answer still in flight comes back carrying a token nothing expands. That window
// is one exchange wide, against a control that otherwise appears not to work at all.
//
// The names Name remembers go with the mapping, or a value seen before the change
// would be handed back the shape the operator just switched away from. The
// numbering stays, for the reason it survives a restart: the conversation still
// remembers the names minted before the change, and a counter back at 1 would give
// them to other values.
func (v *Vault) Forget() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.store.Forget()
	v.names = make(map[string]name)

	// Written now rather than at the next Save: a restart in between would read
	// the old names back and hand values the shape the operator switched away from.
	if v.keepNames {
		v.dirty = true
		v.write()
	}
}

// seal encrypts one original. The nonce is fresh per value and prefixed to the
// box, so two identical originals under two tokens do not produce identical
// ciphertext — which would say they are the same person to anyone reading the
// store.
func (v *Vault) seal(original string) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	box := v.aead.Seal(nonce, nonce, []byte(original), nil)
	return base64.StdEncoding.EncodeToString(box), nil
}

func (v *Vault) open(sealed string) (string, error) {
	box, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	if len(box) < v.aead.NonceSize() {
		return "", fmt.Errorf("sealed value is %d bytes, shorter than a nonce", len(box))
	}
	nonce, body := box[:v.aead.NonceSize()], box[v.aead.NonceSize():]

	original, err := v.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(original), nil
}

// Memory is an in-process Store.
//
// TODO: the mapping is lost on restart and is not shared between processes,
// which is right for one agent on one workstation and wrong for anything else.
// A Redis-backed Store fixes the persistence, and then two things have to move
// with it: the token counters (see the detector's index source, or two agents on
// one session mint the same index for different values and the second write
// erases the first), the encryption key, which must be configured rather
// than generated per process, and the names Name remembers, which live on the
// Vault rather than in the store and would not be shared.
type Memory struct {
	mu       sync.Mutex
	sessions map[string]*memorySession
}

type memorySession struct {
	entries map[string]string
	expires time.Time
}

// NewMemory builds an in-process Store.
func NewMemory() *Memory {
	return &Memory{sessions: make(map[string]*memorySession)}
}

// Load returns a copy of the session's mapping, or nil once it has expired.
func (m *Memory) Load(session string) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[session]
	if !ok {
		return nil
	}
	if time.Now().After(s.expires) {
		delete(m.sessions, session)
		return nil
	}
	return maps.Clone(s.entries)
}

// Merge adds entries to a session and refreshes its lifetime. Nothing to add to
// a session that does not exist creates none: a request that masked nothing
// must not leave an empty session behind.
func (m *Memory) Merge(session string, entries map[string]string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Expired sessions are dropped on every write rather than by a background
	// sweep. A process that keeps writing keeps the map bounded, and one that
	// has stopped writing has nothing left to leak into.
	now := time.Now()
	for name, s := range m.sessions {
		if now.After(s.expires) {
			delete(m.sessions, name)
		}
	}

	s, ok := m.sessions[session]
	if !ok {
		if len(entries) == 0 {
			return
		}
		s = &memorySession{entries: make(map[string]string, len(entries))}
		m.sessions[session] = s
	}
	maps.Copy(s.entries, entries)
	s.expires = now.Add(ttl)
}

// Forget drops every session.
func (m *Memory) Forget() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// A fresh map rather than clear(), so the memory the originals were in is
	// released to the collector rather than kept in the buckets of the old one.
	m.sessions = make(map[string]*memorySession)
}
