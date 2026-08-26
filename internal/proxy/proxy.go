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
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/internal/telemetry"
	"github.com/cloakfleet/cloakfleet/internal/vault"
)

// Config is what the proxy needs beyond a detector and a vault.
type Config struct {
	// Providers are the upstreams, addressed by the first path segment. Empty
	// means DefaultProviders.
	Providers []Provider

	// Logger receives one line per exchange, carrying counts and never content.
	// Nil discards them.
	Logger *slog.Logger

	// Recorder accumulates what a supervised agent reports. Nil means one is
	// created anyway: counting costs a mutex and the request path then has one
	// shape rather than two, with no branch that only runs where nobody looked.
	Recorder *telemetry.Recorder
}

// Server is the agent's HTTP front.
type Server struct {
	det      *detector.Detector
	vault    *vault.Vault
	log      *slog.Logger
	recorder *telemetry.Recorder

	providers []Provider
	routes    map[string]*httputil.ReverseProxy

	// startedAt is when this process began serving, so a supervision backend can
	// show uptime and spot an agent restarting in a loop.
	startedAt time.Time
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
		det:       det,
		vault:     v,
		log:       logger,
		recorder:  recorder,
		providers: providers,
		routes:    make(map[string]*httputil.ReverseProxy, len(providers)),
		startedAt: time.Now(),
	}

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
		ModifyResponse: s.unmask,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.log.Error("upstream failed", "path", r.URL.Path, "error", err)
			http.Error(w, "cloakfleet: the provider could not be reached", http.StatusBadGateway)
		},
	}
}

// reservedRoutes are the paths the agent answers itself. A provider may not take
// one of these codes: "/healthz" would reach the agent while "/healthz/v1/…"
// reached the provider, which is a routing table nobody could reason about.
var reservedRoutes = []string{"healthz", "test"}

// Handler returns the agent's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/test", s.handleTest)
	mux.HandleFunc("/", s.forward)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// Marshalled from the type a local caller decodes, in this same package, so
	// the route cannot grow a field on one side only.
	_ = json.NewEncoder(w).Encode(Health{
		Status:       "ok",
		Version:      Version,
		Locales:      s.det.Locales(),
		Substitution: s.det.Substitution().String(),
		Providers:    s.Providers(),
	})
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
			"cloakfleet: no provider named %q. Point your client at /<provider>, one of: %s",
			code, strings.Join(providerCodes(s.providers), ", ")), http.StatusNotFound)
		return
	}

	session := sessionOf(r)
	if err := s.maskRequest(session, r); err != nil {
		// Fail closed. Forwarding a body this could not read is exactly the
		// leak the agent exists to prevent, so an unreadable body is an error
		// rather than a pass-through.
		s.log.Error("masking failed, refusing to forward", "session", session, "error", err)
		http.Error(w, "cloakfleet: the request body could not be masked", http.StatusUnsupportedMediaType)
		return
	}

	s.recorder.Request()

	r.URL.Path = rest
	r.Host = ""
	route.ServeHTTP(w, r.WithContext(withSession(r.Context(), session)))
}

// maskRequest replaces the sensitive values in the body and records what it
// replaced, in the session's vault, before the request goes anywhere.
func (s *Server) maskRequest(session string, r *http.Request) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}

	body, err := readBody(r)
	if err != nil {
		return err
	}

	pass := s.det.NewPass(s.vault.Load(session))

	replaced := 0
	mask := func(text string) string {
		out, n := s.det.Mask(text, pass)
		replaced += n
		return out
	}

	// A JSON document is masked value by value; anything else as flat text. One
	// pass over the whole body either way, so a value repeated in two fields
	// keeps one identity.
	masked := ""
	if out, ok := mapJSONStrings(body, mask); ok {
		masked = string(out)
	} else {
		masked = mask(string(body))
	}

	if err := s.vault.Save(session, pass.Minted()); err != nil {
		// The mapping is what makes the answer readable again. Masking without
		// storing would send the provider tokens the response path can never
		// expand, so the caller would read the agent's bookkeeping instead of
		// its own data.
		return fmt.Errorf("store the session mapping: %w", err)
	}

	r.Body = io.NopCloser(strings.NewReader(masked))
	r.ContentLength = int64(len(masked))
	r.Header.Set("Content-Length", strconv.Itoa(len(masked)))
	r.Header.Del("Content-Encoding") // readBody has decompressed it

	if replaced > 0 {
		s.recorder.Masked(pass.Counts())

		// Counts, never content: the log is the one place a masked value could
		// come back into the clear by accident.
		s.log.Info("request masked", "session", session, "values", replaced, "minted", len(pass.Minted()))
	}
	return nil
}

// readBody returns the request body as text, decompressing it when it arrived
// compressed.
//
// An encoding it cannot read is an error, not a pass-through. The alternative —
// forwarding a body the agent could not inspect — is the one failure mode a data
// loss prevention tool must never have.
func readBody(r *http.Request) ([]byte, error) {
	switch encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); encoding {
	case "", "identity":
		return io.ReadAll(r.Body)
	case "gzip":
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read a gzipped body: %w", err)
		}
		defer func() { _ = zr.Close() }()
		return io.ReadAll(zr)
	default:
		return nil, fmt.Errorf("cannot inspect a body encoded as %q", encoding)
	}
}

// unmask puts the originals back into the response.
//
// Streaming and buffered responses go through the same expansion, so a value
// restored in one is restored in the other. They differ only in how much text
// the expander can see at a time, which is what the streaming path's held-back
// tail is for.
func (s *Server) unmask(resp *http.Response) error {
	known := s.vault.Load(sessionFromResponse(resp))
	if len(known) == 0 || !isTextual(resp.Header.Get("Content-Type")) {
		return nil
	}

	if isEventStream(resp.Header.Get("Content-Type")) {
		resp.Body = newStreamRehydrator(resp.Body, known, s.recorder.Usage)
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("read the response body: %w", err)
	}

	// Structurally, for the same reason the request path is: an original can
	// contain a quote or a newline, and splicing one into raw JSON produces a
	// document the caller cannot parse. Putting it into a decoded string and
	// letting the encoder escape it is the only way that is always correct.
	expand := func(text string) string { return detector.Unmask(text, known) }

	out := ""
	if doc, err := decodeJSONBody(body); err == nil {
		// The same decode answers both questions, so the body is parsed once:
		// what the tokens cost, and what has to be put back into it.
		if model, usage := usageFrom(doc); model != "" {
			s.recorder.Usage(model, usage)
		}
		if encoded, err := encodeJSONBody(mapStrings(doc, expand)); err == nil {
			out = string(encoded)
		} else {
			out = expand(string(body))
		}
	} else {
		out = expand(string(body))
	}

	resp.Body = io.NopCloser(strings.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
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
