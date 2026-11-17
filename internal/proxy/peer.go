package proxy

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
)

// Which account is at the other end of a loopback connection.
//
// TCP on the loopback interface carries no identity, and this agent runs as one user:
// on a machine with several accounts — a shared Linux server, RDS, two sessions on one
// Mac — every one of them reaches 127.0.0.1:9787. Two leaks followed from that. Another
// account's request landed in this user's session (sessionOf falls back to "default"),
// so asking the model to repeat [EMAIL_1] brought this user's value back restored,
// credentials included. And the first account to bind the port received everybody
// else's traffic — prompts, provider credentials, the control key — because no surface
// asked who was listening before it spoke.
//
// The kernel knows both answers, since every socket has an owner. So the agent refuses
// a caller on this machine that is not its own account (guardAccount), and every local surface
// refuses a listener that is not its user's before a byte is sent (agentClient). Root is
// accepted both ways: it can read control.key and the vault already, and refusing it
// would protect nothing.
//
// A loopback socket that cannot be found is refused rather than waved through: it is
// on this machine, so it is in the table. Any other is asked too — an account on this
// machine reaching an agent bound beyond loopback through the machine's own address —
// and only one the table does not hold is taken for another machine's.
//
// TODO: Linux only, through /proc/net/tcp. Windows can ask GetExtendedTcpTable, which
// names the process owning each connection without privileges, and then that process's
// token. macOS has no unprivileged view of another user's sockets; the agent would have
// to look for the peer among its own user's processes and refuse when it is not there.
// Until then, on both, any account on the machine reaches this agent as its owner does
// — and holds the key within five seconds by binding the port while this account's
// agent is down, with nobody clicking anything: the menu bar asks GET /today with the
// key on every poll.

// ownerLookup names the account owning the socket whose own end is local and whose far
// end is remote, or reports false when no such socket is known.
type ownerLookup func(local, remote netip.AddrPort) (uid uint32, ok bool)

// rootAccount is accepted wherever this account is: see the note above.
const rootAccount = 0

// sameAccount reports whether uid may stand for this agent's own account.
func sameAccount(uid, own uint32) bool {
	return uid == own || uid == rootAccount
}

// guardAccount refuses a caller that belongs to another account on this
// machine.
//
// Outside every other guard, so another account learns nothing from any route —
// /healthz included, which is what `neverseen env` asks before pointing its tools here.
func (s *Server) guardAccount(next http.Handler) http.Handler {
	if s.ownerOf == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, known, refused := s.foreignCaller(r)
		if !refused {
			next.ServeHTTP(w, r)
			return
		}
		if !known {
			s.log.Warn("refused a request whose account could not be told", "path", r.URL.Path)
			http.Error(w, "neverseen: this agent cannot tell which account this request comes from, "+
				"so it refuses it", http.StatusForbidden)
			return
		}
		s.log.Warn("refused a request from another account on this machine", "uid", uid, "path", r.URL.Path)
		http.Error(w, "neverseen: this agent belongs to another account on this machine and refuses "+
			"yours. Run your own on another address: "+EnvListen+"=127.0.0.1:<port> neverseen proxy",
			http.StatusForbidden)
	})
}

// accountDecision is what ConnContext decided about the account behind one connection.
type accountDecision struct {
	uid            uint32
	known, refused bool
}

// accountDecisionKey carries, in a connection's context, the function that decides its
// accountDecision once.
type accountDecisionKey struct{}

// ConnContext has the account at the other end decided once per connection, for
// guardAccount to read on every request the connection carries. Wire it as
// http.Server.ConnContext.
//
// A connection's owner does not change while it is open, and keep-alive puts many
// requests on one — the menu bar's two every five seconds, every turn of a
// conversation — each of which read the kernel's tables whole while the guard asked per
// request. A server built without it is still guarded, per request.
//
// Decided on the connection's first request, not here: net/http calls ConnContext from
// its accept loop, so a lookup here held every new connection — the owner's tools'
// included — behind the tables being read for the one before.
func (s *Server) ConnContext(ctx context.Context, c net.Conn) context.Context {
	if s.ownerOf == nil {
		return ctx
	}
	peer, here := c.RemoteAddr().String(), c.LocalAddr().String()
	decide := sync.OnceValue(func() accountDecision {
		var d accountDecision
		d.uid, d.known, d.refused = foreignPeer(s.ownerOf, s.account, peer, here)
		return d
	})
	return context.WithValue(ctx, accountDecisionKey{}, decide)
}

// foreignCaller reports whether a request is to be refused for its account, and which
// account that was when it is known.
func (s *Server) foreignCaller(r *http.Request) (uid uint32, known, refused bool) {
	if decide, ok := r.Context().Value(accountDecisionKey{}).(func() accountDecision); ok {
		d := decide()
		return d.uid, d.known, d.refused
	}
	// Set by every net/http server on every request. Absent, the request did not come
	// over a connection — a test calling the handler — and there is no socket to ask.
	here, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return 0, false, false
	}
	return foreignPeer(s.ownerOf, s.account, r.RemoteAddr, here.String())
}

