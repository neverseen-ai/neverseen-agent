package proxy

import "testing"

func TestBeyondLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
		why  string
	}{
		{"127.0.0.1:8787", false, "the default"},
		{"127.0.0.2:8787", false, "the rest of the loopback range"},
		{"[::1]:8787", false, "loopback, v6"},
		{"localhost:8787", false, "the name for it"},

		{"0.0.0.0:8787", true, "every v4 interface — the request this exists for"},
		{"[::]:8787", true, "every interface, v6"},
		{":8787", true, "no host at all still binds every interface"},
		{"192.168.1.10:8787", true, "one reachable interface"},
		{"agent.internal:8787", true, "a name resolves to whatever DNS says"},

		// Not an address. ListenAndServe says so better than a warning would.
		{"8787", false, "no port separator"},
		{"", false, "empty"},
	}

	for _, tt := range tests {
		if got := BeyondLoopback(tt.addr); got != tt.want {
			t.Errorf("BeyondLoopback(%q) = %v, want %v — %s", tt.addr, got, tt.want, tt.why)
		}
	}
}
