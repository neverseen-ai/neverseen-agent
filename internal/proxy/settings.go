package proxy

import (
	_ "embed"
	"html/template"
	"net"
	"net/http"
	"strings"
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
// What it does not add: a local process of this user could already read
// ~/.neverseen/control.key, which is 0600 and belongs to them. Against that reader
// this route gives away nothing new. Against every other reader the two guards above
// are the whole answer.

// settingsPolicy is what the page is allowed to load and reach.
//
// 'unsafe-inline' twice because the style and the script are inline, which is the
// deliberate shape: one response, one origin, nothing fetched. Neither weakens what
// this policy is for — 'none' by default and 'self' for connections is what bounds
// where the control key can go.
const settingsPolicy = "default-src 'none'; style-src 'unsafe-inline'; " +
	"script-src 'unsafe-inline'; connect-src 'self'; form-action 'none'; base-uri 'none'"

//go:embed settings.html
var settingsHTML string

// The decisions, inlined into the page rather than served at a path of their own.
//
// One response with one inline script: no second route to reserve, and a policy of
// `default-src 'none'` with nothing to exempt. The file is a build-time asset of this
// binary, never anything a caller sent, which is what makes template.JS safe here.
//
//go:embed settings_decisions.js
var settingsDecisions string

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

	// Decisions is settings_decisions.js, inlined ahead of the page's own script.
	Decisions template.JS
}

// handleSettings serves the configuration page.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "neverseen: use GET to open the settings page", http.StatusMethodNotAllowed)
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
	view := settingsView{ControlKey: s.controlKey, Decisions: template.JS(settingsDecisions)}
	if err := settingsTemplate.Execute(w, view); err != nil {
		// The status line has already gone out, so this can only be reported.
		s.log.Error("rendering the settings page failed", "error", err)
	}
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
	name := host
	if h, port, err := net.SplitHostPort(host); err == nil {
		if !isPort(port) {
			return false
		}
		name = h
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")

	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
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
