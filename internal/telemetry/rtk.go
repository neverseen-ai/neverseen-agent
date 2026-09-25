package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// rtkTimeout bounds one `rtk gain`. It runs on the reporter's goroutine, and a
// hung binary must not hold the bucket it is closing — nor the shutdown, which
// waits for that bucket.
const rtkTimeout = 5 * time.Second

// Gain runs `rtk gain --daily --format json` and returns what it printed, or
// found false when no rtk binary is on this workstation.
//
// The JSON rather than the text `rtk gain` prints by default, because the text is
// for a reader and changes with the release; and rtk's own command rather than
// its SQLite history, whose schema is rtk's business and whose rows carry the
// command lines — content this contract refuses.
func Gain(ctx context.Context) (out []byte, found bool, err error) {
	path := rtkPath()
	if path == "" {
		return nil, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, rtkTimeout)
	defer cancel()
	out, err = exec.CommandContext(ctx, path, "gain", "--daily", "--format", "json").Output() //nolint:gosec // the path is rtk's, found below
	return out, true, err
}

// rtkPath finds the rtk binary, or returns "".
//
// PATH first, then the places installers put it. The second half is not a
// fallback for odd machines: under the installer's service the agent runs with
// launchd's PATH, /usr/bin:/bin:/usr/sbin:/sbin, where a Homebrew or cargo install
// is never found — so a lookup on PATH alone reports rtk absent on exactly the
// workstations that run the agent as intended.
//
// TODO: a fixed list misses an rtk installed anywhere else, which then reads as
// not installed. The upgrade is a setting naming the binary, added the day
// somebody has one.
func rtkPath() string {
	if path, err := exec.LookPath("rtk"); err == nil {
		return path
	}
	candidates := []string{"/opt/homebrew/bin/rtk", "/usr/local/bin/rtk"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".cargo", "bin", "rtk"),
			filepath.Join(home, ".local", "bin", "rtk"))
	}
	for _, candidate := range candidates {
		// LookPath on a path checks that one file, and on Windows tries the
		// executable extensions too.
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

// rtkDay is one row of `rtk gain --daily --format json`.
type rtkDay struct {
	Date     string `json:"date"`
	Commands int    `json:"commands"`
	Input    int    `json:"input_tokens"`
	Output   int    `json:"output_tokens"`
}

// rtkTally turns successive `rtk gain` readings into what happened between them.
//
// It works on the daily rows and never on the summary. rtk deletes its history
// past ninety days each time it records a command, so its totals fall as well as
// rise and a difference between two of them is "added minus purged" — negative
// some days. A day's row, grouped by rtk on the UTC date, only grows until that
// date is over, and the purge only ever reaches days long finished.
//
// TODO: the first reading of a process is a baseline and counts nothing, so what
// rtk did while the agent was stopped — or since the last bucket, after a hard
// kill — is never reported. Keeping the last reading beside the queue on disk
// would close it.
type rtkTally struct {
	seen   map[string]rtkDay // the last reading, by date; nil before the first
	newest string            // the latest date in it
}

// add reads one `rtk gain` output and returns what was recorded since the last.
func (t *rtkTally) add(out []byte) (telemetry.RTK, error) {
	var gain struct {
		Daily []rtkDay `json:"daily"`
	}
	if err := json.Unmarshal(out, &gain); err != nil {
		return telemetry.RTK{}, err
	}

	var delta telemetry.RTK
	seen, newest := make(map[string]rtkDay, len(gain.Daily)), t.newest
	for _, day := range gain.Daily {
		seen[day.Date] = day
		newest = max(newest, day.Date)
		if t.seen == nil {
			continue
		}
		before, known := t.seen[day.Date]
		switch {
		case !known && day.Date <= t.newest:
			// A date older than one already read that was not in the last
			// reading: nothing rtk does adds one, so it is not new activity.
		case day.Commands < before.Commands:
			// The day shrank: `rtk gain --reset`, or the purge trimming the
			// oldest day. Either way nothing in it can be told apart as new, and
			// the row is simply the next baseline.
		default:
			delta.Commands += day.Commands - before.Commands
			delta.Input += max(0, day.Input-before.Input)
			delta.Output += max(0, day.Output-before.Output)
		}
	}
	t.seen, t.newest = seen, newest
	return delta, nil
}
