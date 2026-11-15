package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The kernel prints each word of an address in the machine's own byte order, so the
// same socket reads differently on a little- and a big-endian machine. Read in the
// wrong order, 127.0.0.1 becomes 1.0.0.127 and every lookup misses — which, refusing
// an unknown socket, would refuse the owner too.
func TestProcAddrReadsTheKernelsByteOrder(t *testing.T) {
	cases := []struct {
		field string
		order binary.ByteOrder
		want  string
	}{
		{"0100007F:263B", binary.LittleEndian, "127.0.0.1:9787"},
		{"7F000001:263B", binary.BigEndian, "127.0.0.1:9787"},
		{"00000000000000000000000001000000:CC75", binary.LittleEndian, "[::1]:52341"},
		// A dual-stack listener files an IPv4 connection under its mapped form, and
		// net/http reports the address unmapped: the two must compare equal.
		{"0000000000000000FFFF00000100007F:263B", binary.LittleEndian, "127.0.0.1:9787"},
	}
	for _, c := range cases {
		got, ok := procAddr(c.field, c.order)
		if !ok || got != netip.MustParseAddrPort(c.want) {
			t.Errorf("procAddr(%q) = %v, %v; want %s", c.field, got, ok, c.want)
		}
	}

	for _, bad := range []string{"", "0100007F", "0100007F:XYZ", "01007F:263B", "zz00007F:263B"} {
		if got, ok := procAddr(bad, binary.LittleEndian); ok {
			t.Errorf("procAddr(%q) read %v from a field that is not one", bad, got)
		}
	}
}

