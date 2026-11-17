package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/neverseen-ai/neverseen-agent/internal/telemetry"
)

// rtkReader is how the agent reads rtk for the settings page: where the binary is,
// and what `rtk gain` printed. The zero value reads nothing, which is what a server
// built by a test has — the rtk on the machine running it must not reach an
// assertion, the reason the reporter's Gain is wired in one place too.
type rtkReader struct {
	path func() string
	gain func(ctx context.Context, path string) ([]byte, error)
}

// installed reports whether a binary called rtk was found. A lookup, never a run:
// it is asked at every GET /today.
func (r rtkReader) installed() bool { return r.path != nil && r.path() != "" }

// rtkReply is what GET /rtk answers.
type rtkReply struct {
	// Installed is whether a binary called rtk was found. False, nothing else is set.
	Installed bool `json:"installed"`

	// Path is that binary.
	Path string `json:"path,omitempty"`

	// ReadAt is when rtk was asked.
	ReadAt *time.Time `json:"read_at,omitempty"`

	// Problem is why rtk gave no reading, in its own words: the Rust Type Kit
	// shares the name, and a history that cannot be opened is rtk's to explain.
	// Days is then empty rather than an earlier reading.
	Problem string `json:"problem,omitempty"`

	// Days is rtk's record, one row a UTC day, as far back as rtk keeps it.
	Days []rtkDayReply `json:"days,omitempty"`
}

// rtkDayReply is one day: the agent's own shape rather than rtk's bytes relayed, so
// the page reads three counts and nothing rtk may add to a row later. The saving is
// Input minus Output, as in the heartbeat, and no price is derived from it.
type rtkDayReply struct {
	Date     string `json:"date"`
	Commands int    `json:"commands"`
	Input    int    `json:"input"`
	Output   int    `json:"output"`
}

// handleRTK serves what rtk recorded on this workstation, day by day, for the
// settings page's rtk screen.
//
// Behind the control key, like the day's count: how many commands somebody's coding
// agents ran, and on which days, is theirs. Read when asked rather than kept: the
// page asks when the screen is opened, and the reporter's own readings are deltas
// for a backend, taken only where there is one.
//
// The daily rows and never rtk's history, whose rows carry the command lines — the
// rule the heartbeat's reading follows, through the same Gain.
//
// TODO: every request spawns rtk, for up to its timeout. One screen opened by the
// key's holder is what that is sized for — and the same screen asking again every
// thirty seconds while it has no reading to draw: rtk has recorded nothing, or gave
// a problem. That ends at the first day, or when the problem is repaired. A surface
// that polled it faster would want the last reading kept for a minute.
func (s *Server) handleRTK(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "neverseen: use GET to read what rtk recorded", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorised(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	// Looked up once, so the binary named in the answer is the one that was run.
	var reply rtkReply
	if s.rtk.path != nil {
		reply.Path = s.rtk.path()
	}
	if reply.Path == "" {
		writeJSON(w, reply)
		return
	}
	out, err := s.rtk.gain(r.Context(), reply.Path)
	now := time.Now()
	reply.Installed, reply.ReadAt = true, &now

	var days []telemetry.RTKDay
	if err == nil {
		days, err = telemetry.GainDays(out)
	}
	if err != nil {
		reply.Problem = err.Error()
		writeJSON(w, reply)
		return
	}
	reply.Days = make([]rtkDayReply, 0, len(days))
	for _, day := range days {
		reply.Days = append(reply.Days, rtkDayReply{Date: day.Date, Commands: day.Commands, Input: day.Input, Output: day.Output})
	}
	writeJSON(w, reply)
}
