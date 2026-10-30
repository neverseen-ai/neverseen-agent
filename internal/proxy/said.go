package proxy

import "sync"

// saidCap bounds what said remembers. Reached, it forgets everything and a warning is
// given once more — a repeat, never a silence.
const saidCap = 4096

// said remembers which warnings were given. The zero value is ready to use.
//
// TODO: forgotten whole at saidCap rather than by age, so a long-lived agent repeats
// itself once per saidCap distinct warnings; an LRU would not.
type said struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// first reports whether key has not been given before, and records it.
func (s *said) first(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[key]; ok {
		return false
	}
	if s.seen == nil || len(s.seen) >= saidCap {
		s.seen = make(map[string]struct{})
	}
	s.seen[key] = struct{}{}
	return true
}
