package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// rtkTimeout bounds one `rtk gain`. It runs on the reporter's goroutine, and a
// hung binary must not hold the bucket it is about to close.
const rtkTimeout = 5 * time.Second

// rtkWaitDelay is how long the pipes are waited on once rtk has been killed.
// Without it the kill ends the direct child only: a wrapper script's own child
// inherits stdout, and reading it to EOF waits for that grandchild however long
// it hangs — which made rtkTimeout a bound on nothing.
const rtkWaitDelay = time.Second

// Bounds on what rtk may print. Its real answer is a row per day for ninety days,
// some twenty kilobytes; a binary that writes without end must not be buffered
// whole by a process that is meant to be a bystander, for the reason post bounds
// what the backend answers.
const (
	rtkMaxOutput = 1 << 20
	rtkMaxStderr = 1 << 10
)

// rtkEnvironment is what rtk is given of the agent's environment: enough to find
// its configuration and its history, and nothing else. The agent's environment
// carries its own secrets — the enrolment token among them — and rtk is a
// binary this agent does not own.
//
// RTK_DB_PATH is passed on when the agent has it. One exported by a shell profile
// never reaches an agent under launchd or systemd, and rtk then reads its default
// history instead; rtk's own config.toml (`tracking.database_path`) is read under
// the service, and is the setting that works in both.
var rtkEnvironment = []string{
	"HOME", "USER", "LOGNAME", "PATH", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "APPDATA", "LOCALAPPDATA", "USERPROFILE",
	"RTK_DB_PATH",
}

// Gain runs `rtk gain --daily --format json` and returns what it printed, or
// found false when no rtk binary is on this workstation.
//
// The JSON rather than the text `rtk gain` prints by default, because the text is
// for a reader and changes with the release; and rtk's own command rather than
// its SQLite history, whose schema is rtk's business and whose rows carry the
// command lines — content this contract refuses.
func Gain(ctx context.Context) (out []byte, found bool, err error) {
	path := RTKPath()
	if path == "" {
		return nil, false, nil
	}
	out, err = GainAt(ctx, path)
	return out, true, err
}

// GainAt is Gain on a binary already found, for a caller that reports which one it
// ran: looked up again, the path said need not be the path run.
func GainAt(ctx context.Context, path string) ([]byte, error) { return runGain(ctx, path) }

// RTKPath is where the rtk binary is on this workstation, or "" with none: what
// Gain would run. For the settings page, whose rtk screen makes the case for rtk
// where there is none — a file called rtk, which is less than rtk having answered.
func RTKPath() string { return rtkPath(exec.LookPath, os.UserHomeDir) }

// runGain runs one binary as rtk, bounded in time, in output and in what it is
// told of the agent.
func runGain(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, rtkTimeout)
	defer cancel()

	var stdout, stderr boundedBuffer
	stdout.max, stderr.max = rtkMaxOutput, rtkMaxStderr
	cmd := exec.CommandContext(ctx, path, "gain", "--daily", "--format", "json") //nolint:gosec // the path is rtk's, found by rtkPath
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = rtkWaitDelay

	// Every `rtk` run sends rtk's own daily ping when its user consented to one,
	// so an agent reading it every five minutes would report the workstation
	// active to rtk on days nobody used it. The opt-out is rtk's own variable, and
	// it applies to these runs alone.
	cmd.Env = []string{"RTK_TELEMETRY_DISABLED=1"}
	for _, name := range rtkEnvironment {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}

	if err := cmd.Run(); err != nil {
		// What rtk said, because "exit status 1" on its own is a warning nobody
		// can act on.
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return nil, fmt.Errorf("%w: %s", err, said)
		}
		return nil, err
	}
	if stdout.over {
		return nil, fmt.Errorf("rtk printed more than %d bytes", rtkMaxOutput)
	}
	return stdout.Bytes(), nil
}

// boundedBuffer keeps the first max bytes written to it and drops the rest,
// noting that it did. It never fails a write, so the child is stopped by the
// timeout rather than by a broken pipe, and stderr can be truncated rather than
// lost.
type boundedBuffer struct {
	strings.Builder
	max  int
	over bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); len(p) > room {
		b.over = true
		b.Builder.Write(p[:max(room, 0)])
		return len(p), nil
	}
	b.Builder.Write(p)
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte { return []byte(b.String()) }

