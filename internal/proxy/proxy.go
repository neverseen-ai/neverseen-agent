// Package proxy is the agent's request path: it masks what goes out and puts the
// originals back in what comes in.
//
// There is one pipeline, and every provider runs it. That is the whole design
// decision. The project this one replaces had two, one per entrypoint, and they
// drifted until the same request was masked in one and answered in clear in the
// other — a divergence nothing could have noticed, because each path had its own
// tests and both passed.
package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/internal/telemetry"
	"github.com/neverseen-ai/neverseen-agent/internal/vault"
	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
	pkgtelemetry "github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// Config is what the proxy needs beyond a detector and a vault.
type Config struct {
	// Providers are the upstreams, addressed by the first path segment. Empty
	// means DefaultProviders.
	Providers []Provider

	// Logger receives one line per exchange, carrying counts and never content.
	// Nil discards them.
	Logger *slog.Logger

	// Audit receives one line per value replaced or restored, values in clear.
	// Nil — the ordinary case — means nothing is printed at all. Only `neverseen
	// proxy -a` sets it; see audit.go for why this one surface may see content.
	Audit io.Writer

	// Traces, when set, records both bodies of every exchange into a directory, one
	// file per exchange. Nil everywhere but `neverseen proxy -v` — see trace.go for
	// why the only thing this agent writes to disk in clear takes a flag on a
	// foreground command and nothing else.
	Traces *tracer

	// Recorder accumulates what a supervised agent reports. Nil means one is
	// created anyway: counting costs a mutex and the request path then has one
	// shape rather than two, with no branch that only runs where nobody looked.
	Recorder *telemetry.Recorder

	// ControlKey authenticates the route that changes what is masked. Empty means
	// that route refuses everything, which is what an agent that could not read
	// its key must do.
	ControlKey string

	// Listen is the address the agent will be served on, so State can say whether
	// it is reachable beyond loopback. The server does not listen itself — the
	// command does — so this is told rather than known. Empty reads as loopback.
	Listen string

	// PolicyFile is where a change made through PUT /policy is stored, so it
	// survives a restart. Empty stores nothing, which is what every test that
	// builds a server by hand wants: the default path belongs to FromEnv, as the
	// control key's does, so a unit test cannot write into the operator's own
	// state.
	PolicyFile string
}

// Server is the agent's HTTP front.
type Server struct {
	det      *detector.Detector
	vault    *vault.Vault
	log      *slog.Logger
	recorder *telemetry.Recorder

	// audit is nil unless the agent was built in audit mode. Its methods are
	// nil-safe, so the request path calls them without a branch.
	audit *auditor

	// said holds back a warning already given, so one that a conversation provokes
	// every turn is read once.
	said said

	providers []Provider
	routes    map[string]*httputil.ReverseProxy

	// hosts is where each route actually goes, by code, because one decision on
	// the request path is about the destination and not the name — see
	// identifierHost.
	hosts map[string]string

	// startedAt is when this process began serving, so a supervision backend can
	// show uptime and spot an agent restarting in a loop.
	startedAt time.Time

	// controlKey authenticates the one route that changes what this agent masks.
	// Empty means that route refuses everything, which is the safe direction — see
	// policy.go.
	controlKey string

	// policyFile is where that route stores what it applied, so the next start
	// finds it. Empty stores nothing — see policyfile.go.
	policyFile string

	// exposed records that the agent was told it would listen beyond loopback —
	// see State.Exposed.
	exposed bool

	// listenName is the host name the listen address gave, when it gave a name
	// rather than an address — the one name besides localhost a client has been
	// told to reach this agent by. See answersFor.
	listenName string

	// policyMu serialises PUT /policy, which is a read-modify-write of the file
	// above: apply, read the detector back, store. Two surfaces click at once and
	// the halves interleave.
	policyMu sync.Mutex

	// today counts the values hidden since local midnight, for GET /today.
	today tally

	// menuBarMu serialises PUT /today, which writes menubar.json beside the policy.
	menuBarMu sync.Mutex

	// extensionSeen is when the browser extension last called /mask, for the
	// settings page. Nil until it does.
	extensionSeen atomic.Pointer[time.Time]

	// tickets are the one-time links that open /settings with the key in it — see
	// settings.go.
	tickets tickets

	// ownerOf names the account behind a loopback connection, and account is this
	// agent's own; guardAccount refuses every other. A nil ownerOf is a platform that
	// cannot ask yet — see peer.go.
	ownerOf ownerLookup
	account uint32
}

