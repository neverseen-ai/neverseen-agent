package proxy

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// The /settings page is where what the agent masks is actually configured.
//
// It exists because the menu bar was the only place to configure it and the menu
// bar is the wrong shape for the job. fyne.io/systray gives titles and ticks and
// nothing else: no radio group, no mixed tick, no room for the sentence that says
// what a choice costs. Every one of those absences had been answered by writing the
// sentence into the entry's own title — "weak — every value found, words included;
// masks code too" — and a menu bar of sixty-character rows is a menu nobody reads.
// The page has the room the menu never had, so the menu can go back to being short.
//
// # It carries the control key, and that is what shapes the route
//
// PUT /policy is the one route that changes what this agent does, closed by a
// header a page from the internet cannot set (policy.go says why). A page served by
// this agent is same-origin with it, so it *can* set that header — provided it
// holds the secret. This route hands it over.
//
// That is a secret in a response body, so the route is closed twice before the key
// is ever read:
//
//   - Loopback only, hard, whatever -l bound. The warn-rather-than-refuse trade
//     that lets /healthz and /test answer the network does not transfer: those
//     describe a configuration, this one gives away the ability to switch masking
//     off.
//   - The Host must name this machine. A loopback check alone is not enough against
//     DNS rebinding: a page on the internet whose domain resolves to 127.0.0.1
//     reaches this route *from* the browser — a loopback source — and, being
//     same-origin by the browser's reckoning, gets to read the body. Refusing a Host
//     that is not localhost is what closes that, and it is the only defence here,
//     since the agent implements no CORS and must not.
//
// Neither guard tells one account on this machine from another: TCP on the loopback
// interface carries no identity, and another user's `curl -H 'Host: localhost'` passed
// both and was handed the key. So the key is in the page only when the request carries
// a ticket, and a ticket is minted by POST /settings — which needs the key. Whoever
// opens the page with the key in it has therefore proved they can read
// ~/.neverseen/control.key, which is 0600 and the owner's. The menu bar and
// `neverseen settings` mint one; a page typed by hand is served without the key and
// says where to open it from.
//
// A ticket works once and for a minute, because it travels on a command line —
// `open`, `xdg-open` — and another account can read those. Used once, a copy read off
// the process list is worthless by the time it is read; and if it is read first, the
// owner's own page is the one refused, which they see. The page keeps the key in its
// tab's sessionStorage, which is scoped to this origin and this tab, so a reload works
// without the ticket.
//
// On Linux the race is closed one step earlier: a ticket read off the process list is
// presented by another account, and guardAccount refuses that account before the
// ticket is looked at (peer.go).
//
// TODO: on macOS and Windows the race above is the ceiling — another account polling
// the process list can still beat the browser to a ticket. The upgrade is to tell
// accounts apart there too (peer.go), or the one Jupyter made: write the
// link into a 0600 file and open the file, so no command line carries it. It was not
// taken because Ubuntu's snap Firefox cannot read a hidden directory under $HOME,
// which is where that file would live.

// settingsPolicy is what the page is allowed to load and reach.
//
// 'unsafe-inline' twice because the style and the script are inline, which is the
// deliberate shape: one response, one origin, nothing fetched. Neither weakens what
// this policy is for — 'none' by default and 'self' for connections is what bounds
// where the control key can go.
//
// frame-ancestors because default-src does not cover it: framed by a site the owner
// went on to in the same tab, the page's switches could be clicked through a decoy.
const settingsPolicy = "default-src 'none'; style-src 'unsafe-inline'; " +
	"script-src 'unsafe-inline'; connect-src 'self'; form-action 'none'; base-uri 'none'; " +
	"frame-ancestors 'none'"

//go:embed settings.html
var settingsHTML string

// The console, inlined into the page rather than served at paths of its own: the
// React application in console/, built into internal/proxy/console/ by `make console`
// and committed, so `go build` needs no Node.
//
// One response with one inline script and one inline style: no second route to
// reserve, and a policy of `default-src 'none'` with nothing to exempt. Both files
// are build-time assets of this binary, never anything a caller sent, which is what
// makes template.JS and template.CSS safe here. The one way such a file could still
// break out of its element is to carry its own closing tag, and
// TestTheConsoleCannotCloseItsOwnElement holds that.
//
//go:embed console/console.js
var consoleScript string

