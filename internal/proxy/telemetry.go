package proxy

import (
	"runtime"

	"github.com/cloakfleet/cloakfleet/pkg/pii"
	contract "github.com/cloakfleet/cloakfleet/pkg/telemetry"
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
	return contract.State{
		Version:      Version,
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
		StartedAt:    s.startedAt,
		Locales:      s.det.Locales(),
		Substitution: s.det.Substitution().String(),
		Providers:    providerCodes(s.providers),

		// Read at each heartbeat rather than cached at start-up: a laptop moves
		// between networks and a cached address would name where the machine was
		// when it booted.
		Addresses: localAddresses(),

		// Read at each heartbeat for a stronger version of the same reason: this
		// changes while the process runs, which nothing else in State does. Cached
		// at start-up, a supervision backend would show every agent as applying its
		// whole catalogue no matter what anybody switched off — and it is the one
		// field here whose whole purpose is to say otherwise.
		Masking:     s.det.Masking().String(),
		SwitchedOff: switchedOffCodes(s.det.Disabled()),
		SecretLevel: s.det.SecretLevel().String(),
	}
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