// New builds the server.
func New(cfg Config, det *detector.Detector, v *vault.Vault) (*Server, error) {
	if det == nil || v == nil {
		return nil, errors.New("a detector and a vault are both required")
	}

	providers := cfg.Providers
	if len(providers) == 0 {
		providers = DefaultProviders
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	recorder := cfg.Recorder
	if recorder == nil {
		recorder = telemetry.NewRecorder(time.Now())
	}

	s := &Server{
		det:        det,
		vault:      v,
		log:        logger,
		recorder:   recorder,
		audit:      newAuditor(cfg.Audit, cfg.Traces),
		providers:  providers,
		routes:     make(map[string]*httputil.ReverseProxy, len(providers)),
		hosts:      make(map[string]string, len(providers)),
		startedAt:  time.Now(),
		controlKey: cfg.ControlKey,
		policyFile: cfg.PolicyFile,
		exposed:    cfg.Listen != "" && BeyondLoopback(cfg.Listen),
		listenName: listenName(cfg.Listen),
	}
	s.ownerOf, s.account = accountLookup()
	s.loadMenuBar()

	for _, p := range providers {
		if slices.Contains(reservedRoutes, p.Code) {
			return nil, fmt.Errorf("provider %q takes a path the agent answers itself; "+
				"reserved: %s", p.Code, strings.Join(reservedRoutes, ", "))
		}

		base, err := url.Parse(p.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Code, err)
		}
		if base.Scheme == "" || base.Host == "" {
			return nil, fmt.Errorf("provider %q: %q is not an absolute URL", p.Code, p.BaseURL)
		}
		s.routes[p.Code] = s.reverseProxy(base)
		s.hosts[p.Code] = base.Hostname()
	}
	return s, nil
}

func (s *Server) reverseProxy(base *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(base)
			// The provider routes on Host, and without this it still says
			// localhost — which most of them answer with a certificate error.
			pr.Out.Host = base.Host

			// Dropped so Go's transport negotiates compression itself and
			// decompresses transparently. Forwarding the caller's header means
			// the response arrives compressed and the rehydrator has bytes it
			// cannot read — which would send the caller its own tokens back.
			pr.Out.Header.Del("Accept-Encoding")
		},
		ModifyResponse: func(resp *http.Response) error {
			if err := s.unmask(resp); err != nil {
				return answeredError{err}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// A caller that hung up before the provider answered is not a
			// provider that failed: counted as one, every abandoned prompt
			// showed up on the fleet view as an outage, and the 502 has nobody
			// left to read it.
			if errors.Is(err, context.Canceled) {
				return
			}
			// ReverseProxy hands ModifyResponse's errors here too. That provider
			// answered, and unmask counted the answer: counted again as no answer,
			// one exchange read as an OK and an outage.
			if !errors.As(err, new(answeredError)) {
				s.countUpstream(0)
			}
			s.log.Error("upstream failed", "path", r.URL.Path, "error", err)
			http.Error(w, "neverseen: the provider could not be reached", http.StatusBadGateway)
		},
	}
}

// reservedRoutes are the paths the agent answers itself. A provider may not take
// one of these codes: "/healthz" would reach the agent while "/healthz/v1/…"
// reached the provider, which is a routing table nobody could reason about.
var reservedRoutes = []string{"healthz", "test", "settings", "policy", "mask", "unmask", "prove", "today"}

// Handler returns the agent's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/policy", s.handlePolicy)
	mux.HandleFunc("/test", s.handleTest)
	mux.HandleFunc("/settings", s.handleSettings)
	mux.HandleFunc("/mask", s.handleMask)
	mux.HandleFunc("/unmask", s.handleUnmask)
	mux.HandleFunc("/prove", s.handleProve)
	mux.HandleFunc("/today", s.handleToday)
	mux.HandleFunc("/", s.forward)
	return s.guardAccount(s.guardHost(mux))
}

