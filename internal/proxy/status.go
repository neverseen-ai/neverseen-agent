package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/detector"
)

// Health is what the agent answers about itself on /healthz.
//
// One type for both sides of that route: the server marshals it and every local
// caller — `cloakfleet status`, and the menu bar item that will read the same
// endpoint — unmarshals it, in this same package, so the two cannot come to
// disagree about a field name. That is the drift with no symptom otherwise, the
// one the shared golden exists to catch on the backend contract.
//
// It carries what the agent is *applying* and nothing about who is using it: no
// counters, no addresses, no backend URL. The route is on the loopback interface
// and unauthenticated, and anything richer would be a local oracle for what a
// person has been doing.
type Health struct {
	Status       string   `json:"status"`
	Version      string   `json:"version"`
	Locales      []string `json:"locales"`
	Substitution string   `json:"substitution"`
	Providers    []string `json:"providers"`
}

// Status is what a local caller can learn about the agent, including the case
// where there is nothing there to ask.
type Status struct {
	// Addr is where the answer was looked for, so a report can name it.
	Addr string

	// Answering is whether anything replied.
	Answering bool

	Health
}

// Masking reports whether the agent is doing the job it exists for.
//
// Answering is not the question, which is why this is a separate one. An agent
// with no locale selected is up, healthy, and recognises almost nothing — the
// state a green light would call fine while the traffic went out in clear. The
// same distinction is why the supervision contract reports State rather than a
// heartbeat alone.
func (s Status) Masking() bool { return s.Answering && len(s.Locales) > 0 }

// Query asks the agent about itself.
//
// It returns no error, because "nothing is listening" is an answer to the question
// and not a failure to answer it — every caller here wants to report that state,
// not to handle it.
func Query(ctx context.Context, addr string, timeout time.Duration) Status {
	if addr == "" {
		addr = DefaultListen
	}
	status := Status{Addr: addr}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return status
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return status
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded, and read to the end either way: this runs on every new shell when
	// `cloakfleet env` is wired into a profile, and a connection left half open on
	// a machine that opens a shell every few seconds is a socket nobody reclaims.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if err != nil || resp.StatusCode != http.StatusOK {
		return status
	}

	status.Answering = true
	// A body that will not parse still means something answered. Reporting "not
	// answering" there would send somebody to restart a service that is running,
	// and the fields simply stay empty.
	_ = json.Unmarshal(body, &status.Health)
	return status
}

// Write reports the status in words an operator can act on.
//
// It says what is being masked rather than that the agent is up, and it says what
// the consequence is when it is not — a stopped agent means unmasked traffic, not
// a broken workstation, and somebody reading this has to know which.
func (s Status) Write(w io.Writer) {
	switch {
	case !s.Answering:
		fmt.Fprintf(w, "cloakfleet is not answering on %s.\n\n", s.Addr)
		fmt.Fprintf(w, "Your tools are reaching their provider directly, unmasked — which is\n")
		fmt.Fprintf(w, "deliberate: a stopped agent leaves them working rather than broken.\n")
		fmt.Fprintf(w, "Start it with `cloakfleet proxy`, or `./install.sh --restart`.\n")
		return

	case len(s.Locales) == 0:
		fmt.Fprintf(w, "cloakfleet is answering on %s but masking almost nothing.\n\n", s.Addr)
		fmt.Fprintf(w, "No country pattern set is loaded, so only the locale-independent\n")
		fmt.Fprintf(w, "identifiers and credentials are recognised. Set %s.\n\n", detector.EnvLocale)

	default:
		fmt.Fprintf(w, "cloakfleet is masking on %s.\n\n", s.Addr)
	}

	fmt.Fprintf(w, "  version        %s\n", or(s.Version, "unknown"))
	fmt.Fprintf(w, "  locales        %s\n", or(strings.Join(s.Locales, ", "), "none"))
	fmt.Fprintf(w, "  substitution   %s\n", or(s.Substitution, "unknown"))
	fmt.Fprintf(w, "  providers      %s\n", or(strings.Join(s.Providers, ", "), "none"))
	fmt.Fprintf(w, "\nhttp://%s/test shows what would be masked, in this configuration.\n", s.Addr)
}

func or(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
