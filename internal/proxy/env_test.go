package proxy

import "testing"

func TestBeyondLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
		why  string
	}{
		{"127.0.0.1:9787", false, "the default"},
		{"127.0.0.2:9787", false, "the rest of the loopback range"},
		{"[::1]:9787", false, "loopback, v6"},
		{"localhost:9787", false, "the name for it"},

		{"0.0.0.0:9787", true, "every v4 interface — the request this exists for"},
		{"[::]:9787", true, "every interface, v6"},
		{":9787", true, "no host at all still binds every interface"},
		{"192.168.1.10:9787", true, "one reachable interface"},
		{"agent.internal:9787", true, "a name resolves to whatever DNS says"},

		// Not an address. ListenAndServe says so better than a warning would.
		{"9787", false, "no port separator"},
		{"", false, "empty"},
	}

	for _, tt := range tests {
		if got := BeyondLoopback(tt.addr); got != tt.want {
			t.Errorf("BeyondLoopback(%q) = %v, want %v — %s", tt.addr, got, tt.want, tt.why)
		}
	}
}