// guardHost refuses a request addressed to a name that is not this agent's.
//
// The defence against DNS rebinding for every route. A page that rebinds its own
// domain to 127.0.0.1 is same-origin with the agent: it can post to
// /anthropic/v1/messages with its own key, ask the model to repeat [EMAIL_1]
// [SECRET_1], and read the restored answer — credentials included. CORS is no help
// after a rebind, and the control key guards only the routes that change things.
// What the page cannot change is the Host its browser sends, which carries the
// rebound domain.
//
// /settings keeps its stricter guard (namesThisMachine): it carries the control key,
// and loopback only. This one admits any address, because a rebinding attack always
// arrives under a domain name and never under an IP literal — so an agent bound
// beyond loopback stays reachable by its LAN address.
//
// TODO: a name other than localhost or the one -l gave — host.docker.internal from
// a container, a hosts-file alias, the machine's .local name — is refused. The
// upgrade is a configured list of extra names, the day a deployment asks for one.
func (s *Server) guardHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.answersFor(r.Host) {
			s.log.Warn("refused a request addressed to another host", "host", r.Host, "path", r.URL.Path)
			http.Error(w, fmt.Sprintf("neverseen: this agent does not answer for %q", r.Host),
				http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// answersFor reports whether a Host header names this agent: localhost, any IP
// literal, the name the listen address gave, or no name at all — ":9787" is what a
// local command sends when the listen address carries no host, and no browser can
// be made to send it.
func (s *Server) answersFor(host string) bool {
	name, ok := hostName(host)
	if !ok {
		return false
	}
	if name == "" || net.ParseIP(name) != nil || strings.EqualFold(name, "localhost") {
		return true
	}
	return s.listenName != "" && strings.EqualFold(name, s.listenName)
}

// listenName is the host name a listen address carries, or "" when it carries an
// address or nothing.
func listenName(listen string) string {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) != nil {
		return ""
	}
	return host
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.healthNow())
}

// healthNow is what the agent says about itself at this moment.
//
// One function, called by the route and by the reply to a policy change, so a
// surface that has just switched a category off is told the state in the same
// shape it reads on every poll. Two answers to "what are you doing" is how a menu
// comes to disagree with the traffic.
func (s *Server) healthNow() Health {
	// Marshalled from the type a local caller decodes, in this same package, so
	// the route cannot grow a field on one side only.
	return Health{
		Status:       "ok",
		Version:      Version,
		Locales:      s.det.Locales(),
		Substitution: s.det.Substitution().String(),
		SecretLevel:  s.det.SecretLevel().String(),
		Providers:    s.Providers(),
		Masking:      s.det.Masking().String(),

		// From the registry rather than from the configuration: this is what the
		// build can load, not what it has loaded, and it is the list a surface
		// offering a choice has to draw.
		// The two choices from a closed set, offered by the agent that takes them.
		Substitutions: SubstitutionModes(),
		SecretLevels:  SecretLevels(),

		AvailableLocales: pii.LocaleCodes(),
		LocaleCategories: localeCategories(),
		Groups:           s.catalogue(),
		Off:              switchedOffCodes(s.det.Disabled()),
		Tools:            healthTools(),
	}
}

// healthTools is shellTools as Health serves it, in ToolCodes order.
func healthTools() []HealthTool {
	out := make([]HealthTool, 0, len(shellTools))
	for _, code := range ToolCodes() {
		t := shellTools[code]
		out = append(out, HealthTool{Provider: code, Variable: t.Variable, CLI: t.CLI, Caveat: t.Caveat})
	}
	return out
}

// localeCategories names what each country the build has can find.
//
// From the registry rather than from the detector, like AvailableLocales beside it:
// a country that is not loaded is exactly the one a reader is deciding about, so a
// list that only described the loaded ones would be silent about every choice on
// offer.
func localeCategories() map[string][]string {
	out := make(map[string][]string, len(pii.LocaleCodes()))
	for _, code := range pii.LocaleCodes() {
		cats := pii.CategoriesInLocale(code)
		labels := make([]string, 0, len(cats))
		for _, cat := range cats {
			labels = append(labels, pii.Label(cat))
		}
		out[code] = labels
	}
	return out
}

// catalogue is the whole catalogue as a surface needs to draw it: groups in
// display order, each with its categories, each carrying whether it is off and
// whether it may be.
//
// Served by the agent rather than read from pkg/pii by the caller, even though the
// menu bar could import the catalogue directly. The agent is the one applying it,
// so a menu built from its own copy would go on offering a category after a
// rebuilt agent stopped having one — and the picture would disagree with the
// traffic, which is the whole failure the single /healthz type exists to prevent.
func (s *Server) catalogue() []HealthGroup { return catalogueOf(s.det) }

// catalogueOf draws it for any detector, because the test page draws the same
// list for a simulated one — the same switches, in the same order, with the same
// labels, or the page and the menu would name one category two ways.
func catalogueOf(det *detector.Detector) []HealthGroup {
	off := make(map[pii.Category]bool)
	for _, cat := range det.Disabled() {
		off[cat] = true
	}

	// One walk over the loaded patterns for the two questions asked of them: which
	// categories are in play — only what this agent can actually find, since a switch
	// for a category no loaded locale can emit would say the agent is masking a value
	// it cannot recognise — and what each is recognised by.
	inPlay := det.Notations()

	var out []HealthGroup
	for _, g := range pii.Groups() {
		cats := categoriesOf(g, inPlay, off)
		// A family whose every category is out of play is not drawn: an empty
		// heading reads as a group the agent lost rather than one its locales never
		// loaded.
		if len(cats) == 0 {
			continue
		}

		out = append(out, HealthGroup{
			Code:  string(g),
			Label: pii.GroupLabel(g),
			// Asked of the whole family rather than of the categories in play, so
			// unloading a locale cannot move a heading under somebody's cursor.
			Credentials: pii.IsCredentialGroup(g),
			Categories:  cats,
		})
	}
	return out
}

