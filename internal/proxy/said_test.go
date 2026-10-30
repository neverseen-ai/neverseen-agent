package proxy

import (
	"strconv"
	"testing"
)

func TestSaidGivesAKeyOnce(t *testing.T) {
	var s said
	if !s.first("a") || s.first("a") || !s.first("b") {
		t.Error("first must be true for a new key and false for a repeat")
	}
}

// Reaching the cap forgets rather than refuses: a repeat, never a silence.
func TestSaidForgetsAtItsCap(t *testing.T) {
	var s said
	for i := 0; i < saidCap; i++ {
		s.first(strconv.Itoa(i))
	}
	if !s.first("one more") || !s.first("0") {
		t.Error("a key given before the cap was reached should be given again after it")
	}
	if len(s.seen) > saidCap {
		t.Errorf("remembers %d keys, cap is %d", len(s.seen), saidCap)
	}
}