//go:embed console/console.css
var consoleStyle string

var settingsTemplate = template.Must(template.New("settings").Parse(settingsHTML))

// settingsView is what the page is rendered with.
//
// The key and nothing else about the configuration: the page draws itself from
// /healthz like every other surface, rather than from a snapshot baked into its own
// HTML at the moment it was served. A page rendered with the state would be showing
// what was true when it loaded, and the first thing another surface changed would
// make it a liar.
type settingsView struct {
	ControlKey string

	// Script and Style are the console.
	Script template.JS
	Style  template.CSS
}

// handleSettings serves the configuration page on GET, and mints the ticket that puts
// the key in it on POST.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "neverseen: use GET to open the settings page, or POST with the control key for a ticket",
			http.StatusMethodNotAllowed)
		return
	}

	// Both guards before the key is looked at, so a caller this refuses is never
	// within one bug of being handed it.
	if !fromLoopback(r.RemoteAddr) {
		http.Error(w, "neverseen: this page is served on the loopback interface only",
			http.StatusForbidden)
		return
	}
	if !namesThisMachine(r.Host) {
		http.Error(w, "neverseen: this page is served under localhost only",
			http.StatusForbidden)
		return
	}

	if s.controlKey == "" {
		// The same direction PUT /policy takes with no key: refuse, rather than
		// serve a page whose every control would fail on submission with nothing
		// saying why.
		http.Error(w, "neverseen: no control key on this agent, so nothing here could be changed",
			http.StatusServiceUnavailable)
		return
	}

	if r.Method == http.MethodPost {
		s.issueTicket(w, r)
		return
	}

	// Spent whether or not it was still live, so a ticket read off a process list is
	// worth nothing once the owner's browser has used it.
	key := ""
	if s.tickets.redeem(r.URL.Query().Get(ticketParam)) {
		key = s.controlKey
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A page holding a secret has no business in a disk cache, and the agent it
	// describes moves under it besides.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Defence in depth on the one page with something worth stealing. The page is
	// self-contained — inline style, inline script, no external resource, one fetch
	// to its own origin — so the policy costs nothing to satisfy, and `connect-src
	// 'self'` is the load-bearing clause: it makes the control key unsendable
	// anywhere but back to this agent. There is no injection point today, every
	// agent-supplied string reaches the DOM through textContent and the key is
	// escaped by html/template; this is what keeps that true of a string somebody
	// adds later.
	w.Header().Set("Content-Security-Policy", settingsPolicy)
	view := settingsView{ControlKey: key, Script: template.JS(consoleScript), Style: template.CSS(consoleStyle)}
	if err := settingsTemplate.Execute(w, view); err != nil {
		// The status line has already gone out, so this can only be reported.
		s.log.Error("rendering the settings page failed", "error", err)
	}
}

