package proxy

import (
	"net/url"
	"runtime"

	"github.com/neverseen-ai/neverseen-agent/internal/detector"
	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
	contract "github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// What the agent says about itself to a supervision backend.
//
// It is the request path's half of pkg/telemetry: the contract type is public and
// the backend imports it, and this is where a running server fills one in. Nothing
// here reaches the request path in the other direction — an agent with no backend
// builds no reporter at all, so there is no code path to get wrong rather than one
// that quietly does nothing.

// State is what this agent reports about itself.
//
// Read from the running server rather than from the configuration it was built
// with, because the question a security officer is asking is not "what was it
// told to do" but "what is it doing" — and an agent running with no locale
// selected masks almost nothing while looking perfectly healthy.
func (s *Server) State() contract.State {
	state := detectorState(s.det)
	state.StartedAt = s.startedAt
	state.Providers = providerCodes(s.providers)

	// Read at each heartbeat rather than cached at start-up: a laptop moves
	// between networks and a cached address would name where the machine was
	// when it booted. The name is read with it because a machine can be renamed
	// while the agent runs, and one that reported the old name until somebody
	// restarted it would be a row nobody could match to the workstation in front
	// of them.
	state.Addresses = localAddresses()
	state.Hostname = hostname()

	// How the agent itself is exposed — the console printing values in clear,
	// the traces on disk, an address beyond loopback, a route pointed at a
	// party that is not the vendor. Fixed for the life of the process, all
	// four, but read here with the rest so one function says what the agent is.
	state.Console = s.audit.writes()
	state.Tracing = s.audit.traceDir() != ""
	state.Exposed = s.exposed
	state.Rerouted = s.rerouted()
	return state
}

// detectorState is the half of State that is the detector's: the build, and what
// it is masking. Shared with the replay, which has a detector and no server.
//
// Read at each heartbeat rather than cached at start-up, because the policy
// changes while the process runs, which nothing else in State does. Cached at
// start-up, a supervision backend would show every agent as applying its whole
// catalogue no matter what anybody switched off — and Masking is the one field
// whose whole purpose is to say otherwise.
func detectorState(det *detector.Detector) contract.State {
	return contract.State{
		Version:      Version,
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		Locales:      det.Locales(),
		Substitution: det.Substitution().String(),
		Masking:      det.Masking().String(),
		SwitchedOff:  switchedOffCodes(det.Disabled()),
		SecretLevel:  det.SecretLevel().String(),
		Allowlisted:  det.Allowlisted(),
	}
}

// rerouted names the provider codes whose route does not go to the vendor's own
// host: a default code pointed elsewhere by NEVERSEEN_PROVIDERS, or a code the
// agent does not know by default at all. Nil when every route is where the
// catalogue says it is, so the field stays absent rather than empty.
func (s *Server) rerouted() []string {
	var out []string
	for _, p := range s.providers {
		if s.hosts[p.Code] != defaultHost(p.Code) {
			out = append(out, p.Code)
		}
	}
	return out
}

// defaultHost is where the agent sends a provider's code without configuration,
// or "" for a code it does not know.
func defaultHost(code string) string {
	for _, p := range DefaultProviders {
		if p.Code == code {
			base, err := url.Parse(p.BaseURL)
			if err != nil {
				return ""
			}
			return base.Hostname()
		}
	}
	return ""
}

// switchedOffCodes is the disabled set as the contract carries it.
//
// Category names, which the counters already key on, so this adds no new kind of
// string to the heartbeat — and nil for an agent with nothing switched off, so the
// field stays absent rather than shipping an empty array to every backend on every
// report.
func switchedOffCodes(cats []pii.Category) []string {
	if len(cats) == 0 {
		return nil
	}
	out := make([]string, 0, len(cats))
	for _, cat := range cats {
		out = append(out, string(cat))
	}
	return out
}