// A connection over loopback is two sockets in one table, one per end, each with its
// own owner. Which one is asked for is decided by which end is "own": the caller's
// socket names the caller, the agent's names the agent.
func TestOwnerInFindsTheSocketByBothEnds(t *testing.T) {
	const table = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:263B 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 41234 1 0000000000000000 100 0 0 10 0
   1: 0100007F:CC75 0100007F:263B 01 00000000:00000000 00:00000000 00000000  1001        0 41240 1 0000000000000000 20 4 30 10 -1
   2: 0100007F:263B 0100007F:CC75 01 00000000:00000000 00:00000000 00000000  1000        0 41241 1 0000000000000000 20 4 30 10 -1
   3: 0100007F:D431 0100007F:263B 06 00000000:00000000 03:00001770 00000000     0        0 0 3 0000000000000000
`
	agent := netip.MustParseAddrPort("127.0.0.1:9787")
	caller := netip.MustParseAddrPort("127.0.0.1:52341")

	if uid, ok := ownerIn(table, caller, agent, binary.LittleEndian); !ok || uid != 1001 {
		t.Errorf("the caller's socket: uid %d, %v; want 1001", uid, ok)
	}
	if uid, ok := ownerIn(table, agent, caller, binary.LittleEndian); !ok || uid != 1000 {
		t.Errorf("the agent's socket: uid %d, %v; want 1000", uid, ok)
	}

	// A socket in TIME_WAIT is reported as root's. Read as a live one, a closed
	// connection's tuple would let whoever reused it pass as root.
	closed := netip.MustParseAddrPort("127.0.0.1:54321")
	if uid, ok := ownerIn(table, closed, agent, binary.LittleEndian); ok {
		t.Errorf("a socket in TIME_WAIT was read as owned by uid %d", uid)
	}
	if uid, ok := ownerIn(table, netip.MustParseAddrPort("127.0.0.1:1"), agent, binary.LittleEndian); ok {
		t.Errorf("a socket that is not in the table was found, owned by uid %d", uid)
	}

	// A dual-stack listener files an IPv4 connection in tcp6 under its mapped form,
	// while net/http hands the address over unmapped. Compared as written, the
	// agent's own socket would be missing — and a missing socket is refused.
	const table6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0000000000000000FFFF00000100007F:263B 0000000000000000FFFF00000100007F:CC75 01 00000000:00000000 00:00000000 00000000  1000        0 41241 1 0000000000000000 20 4 30 10 -1
`
	if uid, ok := ownerIn(table6, agent, caller, binary.LittleEndian); !ok || uid != 1000 {
		t.Errorf("the agent's socket under its mapped address: uid %d, %v; want 1000", uid, ok)
	}

	// net/http names a link-local peer with its zone, and the table never does.
	// Compared with the zone, the socket was missing — and a link-local address is not
	// loopback, so a missing one was let through as another machine's.
	const tableLinkLocal = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 000080FE000000000000000001000000:CC75 000080FE000000000000000001000000:263B 01 00000000:00000000 00:00000000 00000000  1001        0 41240 1 0000000000000000 20 4 30 10 -1
`
	zoned := netip.MustParseAddrPort("[fe80::1%eth0]:52341")
	if uid, ok := ownerIn(tableLinkLocal, zoned, netip.MustParseAddrPort("[fe80::1%eth0]:9787"), binary.LittleEndian); !ok || uid != 1001 {
		t.Errorf("a link-local caller named with its zone: uid %d, %v; want 1001", uid, ok)
	}
}

// A connection the kernel has completed and its listener not yet accepted is held by no
// process, and before Linux 6.17 the table names root as its owner. Read as written,
// another account binding the agent's port and accepting late passed a surface's check
// as root, and was handed the control key. It belongs to whoever listens.
func TestAConnectionNotYetAcceptedBelongsToItsListener(t *testing.T) {
	agent := netip.MustParseAddrPort("127.0.0.1:9787")
	surface := netip.MustParseAddrPort("127.0.0.1:53248")

	const table = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:263B 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1001        0 41234 1 0000000000000000 100 0 0 10 0
   1: 0100007F:D000 0100007F:263B 01 00000000:00000000 00:00000000 00000000  1000        0 41240 1 0000000000000000 20 4 30 10 -1
   2: 0100007F:263B 0100007F:D000 01 00000000:00000000 00:00000000 00000000     0        0 0 1 0000000000000000 20 4 30 10 -1
`
	if uid, ok := ownerIn(table, agent, surface, binary.LittleEndian); !ok || uid != 1001 {
		t.Errorf("a connection not yet accepted: uid %d, %v; want 1001, the account listening", uid, ok)
	}

	// Bound to every address, as `-l :9787` binds: a dual-stack listener filed in tcp6
	// as ::, with the connection under its mapped form.
	const table6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:263B 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1001        0 41234 1 0000000000000000 100 0 0 10 0
   1: 0000000000000000FFFF00000100007F:263B 0000000000000000FFFF00000100007F:D000 01 00000000:00000000 00:00000000 00000000     0        0 0 1 0000000000000000 20 4 30 10 -1
`
	if uid, ok := ownerIn(table6, agent, surface, binary.LittleEndian); !ok || uid != 1001 {
		t.Errorf("a connection not yet accepted by a listener on every address: uid %d, %v; want 1001", uid, ok)
	}

	// A caller that hung up at once: its socket, orphaned on its way to TIME_WAIT, is held
	// by no process either, and nothing listens on its end. Read as root's, its request
	// passed the agent's guard; not found, it is refused.
	const orphaned = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:D000 0100007F:263B 05 00000000:00000000 00:00000000 00000000     0        0 0 1 0000000000000000 20 4 30 10 -1
   1: 0100007F:263B 0100007F:D000 08 00000000:00000000 00:00000000 00000000  1000        0 41241 1 0000000000000000 20 4 30 10 -1
`
	if uid, ok := ownerIn(orphaned, surface, agent, binary.LittleEndian); ok {
		t.Errorf("a socket its process has closed was read as owned by uid %d", uid)
	}
}

// fixedOwner answers every lookup with one account, or with none.
func fixedOwner(uid uint32, known bool) ownerLookup {
	return func(netip.AddrPort, netip.AddrPort) (uint32, bool) { return uid, known }
}