// issueTicket answers POST /settings: a ticket for whoever proved they hold the key.
func (s *Server) issueTicket(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(w, r) {
		return
	}
	value, err := s.tickets.issue()
	if err != nil {
		s.log.Error("minting a settings ticket failed", "error", err)
		http.Error(w, "neverseen: could not mint a ticket for the settings page",
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, settingsTicket{Ticket: value})
}

// settingsTicket is what POST /settings answers.
type settingsTicket struct {
	Ticket string `json:"ticket"`
}

// ticketParam is the query parameter a ticket travels in.
const ticketParam = "t"

// ticketTTL is how long a ticket waits to be used: long enough for a browser that has
// to start first, short enough that a link left in a history is dead.
const ticketTTL = time.Minute

// tickets are the one-time links POST /settings mints.
//
// A handful at most, so a slice walked with a constant-time compare rather than a map:
// a lookup keyed by the secret is a comparison whose timing depends on it.
type tickets struct {
	mu     sync.Mutex
	issued []ticket

	// now is the clock, so expiry is tested at a pinned instant. Nil is time.Now.
	now func() time.Time
}

type ticket struct {
	value   string
	expires time.Time
}

func (t *tickets) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// issue mints a ticket, dropping the expired ones on the way. The same shape as the
// control key: 32 random bytes in hex.
func (t *tickets) issue() (string, error) {
	value, err := newControlKey()
	if err != nil {
		return "", err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock()
	t.issued = slices.DeleteFunc(t.issued, func(k ticket) bool { return !now.Before(k.expires) })
	t.issued = append(t.issued, ticket{value: value, expires: now.Add(ticketTTL)})
	return value, nil
}

// redeem reports whether value is a live ticket, and spends it either way.
func (t *tickets) redeem(value string) bool {
	if value == "" {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	for i, k := range t.issued {
		if subtle.ConstantTimeCompare([]byte(value), []byte(k.value)) == 1 {
			t.issued = slices.Delete(t.issued, i, i+1)
			return t.clock().Before(k.expires)
		}
	}
	return false
}

// SettingsURL asks the agent for a one-time link to its settings page.
//
// The one place a surface asks for it, as SetPolicy is the one place a surface writes:
// the menu bar opens what this returns and `neverseen settings` prints it.
func SettingsURL(ctx context.Context, addr, key string, timeout time.Duration) (string, error) {
	if addr == "" {
		addr = DefaultListen
	}
	// The page is served on loopback, under a loopback name, whatever -l bound. An
	// agent listening on 0.0.0.0:9787 or :9787 is reached there too, and a link naming
	// the bound address would be refused by namesThisMachine. One bound to a single
	// other address does not listen on loopback at all: dialled there, it answered
	// "connection refused", which says nothing about why.
	if host, port, err := net.SplitHostPort(addr); err == nil && !namesThisMachine(addr) {
		if ip := net.ParseIP(host); host != "" && (ip == nil || !ip.IsUnspecified()) {
			return "", fmt.Errorf("the settings page is served on loopback alone, and an agent "+
				"bound to %s does not listen there: bind it to 127.0.0.1 or 0.0.0.0", addr)
		}
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	if key == "" {
		return "", fmt.Errorf("no control key in %s, so the settings page cannot be opened with one",
			DefaultControlKeyFile)
	}

	code, answer, err := askAgent(ctx, timeout, http.MethodPost, "http://"+addr+"/settings", key, nil, 1024)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		// The agent's own words, for the reason SetPolicy keeps them.
		return "", errors.New(strings.TrimSpace(string(answer)))
	}
	var reply settingsTicket
	if err := json.Unmarshal(answer, &reply); err != nil || reply.Ticket == "" {
		return "", errors.New("the agent answered something that is not a ticket")
	}
	return "http://" + addr + "/settings?" + ticketParam + "=" + url.QueryEscape(reply.Ticket), nil
}

// namesThisMachine reports whether a Host header names the loopback interface.
//
// The defence against DNS rebinding, and it is asked of the *name* the browser
// used rather than of the address the request came from — those are the same thing
// for a person typing the URL and deliberately different for the attack, which is
// a loopback request carrying somebody else's domain.
//
// A Host with no port is accepted as written: net.SplitHostPort refuses it, and
// "localhost" alone is what a request to port 80 carries.
//
// The port is checked to be one, because SplitHostPort does not check: it splits
// "localhost:9787@example.com" into a host of "localhost" and a port of the rest, and
// the name this guard read was then the one it wanted to see. No browser sends such a
// Host, and the rebinding attack this exists for needs a browser — but "no real client
// does that" is the argument that ages badly, on one of the two guards standing in
// front of the only body carrying the control key.
func namesThisMachine(host string) bool {
	name, ok := hostName(host)
	if !ok {
		return false
	}
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// hostName is the name half of a Host header, brackets off, or false when the port
// half is not a port. One parser for both Host guards, so the port check above
// cannot be fixed in one of them and not the other.
func hostName(host string) (string, bool) {
	name := host
	if h, port, err := net.SplitHostPort(host); err == nil {
		if !isPort(port) {
			return "", false
		}
		name = h
	}
	return strings.TrimSuffix(strings.TrimPrefix(name, "["), "]"), true
}

// isPort reports whether a Host's port half is one. Empty counts: "127.0.0.1:" is a
// shape net/http produces for a request to the default port.
func isPort(port string) bool {
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
