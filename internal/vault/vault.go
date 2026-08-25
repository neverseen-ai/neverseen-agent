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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"maps"
	"sync"
	"time"
)

// DefaultTTL is how long a session's mapping outlives its last use.
//
// Long enough that a conversation with a pause in it keeps one identity for each
// value, short enough that a workstation left running does not accumulate the
// day's data. It is the window in which the originals exist in memory at all, so
// it is a privacy setting as much as a cache setting.
const DefaultTTL = 30 * time.Minute

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
}

// Vault is a Store with the originals encrypted at rest.
type Vault struct {
	store Store
	aead  cipher.AEAD
	ttl   time.Duration
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
	return &Vault{store: store, aead: aead, ttl: ttl}, nil
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

// Save adds what a pass minted to the session's mapping.
func (v *Vault) Save(session string, entries map[string]string) error {
	if len(entries) == 0 {
		return nil
	}

	sealed := make(map[string]string, len(entries))
	for masked, original := range entries {
		box, err := v.seal(original)
		if err != nil {
			return fmt.Errorf("seal a value: %w", err)
		}
		sealed[masked] = box
	}
	v.store.Merge(session, sealed, v.ttl)
	return nil
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
// erases the first), and the encryption key, which must be configured rather
// than generated per process.
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

// Merge adds entries to a session and refreshes its lifetime.
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
		s = &memorySession{entries: make(map[string]string, len(entries))}
		m.sessions[session] = s
	}
	maps.Copy(s.entries, entries)
	s.expires = now.Add(ttl)
}

// Sessions reports how many live sessions the store holds, for the agent's own
// status line.
func (m *Memory) Sessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	now, live := time.Now(), 0
	for _, s := range m.sessions {
		if now.Before(s.expires) {
			live++
		}
	}
	return live
}