// asked is a request as net/http would hand it over a real connection — with the
// local address in its context, which is what tells it from a test calling the
// handler — coming from remote.
func asked(t *testing.T, path, remote string) *http.Request {
	t.Helper()
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9787}
	ctx := context.WithValue(t.Context(), http.LocalAddrContextKey, local)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	req.RemoteAddr = remote
	req.Host = "127.0.0.1:9787"
	return req
}

func TestAnotherAccountIsRefusedOnEveryRoute(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	srv.ownerOf, srv.account = fixedOwner(1001, true), 1000
	handler := srv.Handler()

	// The provider route as well as the agent's own: that is where another account's
	// request was restored with this user's values.
	for _, path := range []string{"/healthz", "/test", "/settings", "/anthropic/v1/messages"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, asked(t, path, "127.0.0.1:52341"))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), EnvListen) {
			t.Errorf("%s: the refusal does not say how to run one's own: %s", path, rec.Body.String())
		}
	}
}

func TestTheAgentsOwnAccountAndRootPass(t *testing.T) {
	for _, uid := range []uint32{1000, rootAccount} {
		srv := settingsAgent(t, testControlKey)
		srv.ownerOf, srv.account = fixedOwner(uid, true), 1000

		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, asked(t, "/healthz", "127.0.0.1:52341"))
		if rec.Code != http.StatusOK {
			t.Errorf("uid %d: status %d, want 200", uid, rec.Code)
		}
	}
}

// Only loopback is asked about, so a socket that is not found there is not somebody
// else's machine: it is a question that could not be answered, and the answer to that
// is no.
func TestACallerWhoseAccountCannotBeToldIsRefused(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	srv.ownerOf, srv.account = fixedOwner(0, false), 1000

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, asked(t, "/healthz", "127.0.0.1:52341"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cannot tell") {
		t.Errorf("the refusal does not say why: %s", rec.Body.String())
	}
}

// The way round a guard that asked of loopback alone: an agent bound with -l :9787 is
// reached through this machine's own address as well, and another account doing so has
// a socket in the table like any loopback caller.
func TestAnotherAccountIsRefusedThroughThisMachinesOwnAddress(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	srv.ownerOf, srv.account = fixedOwner(1001, true), 1000

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, asked(t, "/anthropic/v1/messages", "192.168.1.5:52341"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

// What the guard lets through: a caller on another machine, which has no socket in
// this one's table and reached the agent only because -l said so; and a request that
// did not come over a connection at all.
func TestTheAccountGuardPassesWhatHasNoSocketHere(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	srv.ownerOf, srv.account = fixedOwner(0, false), 1000
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, asked(t, "/healthz", "192.168.1.20:52341"))
	if rec.Code != http.StatusOK {
		t.Errorf("a caller off this machine: status %d, want 200", rec.Code)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:52341"
	req.Host = "127.0.0.1:9787"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("a request with no connection: status %d, want 200", rec.Code)
	}
}

// A connection's account is decided once, when it is accepted: keep-alive puts many
// requests on one, and each read the kernel's tables whole while the guard asked per
// request. The decision still holds for every one of them, a refusal included.
func TestTheAccountIsDecidedOncePerConnection(t *testing.T) {
	for _, c := range []struct {
		uid  uint32
		want int
	}{{1000, http.StatusOK}, {1001, http.StatusForbidden}} {
		srv := settingsAgent(t, testControlKey)
		var lookups atomic.Int32
		srv.ownerOf = func(netip.AddrPort, netip.AddrPort) (uint32, bool) {
			lookups.Add(1)
			return c.uid, true
		}
		srv.account = 1000

		agent := httptest.NewUnstartedServer(srv.Handler())
		agent.Config.ConnContext = srv.ConnContext
		agent.Start()
		t.Cleanup(agent.Close)

		for range 3 {
			resp, err := agent.Client().Get(agent.URL + "/healthz")
			if err != nil {
				t.Fatal(err)
			}
			// Read to the end, or the client opens a new connection for the next one.
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Errorf("uid %d: status %d, want %d", c.uid, resp.StatusCode, c.want)
			}
		}
		if n := lookups.Load(); n != 1 {
			t.Errorf("uid %d: three requests on one connection asked for its account %d times, want 1", c.uid, n)
		}
	}
}