// categoriesOf is one family's switches, in catalogue order, keeping only what this
// detector can find.
func categoriesOf(g pii.Group, inPlay map[pii.Category][]string,
	off map[pii.Category]bool) []HealthCategory {
	var out []HealthCategory
	for _, cat := range pii.CategoriesInGroup(g) {
		notations, found := inPlay[cat]
		if !found {
			continue
		}
		out = append(out, HealthCategory{
			Code:      string(cat),
			Label:     pii.Label(cat),
			Off:       off[cat],
			Locked:    !pii.Switchable(cat),
			Notations: notations,
		})
	}
	return out
}

// forward is the whole request path: pick the upstream, mask the body, remember
// what was masked, and hand it on.
func (s *Server) forward(w http.ResponseWriter, r *http.Request) {
	code, rest := splitProvider(r.URL.Path)

	route, ok := s.routes[code]
	if !ok {
		// Named explicitly rather than guessed at. A proxy that silently picked
		// a provider would send one vendor's key to another vendor.
		http.Error(w, fmt.Sprintf(
			"neverseen: no provider named %q. Point your client at /<provider>, one of: %s",
			code, strings.Join(providerCodes(s.providers), ", ")), http.StatusNotFound)
		return
	}

	session := sessionOf(r)
	ref, conversation, err := s.maskRequest(session, code, r)
	if err != nil {
		// Fail closed. Forwarding a body this could not read is exactly the
		// leak the agent exists to prevent, so an unreadable body is an error
		// rather than a pass-through.
		s.countRefused()
		s.log.Error("masking failed, refusing to forward", "session", session, "error", err)
		if errors.Is(err, errBodyTooLarge) {
			http.Error(w, "neverseen: "+err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "neverseen: the request body could not be masked", http.StatusUnsupportedMediaType)
		return
	}

	client := telemetry.ClientFamily(r.UserAgent())
	s.countRequest(conversation, client, code)

	r.URL.Path = rest
	r.Host = ""
	if ref != nil {
		ref.sent = time.Now()
	}
	ctx := withConversation(withSession(r.Context(), session), conversation)
	route.ServeHTTP(w, r.WithContext(withTrace(ctx, ref)))
}

// newPass starts a pass over what the session's mapping holds, and names every
// other value through the vault: the name the session gave it before, even once the
// mapping has expired, or a new one in the session's own numbering, which a restart
// does not reset — claimed in one step, so two requests in flight cannot give one
// value two names (vault.Name). One constructor for every route that stores what it
// masks, so the extension and the request path cannot disagree about a value's name.
//
// carried reports whether the text about to be masked already holds a name as text,
// so none is handed to a value (detector.Pass.Carried).
func (s *Server) newPass(session string, carried func(string) bool) *detector.Pass {
	pass := s.det.NewPass(s.vault.Load(session))
	pass.Carried = carried
	pass.Name = func(original, prefix string, render func(int64) (string, bool)) string {
		return s.vault.Name(session, original, prefix, render)
	}
	return pass
}

// maskRequest replaces the sensitive values in the body and records what it
// replaced, in the session's vault, before the request goes anywhere.
//
// It also returns the conversation the exchange belongs to, for the heartbeat's
// session counts — see conversationOf. The vault keeps its own session, because
// changing what scopes the mapping is a change to what the agent does, and this
// is a change to what it counts.
//
// It returns the exchange's trace, or nil when nothing is being recorded, so the
// answer can be appended to the file this half has just written.
func (s *Server) maskRequest(session, provider string, r *http.Request) (*traceRef, string, error) {
	if r.Body == nil || r.ContentLength == 0 {
		return nil, session, nil
	}

	body, err := readBody(r)
	if err != nil {
		return nil, session, err
	}

	// Searched in the raw body, where a token is written as it reads: none of its
	// characters is one JSON escapes. Once per name minted, under the vault's lock
	// (render runs there), so each first sighting costs one scan of the body.
	// TODO: a stand-in a client wrote escaped (`\u00e9`) is not found, so fake mode can
	// still hand out a name the body carries. The upgrade is to search the decoded
	// strings, once a fake-mode collision is seen.
	pass := s.newPass(session, func(name string) bool { return bytes.Contains(body, []byte(name)) })

	// Decoded once, here, because two things read the document: the exemption
	// below, from one member of it, and the masking, over every string in it.
	// A body that is not JSON is masked as flat text further down.
	doc, decodeErr := decodeJSONBody(body)

	// The identifiers naming this client to the provider that issued them are not
	// the caller's data, and a token in their place is a cost with no protection
	// bought — see identifiers.go. Decided on where the route goes, not what it is
	// called: the same code can be pointed at any host by NEVERSEEN_PROVIDERS.
	conversation := session
	if decodeErr == nil {
		pass.Exempt = exemptIdentifiers(s.hosts[provider], doc)
		conversation = conversationOf(session, s.hosts[provider], doc)
		s.warnUnread(session, doc)
	}

	// Collected rather than printed as they are found, so the console can write
	// the whole outbound half of one exchange in a single block: what arrived,
	// what was replaced in it, and what left. Two tools talking to the agent at
	// once would otherwise interleave their bodies on the screen.
	//
	// These are first sightings only — Reveal fires at minting — which is why the
	// count below is handed over separately. A body whose every value was already
	// in the session mapping produces no pair at all and is still a body in which
	// three values were replaced.
	var replaced [][2]string
	if s.audit != nil {
		pass.Reveal = func(original, replacement string) {
			replaced = append(replaced, [2]string{original, replacement})
		}
	}

	count := 0
	mask := func(text string) string {
		out, n := s.det.Mask(text, pass)
		count += n
		return out
	}

	// A JSON document is masked value by value; anything else as flat text. One
	// pass over the whole body either way, so a value repeated in two fields
	// keeps one identity.
	//
	// Timed around the scan alone: what a trace reports as the cost of masking has
	// to be the detector's work, not the bookkeeping that only exists because
	// somebody asked for a trace.
	scanned := time.Now()
	masked := ""
	if encoded, err := encodeMasked(doc, decodeErr, mask); err == nil {
		masked = string(encoded)
	} else {
		masked = mask(string(body))
	}
	if count == 0 {
		// Nothing replaced, so the bytes that arrived are the ones that leave. Re-encoded,
		// an indented body went out compacted and `é` or `\/` decoded — the same
		// document, but an agent with nothing to do should not be visible at all.
		masked = string(body)
	}
	masking := time.Since(scanned)

	// Both halves, side by side, before anything else can fail: the two bodies are
	// what an operator reads to see that the value they typed is not in the one
	// that left. The clear half is the reason this is a mode of its own — see
	// audit.go.
	path := s.audit.request(session, provider, string(body), masked, count, replaced)

	if err := s.vault.Save(session, pass.Minted()); err != nil {
		// The mapping is what makes the answer readable again. Masking without
		// storing would send the provider tokens the response path can never
		// expand, so the caller would read the agent's bookkeeping instead of
		// its own data.
		return nil, session, fmt.Errorf("store the session mapping: %w", err)
	}

	r.Body = io.NopCloser(strings.NewReader(masked))
	r.ContentLength = int64(len(masked))
	r.Header.Set("Content-Length", strconv.Itoa(len(masked)))
	r.Header.Del("Content-Encoding") // readBody has decompressed it

	if count > 0 {
		s.countMasked(conversation, pass)

		// Counts, never content: the log is the one place a masked value could
		// come back into the clear by accident.
		s.log.Info("request masked", "session", session, "values", count, "minted", len(pass.Minted()))
	}

	if path == "" {
		return nil, conversation, nil
	}
	return &traceRef{path: path, provider: provider, masking: masking}, conversation, nil
}

// requestMaxBytes caps a provider request body, measured after decompression.
//
// 32 MiB because that is Anthropic's own request limit: a body the provider would
// refuse anyway is one the agent need not hold in memory to mask. Measured on what
// gzip yields rather than on what arrived, because a few kilobytes of gzip can
// inflate to gigabytes, and the read is what buffers it.
const requestMaxBytes = 32 << 20

// errBodyTooLarge is a body over requestMaxBytes, refused rather than forwarded
// unread for the same reason an unreadable encoding is.
var errBodyTooLarge = fmt.Errorf("the request body is larger than %d bytes", requestMaxBytes)

// readBody returns the request body as text, decompressing it when it arrived
// compressed.
//
// An encoding it cannot read is an error, not a pass-through. The alternative —
// forwarding a body the agent could not inspect — is the one failure mode a data
// loss prevention tool must never have.
func readBody(r *http.Request) ([]byte, error) {
	var src io.Reader
	switch encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); encoding {
	case "", "identity":
		src = r.Body
	case "gzip":
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read a gzipped body: %w", err)
		}
		defer func() { _ = zr.Close() }()
		src = zr
	default:
		return nil, fmt.Errorf("cannot inspect a body encoded as %q", encoding)
	}

	body, err := io.ReadAll(io.LimitReader(src, requestMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > requestMaxBytes {
		return nil, errBodyTooLarge
	}
	return body, nil
}

// answeredError is unmask failing on an answer the provider did give, which the
// ErrorHandler must not count as one it did not.
type answeredError struct{ error }

func (e answeredError) Unwrap() error { return e.error }

// unmask puts the originals back into the response.
//
// Streaming and buffered responses go through the same expansion, so a value
// restored in one is restored in the other. They differ only in how much text
// the expander can see at a time, which is what the streaming path's held-back
// tail is for.
func (s *Server) unmask(resp *http.Response) error {
	// Wrapped before anything below can return early, so the answer reaches the
	// trace whatever happens to it afterwards. A session with no mapping, and a
	// body the expander will not read, are still the provider's answer to a
	// request the file already holds — and an exchange whose outbound half is on
	// disk with no inbound half reads as one that never came back.
	ref := s.recordResponse(resp)
	s.countUpstream(resp.StatusCode)

	session := sessionFromResponse(resp)
	known := s.vault.Load(session)
	conversation := conversationFromResponse(resp)
	if !isTextual(resp.Header.Get("Content-Type")) {
		return nil
	}
	// Every textual answer goes through, whatever the session minted and whoever is
	// watching. An answer says three things, and only the first depends on the
	// mapping: what has to be put back, what the exchange cost, and what tool the
	// model asked to run.
	//
	// This used to return early on an empty mapping, so an exchange that replaced
	// nothing was never read for its token counts and never reached the heartbeat.
	// Gating it on `-a` instead would have been worse: two agents on identical
	// traffic would report different totals depending on whether a console was
	// attached, and a trace's `unmask` duration would appear and vanish with the
	// flag. What the flags reveal must never change what the agent does.

	if isEventStream(resp.Header.Get("Content-Type")) {
		stream := newStreamRehydrator(resp.Body, known, s.usageSink(ref, conversation), s.audit.unmaskedSeen())
		stream.onTool = s.toolSink(conversation)
		stream.onDegraded = s.recorder.Degraded
		// TODO: a session with no mapping is never expanded — streamRehydrator.observe
		// here, the len(known) guards in unmaskBuffered — so a name in its answer goes
		// unreported. Reachable only when every name in the history is one the model
		// made up; the upgrade is a token scan in observe and in unmaskBuffered.
		stream.expander.Unknown = s.unknownName(session)
		if ref != nil {
			stream.onExpanded = func(spent time.Duration) { ref.expanding = spent }
		}
		resp.Body = stream
		// The rewritten stream is not the length the provider declared: a
		// gateway that puts a Content-Length on text/event-stream had the
		// restored body cut at the original length, or the client waiting for
		// bytes that never came. Dropped, as the buffered path below replaces it.
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
		return nil
	}

	return s.unmaskBuffered(resp, ref, conversation, known, session)
}

// unmaskBuffered is unmask for an answer that arrives whole: the same expansion over
// a body read in one piece.
func (s *Server) unmaskBuffered(resp *http.Response, ref *traceRef, conversation string,
	known map[string]string, session string) error {
	// Closed at the end rather than straight after the read, because closing is
	// what files the trace: closed here, the answer would go to disk above a set of
	// figures none of which had been measured yet. The original is held rather than
	// resp.Body, which is replaced below — a deferred close of the field would shut
	// the reader handed to the caller instead of the one from the provider.
	upstream := resp.Body
	defer func() { _ = upstream.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read the response body: %w", err)
	}

	// Structurally, for the same reason the request path is: an original can
	// contain a quote or a newline, and splicing one into raw JSON produces a
	// document the caller cannot parse. Putting it into a decoded string and
	// letting the encoder escape it is the only way that is always correct.
	expander := detector.NewExpander(known)
	expander.Unknown = s.unknownName(session)
	expand := func(text string) string {
		return expander.Unmask(text, s.audit.unmaskedSeen())
	}

	expanded := time.Now()
	out := string(body)
	if doc, err := decodeJSONBody(body); err == nil {
		// The same decode answers both questions, so the body is parsed once:
		// what the tokens cost, and what has to be put back into it.
		if model, usage := usageFrom(doc); model != "" {
			s.usageSink(ref, conversation)(model, usage)
		}
		// The buffered half of the tool-call console and the heartbeat's tool
		// counts — see reportToolCalls, and why it runs before the pass below.
		reportToolCalls(doc, known, s.audit.unmaskedSeen(), s.toolSink(conversation))
		// Expanded and re-encoded only when there is something to put back. The
		// round trip is not byte-preserving — a lone surrogate becomes U+FFFD,
		// pretty-printing is compacted — and an answer the agent has no reason to
		// touch must reach the caller as the provider sent it. The decode above
		// still happens for every answer, because the counts and the tool calls do
		// not depend on the mapping; only the rewrite does.
		if len(known) > 0 {
			if encoded, err := encodeJSONBody(mapStrings(doc, expand)); err == nil {
				out = string(encoded)
			} else {
				out = expand(string(body))
			}
		}
	} else if len(known) > 0 {
		out = expand(string(body))
	}
	if ref != nil {
		ref.expanding = time.Since(expanded)
	}

	resp.Body = io.NopCloser(strings.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}

// warnUnread says that a request carries a picture or a document. It goes out as it
// arrived — the catalogue reads text — so whatever is written in it reaches the model
// in clear. Said when the count changes: a conversation sends its history again each
// turn, and the same pictures are not news the second time.
func (s *Server) warnUnread(session string, doc any) {
	if n := unreadPayloads(doc); n > 0 && s.said.first(fmt.Sprintf("%s\x00unread\x00%d", session, n)) {
		s.log.Warn("request carries what the agent cannot read", "session", session, "unread", n)
	}
}

// unknownName says that an answer carries a name nothing in the session stands for
// — one the model wrote itself, in the shape it saw, for a value that reached it in
// clear. The caller reads it as a placeholder left in, and a tool runs it as written.
// The name only: it is this agent's vocabulary, never a value.
func (s *Server) unknownName(session string) func(string) {
	return func(token string) {
		// Once per session: the model quotes a name it was given as readily as it
		// invents one, and a conversation says it again every turn.
		if !s.said.first(session + "\x00" + token) {
			return
		}
		s.log.Warn("answer carries a name the agent never gave", "session", session, "name", token)
	}
}

// recordResponse wraps the body so the provider's answer is filed with the trace
// this exchange already wrote, and does nothing at all when nothing is recording.
//
// Nothing rather than a wrapper that discards: the ordinary agent must not pay a
// copy of every answer it forwards to arrive at throwing it away.
func (s *Server) recordResponse(resp *http.Response) *traceRef {
	ref := traceFromResponse(resp)
	if ref == nil || resp.Body == nil {
		return nil
	}

	// Stamped here because here is where the provider's headers have arrived, and
	// on a stream that is a long way before its body has.
	ref.upstream = time.Since(ref.sent)
	ref.answering = time.Now()

	resp.Body = newResponseRecorder(resp.Body, func(raw string, ended time.Time) {
		ref.delivering = ended.Sub(ref.answering)
		if err := s.audit.response(ref, raw); err != nil {
			// Said and carried on, as the outbound half is: the thing that records
			// the control must never be able to take the control down.
			s.log.Error("the answer could not be traced", "error", err)
		}
	})
	return ref
}

// The events the heartbeat's recorder and the day's tally both count, each reported to
// both in one call.
//
// They were written side by side at every call site with nothing holding a pair
// together, and a path that fed one and forgot the other leaves the fleet view and the
// settings page disagreeing about the same traffic
// (TestTheDayAndTheHeartbeatCountTheSameTraffic). The tool calls go through toolSink for
// the same reason; what only the recorder counts — Usage, Policy, Degraded — is called
// directly.

func (s *Server) countRequest(conversation, client, provider string) {
	s.recorder.Request(conversation, client, provider)
	s.today.request(time.Now(), client)
}

func (s *Server) countRefused() {
	s.recorder.Refused()
	s.today.refused(time.Now())
}

// countMasked counts a pass two ways on purpose: the heartbeat every replacement, the
// day what was minted — see tally.
func (s *Server) countMasked(conversation string, pass *detector.Pass) {
	s.recorder.Masked(conversation, pass.Counts())
	s.today.add(time.Now(), pass.MintedCounts())
}

func (s *Server) countUpstream(status int) {
	s.recorder.Upstream(status)
	s.today.upstream(time.Now(), status)
}

// usageSink is where a response reports what it cost.
//
// Always the recorder, which is what the heartbeat is built from; also the trace,
// when there is one. Handed over as one function so neither caller has to remember
// both.
func (s *Server) usageSink(ref *traceRef, conversation string) func(string, pkgtelemetry.TokenUsage) {
	return func(model string, usage pkgtelemetry.TokenUsage) {
		if ref != nil {
			ref.model, ref.usage = model, usage
		}
		s.recorder.Usage(conversation, model, usage)
	}
}

// toolSink is where a response reports the tool calls in it: always the recorder,
// which counts every one, and the console when there is one.
//
// The console used to be the only consumer, and the callback was nil without it so
// that an agent not auditing paid nothing per tool call. The heartbeat now wants
// each call, so every agent pays the reduction in Recorder.Tool — a JSON decode of
// a shell command's arguments and a split on its operators, on the response path,
// once per tool call.
func (s *Server) toolSink(conversation string) func(name, arguments string, restored bool) {
	return func(name, arguments string, restored bool) {
		s.recorder.Tool(conversation, telemetry.ToolCall{Name: name, Arguments: arguments, Restored: restored})
		s.today.tool(time.Now(), restored)
		if s.audit.writes() {
			s.audit.tool(name, arguments)
		}
	}
}

// isTextual reports whether a content type is one the expander should read at
// all. Anything else — an image, an audio stream — is passed through untouched.
func isTextual(contentType string) bool {
	if contentType == "" {
		return true // no type declared: JSON, in practice
	}
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return strings.HasPrefix(media, "text/") ||
		media == "application/json" ||
		strings.HasSuffix(media, "+json")
}

func isEventStream(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	return err == nil && media == "text/event-stream"
}

// sessionOf reads which conversation a request belongs to.
//
// A session is what scopes the mapping: two conversations must not be able to
// read each other's values, and one conversation has to keep its own across
// turns. Falling back to a single name is right for one person at one
// workstation, which is what this agent is — and it is why the agent must not be
// exposed beyond the loopback interface.
func sessionOf(r *http.Request) string {
	for _, header := range []string{"X-Session-Id", "X-Request-Id"} {
		if v := strings.TrimSpace(r.Header.Get(header)); v != "" {
			return v
		}
	}
	return "default"
}

// traceRef is what the response half needs to find the file the request half
// wrote: where it is, whose answer is being recorded into it, and what the
// exchange cost on the way.
//
// The figures are filled in at four different moments and read once, when the
// answer's body closes. Plain fields and no lock: everything after ModifyResponse
// happens on the goroutine the reverse proxy copies the body on, which is the same
// assumption the stream rehydrator's own counters already make.
type traceRef struct {
	path     string
	provider string

	// masking is the detector's scan of the outbound body, and nothing else — not
	// the vault write and not the trace write, which would make the figure say
	// "how long -v costs" rather than "how long masking costs".
	masking time.Duration

	// sent is when the request left, and upstream how long the provider took to
	// answer with its headers. Separate from delivering, because on a stream those
	// are different questions: the first is the provider's latency, the second is
	// how long the answer took to arrive, and adding them together hides both.
	sent       time.Time
	upstream   time.Duration
	answering  time.Time
	delivering time.Duration

	// expanding is the restoration, summed. On a stream it is the sum of the
	// rewrites rather than the wall clock, which is almost all waiting.
	expanding time.Duration

	model string
	usage pkgtelemetry.TokenUsage
}

type traceKey struct{}

// withTrace carries the exchange's trace to the response path.
//
// Nothing is added when nothing is recording, so an agent without -v puts no value
// in the context of every request it forwards.
func withTrace(ctx context.Context, ref *traceRef) context.Context {
	if ref == nil {
		return ctx
	}
	return context.WithValue(ctx, traceKey{}, ref)
}

// traceFromResponse recovers the exchange's trace, or nil for an untraced one.
func traceFromResponse(resp *http.Response) *traceRef {
	if resp.Request == nil {
		return nil
	}
	ref, _ := resp.Request.Context().Value(traceKey{}).(*traceRef)
	return ref
}

type sessionKey struct{}

func withSession(ctx context.Context, session string) context.Context {
	return context.WithValue(ctx, sessionKey{}, session)
}

// sessionFromResponse recovers the session on the response path, from the
// context the request carried upstream.
func sessionFromResponse(resp *http.Response) string {
	if resp.Request == nil {
		return "default"
	}
	if session, ok := resp.Request.Context().Value(sessionKey{}).(string); ok {
		return session
	}
	return "default"
}

type conversationKey struct{}

func withConversation(ctx context.Context, conversation string) context.Context {
	return context.WithValue(ctx, conversationKey{}, conversation)
}

// conversationFromResponse recovers the conversation the heartbeat counts an
// exchange under, on the response path. Falls back to the session, which is what
// the conversation is whenever nothing narrower was read.
func conversationFromResponse(resp *http.Response) string {
	if resp.Request == nil {
		return "default"
	}
	if conversation, ok := resp.Request.Context().Value(conversationKey{}).(string); ok {
		return conversation
	}
	return sessionFromResponse(resp)
}
