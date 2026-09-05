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

	// SecretLevel is the weakest named secret being masked: "weak", "medium" or
	// "strong". A string rather than the Strength, for the reason Masking is one: a
	// number would have the two sides agreeing about 2 and disagreeing about what 2
	// means.
	SecretLevel string   `json:"secret_level"`
	Providers   []string `json:"providers"`

	// Masking is how much of the catalogue is being applied: "full", "partial" or
	// "none". A string rather than the Level, because this crosses a wire and a
	// number would have the two sides agreeing about 1 while disagreeing about
	// what 1 means.
	Masking string `json:"masking"`

	// AvailableLocales are the country pattern sets this build has, in load order,
	// whether or not they are loaded.
	//
	// Served rather than read from pkg/pii by the caller, for the reason Groups is:
	// a menu built from its own copy would offer a locale an older agent does not
	// have, and the click would fail with "no locale" from a name the menu itself
	// suggested. Load order because it is a decision — it settles which country
	// claims a value both could read — and a list somebody ticks in a different
	// order than the agent applies them in is a list that lies about the result.
	AvailableLocales []string `json:"available_locales,omitempty"`

	// Groups is the catalogue as a list somebody can be shown: every group in
	// display order with its categories, each carrying whether it is switched off
	// and whether it may be.
	//
	// Served rather than read from pkg/pii by the caller, even though the menu bar
	// could import the catalogue directly. The agent is the one applying it: a
	// menu built from its own copy would go on offering a category after a rebuilt
	// agent stopped having it, and the picture would disagree with the traffic.
	Groups []HealthGroup `json:"groups,omitempty"`

	// Off is the whole switched-off set as codes — the intent, including categories
	// no loaded locale can emit — and it is what a surface sends back.
	//
	// Groups carries only what the detector can find, on purpose (see catalogue),
	// so a surface that rebuilt the set from Groups sent back the reachable half:
	// with SSN off and `us` unloaded, the next click on anything at all put
	// `off: []` and the stored file made the loss permanent — loading `us` later
	// found SSN on, which is the regression the stored intent exists to prevent.
	// Displayed from Groups, resent from here.
	Off []string `json:"off,omitempty"`
}

// HealthGroup is one family of categories, as a surface needs to draw it.
type HealthGroup struct {
	Code       string           `json:"code"`
	Label      string           `json:"label"`
	Categories []HealthCategory `json:"categories"`
}