// rtkPath finds the rtk binary, or returns "".
//
// PATH first, then the places installers put it. The second half is not a
// fallback for odd machines: under the installer's service the agent runs with
// launchd's PATH, /usr/bin:/bin:/usr/sbin:/sbin, and systemd's is no richer — so
// a lookup on PATH alone reports rtk absent on exactly the workstations that run
// the agent as intended.
//
// TODO: a fixed list misses an rtk installed anywhere else, which then reads as
// not installed. The upgrade is a setting naming the binary, added the day
// somebody has one.
func rtkPath(lookPath func(string) (string, error), home func() (string, error)) string {
	if path, err := lookPath("rtk"); err == nil {
		return path
	}
	candidates := []string{
		"/opt/homebrew/bin/rtk",              // Homebrew, Apple silicon
		"/usr/local/bin/rtk",                 // Homebrew, Intel; a manual install
		"/home/linuxbrew/.linuxbrew/bin/rtk", // Homebrew on Linux
	}
	if dir, err := home(); err == nil {
		candidates = append(candidates,
			filepath.Join(dir, ".cargo", "bin", "rtk"),
			filepath.Join(dir, ".local", "bin", "rtk"),
			filepath.Join(dir, ".linuxbrew", "bin", "rtk"))
	}
	for _, candidate := range candidates {
		// LookPath on a path checks that one file, and on Windows tries the
		// executable extensions too.
		if path, err := lookPath(candidate); err == nil {
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

// RTKDay is one day as rtk recorded it, for the settings page. The date is rtk's
// own, a UTC day (rtkTally says how that was measured).
type RTKDay = rtkDay

// GainDays reads the daily rows out of what Gain returned.
func GainDays(out []byte) ([]RTKDay, error) {
	var gain struct {
		Daily *[]rtkDay `json:"daily"`
	}
	if err := json.Unmarshal(out, &gain); err != nil {
		return nil, err
	}
	if gain.Daily == nil {
		return nil, errNoDailyRows
	}
	return *gain.Daily, nil
}

// errNoDailyRows is a reading that is JSON without the rows asked for — `{}`,
// `null`, or whatever another tool called rtk prints. Accepted as an empty
// reading, it became the baseline, and the next real one was counted whole:
// ninety days of rtk filed as one window.
var errNoDailyRows = errors.New("rtk printed no daily rows")

// rtkTally turns successive `rtk gain` readings into what happened between them.
//
// It works on the daily rows and never on the summary. rtk deletes its history
// past ninety days each time it records a command, so its totals fall as well as
// rise and a difference between two of them is "added minus purged" — negative
// some days. A day's row, grouped by rtk on the UTC date, only grows until that
// date is over, and the purge only ever reaches days long finished.
//
// The UTC date is measured, not assumed: against rtk 0.43.0's own history, the
// commands run between midnight and 2am in Paris are in the row for the UTC day
// before, on every day that had some. readOn is a UTC date for that reason. An
// rtk that grouped by the local date would have a user west of UTC lose the
// first commands of each evening to the branch below that reads an unknown past
// day as a clock gone back.
//
// TODO: the first reading of a process is a baseline and counts nothing, so what
// rtk did while the agent was stopped — or since the last bucket, after a hard
// kill — is never reported. Keeping the last reading beside the queue on disk
// would close it.
type rtkTally struct {
	seen   map[string]rtkDay // the last reading, by date; nil before the first
	readOn string            // the UTC date the last reading was taken on
}

// add reads one `rtk gain` output, taken at a given moment, and returns what was
// recorded since the last.
func (t *rtkTally) add(out []byte, at time.Time) (telemetry.RTK, error) {
	days, err := GainDays(out)
	if err != nil {
		return telemetry.RTK{}, err
	}

	var delta telemetry.RTK
	seen := make(map[string]rtkDay, len(days))
	for _, day := range days {
		seen[day.Date] = day
		if t.seen == nil {
			continue
		}
		before, known := t.seen[day.Date]
		switch {
		case !known && day.Date < t.readOn:
			// A day that ended before the last reading and was not in it: rtk only
			// adds rows for the moment it records, so this is a clock that went
			// back, not activity. Judged against when the last reading was taken
			// rather than against the dates it held, because a row dated in the
			// future by a clock set wrong once would otherwise have every new day
			// discarded until that date came round.
		case day.Commands < before.Commands || day.Input < before.Input || day.Output < before.Output:
			// The day shrank: `rtk gain --reset`, or the purge trimming the oldest
			// day. Nothing in it can be told apart as new, and the row is simply
			// the next baseline. Any of the three, because a reset followed by as
			// many commands again leaves the count where it was while the tokens
			// fall, and differencing each field alone filed more output than input.
		default:
			delta.Commands += day.Commands - before.Commands
			delta.Input += day.Input - before.Input
			delta.Output += day.Output - before.Output
		}
	}
	t.seen, t.readOn = seen, at.UTC().Format(time.DateOnly)
	return delta, nil
}
