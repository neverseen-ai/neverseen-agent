package proxy

import (
	"net"
	"os"
	"slices"
	"sort"
	"testing"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
)

// What the machine running the tests reports has to be usable for the one purpose
// this field exists for: recognising a machine.
//
// Asserted as properties rather than against a fixed list, because the addresses
// of the machine running this suite are not knowable in advance — and a test that
// skipped on a machine with no network would be a test that never ran in CI.
func TestLocalAddressesAreUsableForRecognisingAMachine(t *testing.T) {
	got := localAddresses()

	if len(got) > maxAddresses {
		t.Errorf("reported %d addresses, want at most %d: %v", len(got), maxAddresses, got)
	}

	for _, s := range got {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Errorf("%q is not an IP address", s)
			continue
		}
		// Every machine has 127.0.0.1, so a loopback address distinguishes
		// nothing and would make every row of the fleet view look identical.
		if ip.IsLoopback() {
			t.Errorf("%s is a loopback address", s)
		}
		// A link-local address names a network whose DHCP failed, not a machine.
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			t.Errorf("%s is link-local", s)
		}
	}

	// No duplicates: an address reported twice reads as two interfaces on the same
	// network, which is a thing an operator would go and investigate.
	seen := map[string]bool{}
	for _, s := range got {
		if seen[s] {
			t.Errorf("%s is reported twice", s)
		}
		seen[s] = true
	}
}

// The same machine reports the same list in the same order every time.
//
// Interface enumeration order is not guaranteed by the platform. A list that
// reshuffled between heartbeats would look like a machine whose addresses kept
// changing — and on the dashboard that is indistinguishable from a laptop moving
// between networks, which is a thing somebody would chase.
func TestLocalAddressesAreStablyOrdered(t *testing.T) {
	first := localAddresses()

	for range 5 {
		again := localAddresses()
		if !slices.Equal(first, again) {
			t.Fatalf("two enumerations differ:\n%v\n%v", first, again)
		}
	}

	// IPv4 before IPv6, because the v4 address is the one an operator recognises.
	// Checked by finding the first v6 and asserting nothing after it is v4.
	seenSix := false
	for _, s := range first {
		isFour := net.ParseIP(s).To4() != nil
		if !isFour {
			seenSix = true
			continue
		}
		if seenSix {
			t.Errorf("an IPv4 address follows an IPv6 one: %v", first)
			break
		}
	}

	// And within each family, sorted.
	var four []string
	for _, s := range first {
		if net.ParseIP(s).To4() != nil {
			four = append(four, s)
		}
	}
	sorted := slices.Clone(four)
	sort.Strings(sorted)
	if !slices.Equal(four, sorted) {
		t.Errorf("the IPv4 addresses are not sorted: %v", four)
	}
}

// The state the agent reports carries them, which is the wiring that makes the
// field worth having. Read at each heartbeat rather than cached, because a laptop
// moves between networks.
func TestTheReportedStateCarriesTheAddresses(t *testing.T) {
	det := detector.New(detector.Config{Locales: []string{"fr"}})
	v, err := vault.New(vault.NewMemory(), nil, vault.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{}, det, v)
	if err != nil {
		t.Fatal(err)
	}

	state := srv.State()
	if !slices.Equal(state.Addresses, localAddresses()) {
		t.Errorf("the reported state has %v, want %v", state.Addresses, localAddresses())
	}

	// The name beside them, for the same reason: an address needs a directory to
	// become a machine and a name is one already. Asserted against the system's
	// own answer rather than a literal, because a test that pinned a name would
	// only pass on the machine it was written on.
	want, err := os.Hostname()
	if err != nil {
		t.Skip("this machine cannot say what it is called")
	}
	if state.Hostname != want {
		t.Errorf("the reported state is called %q, want %q", state.Hostname, want)
	}
}