// HealthCategory is one switch.
type HealthCategory struct {
	Code  string `json:"code"`
	Label string `json:"label"`

	// Off is whether this category is currently not being masked.
	Off bool `json:"off,omitempty"`

	// Locked is whether it may be switched off at all. Sent rather than derived
	// from the code's "SECRET_" prefix, because that prefix is a naming convention
	// and this is a rule — and a surface guessing at it would draw a switch the
	// agent refuses to honour.
	Locked bool `json:"locked,omitempty"`
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

// Masking reports whether the agent is doing the job it exists for at all.
//
// Answering is not the question, which is why this is a separate one. An agent
// with no locale selected is up, healthy, and recognises almost nothing — the
// state a green light would call fine while the traffic went out in clear. The
// same distinction is why the supervision contract reports State rather than a
// heartbeat alone.
func (s Status) Masking() bool { return s.Answering && len(s.Locales) > 0 }

// Level reports how much of the catalogue is being applied, which is a different
// question from Masking and the one an icon and an exit code have to follow.
//
// Three answers because two were not enough once a category could be switched
// off: such an agent is masking, so Masking is true, and reporting that alone
// would be the green light over the values that are not being replaced. It is the
// answering/masking distinction one level further in.
//
// Read from what the agent said rather than recomputed from Groups: the agent is
// the one applying the catalogue, and a caller deciding for itself is a second
// answer to the question this route exists to answer.
func (s Status) Level() detector.Level {
	if !s.Masking() {
		return detector.LevelNone
	}
	switch s.Health.Masking {
	case detector.LevelPartial.String():
		return detector.LevelPartial
	case detector.LevelFull.String():
		return detector.LevelFull
	default:
		// An agent that answered without the field: an older build, or a body that
		// parsed only partly. Derived from what it did carry rather than assumed —
		// a build with no policy route cannot have a category switched off, so
		// "full" is a fact about that build rather than an invention, and a body
		// that carried groups is read from those.
		if len(s.SwitchedOff()) > 0 {
			return detector.LevelPartial
		}
		return detector.LevelFull
	}
}

// SwitchedOff names the categories the agent is not masking, in words, in the order
// the catalogue lists them. For a person reading a report.
func (s Status) SwitchedOff() []string { return s.switchedOff(false) }

// SwitchedOffCodes is the set as the codes a request carries — the whole intent,
// unreachable categories included. For anything that has to send the set back.
//
// Two methods rather than one returning both, because the two are for different
// readers and a caller that mixed them would print "EMAIL" at somebody or send
// "Email address" to the agent — and the second fails with "no category named",
// which reads as a bug in the agent. And two sources: the labels come from Groups,
// which lists what the agent can find; the codes from Off, which is what it
// remembers. Derived from Groups only for an agent that did not send Off — an
// older build, whose set could not hold more than Groups shows.
func (s Status) SwitchedOffCodes() []string {
	if s.Off != nil {
		return s.Off
	}
	return s.switchedOff(true)
}

func (s Status) switchedOff(codes bool) []string {
	var out []string
	for _, g := range s.Groups {
		for _, c := range g.Categories {
			if !c.Off {
				continue
			}
			if codes {
				out = append(out, c.Code)
			} else {
				out = append(out, c.Label)
			}
		}
	}
	return out
}

// healthMaxBytes bounds what Query reads from /healthz.
//
// The payload is the whole catalogue listed by group, so it grows every time a
// category is added — roughly eighty bytes each. Sixty-four kibibytes is eight
// times what the catalogue needs today, which is headroom for the next batch
// rather than a number that has to be revisited with each one, and it still
// bounds a route that is called on every new shell.
const healthMaxBytes = 64 * 1024

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
	//
	// healthMaxBytes, not a literal, because the payload carries the whole
	// catalogue by group and so grows with it. At 8 KiB it already did not fit:
	// the second tier of vendor prefixes took the body past the cap, the read
	// truncated, the parse below failed and every field stayed empty — so
	// `cloakfleet status` printed nothing, the menu bar drew nothing and the exit
	// code said something was wrong, over an agent that was answering perfectly.
	// TestHealthPayloadFitsTheQueryBound is what makes the next overrun loud.
	body, err := io.ReadAll(io.LimitReader(resp.Body, healthMaxBytes))
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

// Headline is the one sentence that says what this agent is doing, for whichever
// surface is about to report on it.
//
// One sentence rather than one per surface, because the two that print it were
// written apart and had already drifted: `cloakfleet status` called a category
// "switched off" where `cloakfleet mask` called the same category "in clear", so
// the two commands describing the same agent disagreed about what had happened to
// it. Which is the failure the single /healthz type prevents on the wire, arrived
// at through prose instead.
//
// "In clear" is what survived. It says the consequence rather than the mechanism —
// somebody reading "switched off" learns that a setting moved, and somebody reading
// "in clear" learns that their data is leaving — and it is already the word the
// per-category lines below use.
//
// The menu bar keeps its own wording, and deliberately: it has a tooltip's worth of
// room and no address to name, so it says "Masking, with 2 categories in clear"
// where these say a sentence. What the three share is the decision, and that is
// Level, which all three already read.
func (s Status) Headline() string {
	off := s.SwitchedOff()

	switch {
	case !s.Answering:
		return fmt.Sprintf("cloakfleet is not answering on %s.", s.Addr)
	case len(s.Locales) == 0:
		return fmt.Sprintf("cloakfleet is answering on %s but masking almost nothing.", s.Addr)
	case s.Level() == detector.LevelPartial:
		return fmt.Sprintf("cloakfleet is masking on %s, with %d categor%s in clear.",
			s.Addr, len(off), plural(len(off), "y", "ies"))
	default:
		return fmt.Sprintf("cloakfleet is masking on %s. Every category its locales loaded is on.",
			s.Addr)
	}
}

// Write reports the status in words an operator can act on.
//
// It says what is being masked rather than that the agent is up, and it says what
// the consequence is when it is not — a stopped agent means unmasked traffic, not
// a broken workstation, and somebody reading this has to know which.
func (s Status) Write(w io.Writer) {
	fmt.Fprintf(w, "%s\n\n", s.Headline())

	switch {
	case !s.Answering:
		fmt.Fprintf(w, "Your tools are reaching their provider directly, unmasked — which is\n")
		fmt.Fprintf(w, "deliberate: a stopped agent leaves them working rather than broken.\n")
		fmt.Fprintf(w, "Start it with `cloakfleet proxy`, or `./install.sh --restart`.\n")
		return

	case len(s.Locales) == 0:
		fmt.Fprintf(w, "No country pattern set is loaded, so only the locale-independent\n")
		fmt.Fprintf(w, "identifiers and credentials are recognised. Set %s.\n\n", detector.EnvLocale)

	case s.Level() == detector.LevelPartial:
		// Named, not counted. "Two categories are off" sends somebody looking; the
		// names are what tells them whether the one they care about is among them.
		for _, name := range s.SwitchedOff() {
			fmt.Fprintf(w, "  in clear       %s\n", name)
		}
		fmt.Fprint(w, "\n")
	}

	fmt.Fprintf(w, "  version        %s\n", or(s.Version, "unknown"))
	fmt.Fprintf(w, "  locales        %s\n", or(strings.Join(s.Locales, ", "), "none"))
	fmt.Fprintf(w, "  substitution   %s\n", or(s.Substitution, "unknown"))
	fmt.Fprintf(w, "  providers      %s\n", or(strings.Join(s.Providers, ", "), "none"))
	fmt.Fprintf(w, "\nhttp://%s/test shows what would be masked, in this configuration.\n", s.Addr)
}

// plural is the one-or-many ending of a word. The menu bar carries its own copy,
// which is the right amount of duplication for five lines: a shared package for it
// would be a dependency between the request path and the menu bar, which are
// deliberately kept from importing each other's concerns. The command had a third
// copy until the sentence that used it moved into Headline.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func or(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