// net/http calls ConnContext from its accept loop: a lookup there held every new
// connection, the owner's included, behind the tables being read for the one before.
func TestTheAccountIsNotAskedOnTheAcceptLoop(t *testing.T) {
	srv := settingsAgent(t, testControlKey)
	var lookups atomic.Int32
	srv.ownerOf = func(netip.AddrPort, netip.AddrPort) (uint32, bool) {
		lookups.Add(1)
		return 1000, true
	}
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })

	_ = srv.ConnContext(t.Context(), server)
	if n := lookups.Load(); n != 0 {
		t.Errorf("ConnContext asked for the account %d times, want 0: it is asked on the first request", n)
	}
}

// The other direction: the first account to bind the port received everybody else's
// control key. Nothing may be written to a listener of another account — asserted on
// what the listener received, not on the error, because a refusal after the request
// went out would still have handed the key over.
func TestASurfaceSendsNothingToAListenerOfAnotherAccount(t *testing.T) {
	var received atomic.Int32
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
	}))
	t.Cleanup(listener.Close)

	cases := []struct {
		name      string
		lookup    ownerLookup
		wantKnown bool
	}{
		{"another account", fixedOwner(1001, true), true},
		{"an account that cannot be told", fixedOwner(0, false), false},
	}
	for _, c := range cases {
		client := newAgentClient(c.lookup, 1000)
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, listener.URL+"/today", nil)
		req.Header.Set(controlHeader, testControlKey)

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("%s: the request was answered", c.name)
		}
		var foreign *ForeignListenerError
		if !errors.As(err, &foreign) || foreign.Known != c.wantKnown {
			t.Errorf("%s: error %v, want a ForeignListenerError with Known %v", c.name, err, c.wantKnown)
		}
	}
	if n := received.Load(); n != 0 {
		t.Fatalf("the listener received %d requests", n)
	}

	resp, err := newAgentClient(fixedOwner(1000, true), 1000).Get(listener.URL)
	if err != nil {
		t.Fatalf("this account's own listener was refused: %v", err)
	}
	_ = resp.Body.Close()
}

// `neverseen status` on the account that lost the port: "not answering, start it" sent
// somebody to start an agent that cannot bind. It says who holds the address instead —
// and when that could not be told, says so rather than blaming another account.
func TestStatusSaysWhoHoldsTheAddress(t *testing.T) {
	listener := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(listener.Close)
	saved := agentClient
	t.Cleanup(func() { agentClient = saved })

	cases := []struct {
		name         string
		lookup       ownerLookup
		says, denies string
	}{
		{"another account", fixedOwner(1001, true), "another account", "could not be told"},
		{"an account that cannot be told", fixedOwner(0, false), "could not be told", "another account"},
	}
	for _, c := range cases {
		agentClient = newAgentClient(c.lookup, 1000)

		status := Query(t.Context(), strings.TrimPrefix(listener.URL, "http://"), time.Second)
		if status.Answering || status.Foreign == nil {
			t.Fatalf("%s: Answering %v, Foreign %v; want false and a reason", c.name, status.Answering, status.Foreign)
		}

		var out bytes.Buffer
		status.Write(&out)
		if !strings.Contains(out.String(), c.says) || strings.Contains(out.String(), c.denies) {
			t.Errorf("%s: the report should say %q and not %q:\n%s", c.name, c.says, c.denies, out.String())
		}
	}
}