// foreignPeer reports whether the far end of a connection belongs to another account,
// and which account that was when it is known. The socket asked about is the peer's:
// its own end is the peer's address, and its far end is ours.
//
// One function for both directions — the agent asking who calls, a surface asking who
// listens — so a change to what is refused cannot reach one and miss the other.
func foreignPeer(ownerOf ownerLookup, own uint32, peer, here string) (uid uint32, known, refused bool) {
	theirs, err := netip.ParseAddrPort(peer)
	if err != nil {
		return 0, false, true
	}
	ours, err := netip.ParseAddrPort(here)
	if err != nil {
		return 0, false, true
	}
	// Asked of every peer, not of loopback alone: an agent bound beyond loopback is
	// also reached through this machine's own address, and another account doing so
	// has a socket in the table like any loopback caller.
	uid, known = ownerOf(theirs, ours)
	if !known && !theirs.Addr().Unmap().IsLoopback() {
		// A peer on another machine has no socket in this one's table. A caller there
		// reached the agent only because -l said so, which is BeyondLoopback's warning
		// and not this guard's; an agent there was reached because somebody configured
		// that.
		return 0, false, false
	}
	return uid, known, !known || !sameAccount(uid, own)
}

// ForeignListenerError is what a local surface gets when the address it was given is
// held by another account's listener.
type ForeignListenerError struct {
	Addr  string
	UID   uint32
	Known bool
}

func (e *ForeignListenerError) Error() string {
	if !e.Known {
		return fmt.Sprintf("cannot tell which account is listening on %s, so nothing was sent to it", e.Addr)
	}
	return fmt.Sprintf("%s is held by another account on this machine (uid %d), so nothing was sent to it",
		e.Addr, e.UID)
}

// newAgentClient is the client every local surface reaches the agent with.
//
// Its dialer asks who owns the listener before the request is written, because what
// these requests carry is the control key — and the answer to Query decides whether
// `neverseen env` points every tool on this account, with its provider credentials, at
// that address.
//
// Never through a proxy: the environment's HTTP_PROXY has no business between a
// surface and an agent on this machine.
func newAgentClient(ownerOf ownerLookup, own uint32) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil || ownerOf == nil {
			return conn, err
		}
		agent, us := conn.RemoteAddr().String(), conn.LocalAddr().String()
		if uid, known, refused := foreignPeer(ownerOf, own, agent, us); refused {
			_ = conn.Close()
			return nil, &ForeignListenerError{Addr: addr, UID: uid, Known: known}
		}
		return conn, nil
	}
	return &http.Client{Transport: transport}
}

// agentClient is newAgentClient for this process's own account.
var agentClient = newAgentClient(accountLookup())

// ownerIn finds, in a /proc/net/tcp or /proc/net/tcp6 table, the owner of the socket
// whose own end is local and whose far end is remote.
//
// A socket no process holds is never read for its owner. The table marks one with inode
// 0 and, before Linux 6.17, with uid 0 — and read as root's it passed wherever root
// does. It is either a connection the kernel has completed and its listener not yet
// accepted, which is what a surface asks about the moment it has connected — another
// account binding the port and accepting late was handed the control key — or a socket
// its process has closed, in TIME_WAIT or on its way there, under whose tuple a caller
// that hung up at once passed as root. The first belongs to whoever listens on its own
// end, and a listening socket always names its owner; the second has no listener, so it
// is not found, and a loopback socket that is not found is refused.
//
// Kept apart from reading the file so the parsing is tested on every platform, with
// the byte order given rather than assumed.
func ownerIn(table string, local, remote netip.AddrPort, order binary.ByteOrder) (uint32, bool) {
	// Without the zone too: net/http names a link-local peer fe80::1%eth0 and the table
	// never carries one. Compared with it, another account's socket was never found,
	// and a link-local address is not loopback, so foreignPeer took it for another
	// machine's and let it through.
	local = netip.AddrPortFrom(local.Addr().Unmap().WithZone(""), local.Port())
	remote = netip.AddrPortFrom(remote.Addr().Unmap().WithZone(""), remote.Port())

	// The listener on local's address, else one on every address of local's port: the
	// owner of a connection this machine holds and no process has accepted yet (pending),
	// and of nothing else. A pair with no line at all is no socket here — another
	// machine's end, an agent this one is pointed at — and is answered as not found, or a
	// stranger listening on every address of that port would own it.
	var listener, wildcard struct {
		uid   uint32
		found bool
	}
	pending := false
	for i, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) < 10 {
			continue
		}
		mine, ok := procAddr(fields[1], order)
		if !ok || mine.Port() != local.Port() {
			continue
		}
		uid, err := strconv.ParseUint(fields[7], 10, 32)
		if err != nil {
			continue
		}
		if fields[3] == "0A" { // LISTEN
			switch {
			case mine.Addr() == local.Addr():
				listener.uid, listener.found = uint32(uid), true
			case mine.Addr().IsUnspecified():
				wildcard.uid, wildcard.found = uint32(uid), true
			}
			continue
		}
		if mine != local {
			continue
		}
		theirs, ok := procAddr(fields[2], order)
		if !ok || theirs != remote {
			continue
		}
		if fields[9] == "0" { // inode 0: no process holds it, so the uid is not its owner's
			pending = true
			continue
		}
		return uint32(uid), true
	}
	if !pending {
		return 0, false
	}
	if listener.found {
		return listener.uid, true
	}
	return wildcard.uid, wildcard.found
}

// procAddr reads one address of a /proc/net/tcp line: hex address, colon, hex port.
//
// The kernel prints each 32-bit word of the address as a number in the machine's own
// byte order, so 127.0.0.1 reads 0100007F on a little-endian machine. Each word is put
// back into memory in that order, which leaves the bytes in network order.
func procAddr(field string, order binary.ByteOrder) (netip.AddrPort, bool) {
	hexAddr, hexPort, ok := strings.Cut(field, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(hexAddr)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	for i := 0; i < len(raw); i += 4 {
		order.PutUint32(raw[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	addr, _ := netip.AddrFromSlice(raw)
	return netip.AddrPortFrom(addr.Unmap(), uint16(port)), true
}
