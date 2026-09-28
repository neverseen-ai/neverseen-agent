package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"
)

// gainOf renders days the way `rtk gain --daily --format json` prints them, as
// "date:commands:input:output", with the summary rtk puts above them — which the
// tally must not read, so it is given figures that would show if it did.
func gainOf(days ...string) []byte {
	rows := make([]string, 0, len(days))
	for _, day := range days {
		var date string
		var commands, input, output int
		if _, err := fmt.Sscanf(strings.ReplaceAll(day, ":", " "), "%s %d %d %d",
			&date, &commands, &input, &output); err != nil {
			panic(err)
		}
		rows = append(rows, fmt.Sprintf(
			`{"date":%q,"commands":%d,"input_tokens":%d,"output_tokens":%d,"saved_tokens":%d}`,
			date, commands, input, output, input-output))
	}
	return []byte(`{"summary":{"total_commands":999999,"total_input":999999,"total_output":1},` +
		`"daily":[` + strings.Join(rows, ",") + `]}`)
}

// on is a moment on a UTC date, for dating a reading.
func on(date string) time.Time {
	at, err := time.Parse(time.DateOnly, date)
	if err != nil {
		panic(err)
	}
	return at.Add(12 * time.Hour)
}

// Each reading counts what the days gained since the last one — and nothing that
// rtk's own bookkeeping moved. Every case here is one a difference of totals, or
// of fields taken one at a time, gets wrong.
func TestRTKTallyCountsWhatTheDaysGained(t *testing.T) {
	var tally rtkTally
	steps := []struct {
		name string
		out  []byte
		at   string
		want telemetry.RTK
	}{
		{
			// Whatever rtk holds when the agent starts happened before it: counted
			// here, a restart would file ninety days of rtk in one bucket.
			name: "the first reading is a baseline",
			out:  gainOf("2026-09-24:100:5000:500", "2026-09-25:10:1000:100"),
			at:   "2026-09-25",
		},
		{
			name: "today grew",
			out:  gainOf("2026-09-24:100:5000:500", "2026-09-25:13:1600:130"),
			at:   "2026-09-25",
			want: telemetry.RTK{Commands: 3, Input: 600, Output: 30},
		},
		{
			// Midnight UTC between two readings: the end of yesterday and the
			// start of today both count.
			name: "a new day, and the end of the last one",
			out:  gainOf("2026-09-24:100:5000:500", "2026-09-25:15:1800:150", "2026-09-26:2:40:4"),
			at:   "2026-09-26",
			want: telemetry.RTK{Commands: 4, Input: 240, Output: 24},
		},
		{
			// rtk deletes rows past ninety days whenever it records a command, so
			// the summary falls here; the oldest day going, or shrinking, is not
			// activity and not a negative either.
			name: "the purge takes the oldest day",
			out:  gainOf("2026-09-25:15:1800:150", "2026-09-26:3:60:6"),
			at:   "2026-09-26",
			want: telemetry.RTK{Commands: 1, Input: 20, Output: 2},
		},
		{
			// `rtk gain --reset` and then as many commands again as there were: the
			// count is back where it was, the tokens are not. Differenced field by
			// field this filed output with no input — a negative saving.
			name: "a reset the command count does not show",
			out:  gainOf("2026-09-26:3:20:10"),
			at:   "2026-09-26",
		},
		{
			name: "and the next reading counts from it",
			out:  gainOf("2026-09-26:6:80:16"),
			at:   "2026-09-26",
			want: telemetry.RTK{Commands: 3, Input: 60, Output: 6},
		},
		{
			// Nothing rtk does adds a row for a day already over.
			name: "a past day appearing is not new activity",
			out:  gainOf("2026-09-20:50:900:90", "2026-09-26:6:80:16"),
			at:   "2026-09-26",
		},
		{
			// A clock set a year ahead once. The row is real activity, and it must
			// not make every later day look like one already over.
			name: "a row dated in the future",
			out:  gainOf("2026-09-20:50:900:90", "2026-09-26:6:80:16", "2027-09-26:1:10:1"),
			at:   "2026-09-26",
			want: telemetry.RTK{Commands: 1, Input: 10, Output: 1},
		},
		{
			name: "does not hide the next day",
			out:  gainOf("2026-09-26:6:80:16", "2026-09-27:2:30:3", "2027-09-26:1:10:1"),
			at:   "2026-09-27",
			want: telemetry.RTK{Commands: 2, Input: 30, Output: 3},
		},
	}
	for _, step := range steps {
		got, err := tally.add(step.out, on(step.at))
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got != step.want {
			t.Errorf("%s: counted %+v, want %+v", step.name, got, step.want)
		}
	}
}

// A reading without the rows asked for is refused and changes nothing. Taken as an
// empty reading it became the baseline, and the next real one was counted whole.
func TestRTKTallyRefusesAReadingWithoutRows(t *testing.T) {
	for _, out := range []string{`{}`, `null`, `{"summary":{"total_commands":5}}`, `rtk: database is locked`} {
		var tally rtkTally
		if _, err := tally.add([]byte(out), on("2026-09-25")); err == nil {
			t.Errorf("%s was accepted as a reading", out)
		}
		got, err := tally.add(gainOf("2026-06-30:5000:9000000:700000", "2026-09-25:10:1000:100"), on("2026-09-25"))
		if err != nil {
			t.Fatal(err)
		}
		if got != (telemetry.RTK{}) {
			t.Errorf("after %s, the first real reading counted %+v — it is the baseline", out, got)
		}
	}

	// An empty history is a reading, and the day it starts is counted.
	var tally rtkTally
	if _, err := tally.add([]byte(`{"daily":[]}`), on("2026-09-25")); err != nil {
		t.Fatalf("an empty history was refused: %v", err)
	}
	got, err := tally.add(gainOf("2026-06-30:5000:9000000:700000", "2026-09-25:2:30:3"), on("2026-09-25"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (telemetry.RTK{Commands: 2, Input: 30, Output: 3}); got != want {
		t.Errorf("counted %+v, want only today's %+v", got, want)
	}
}

// The reporter files what rtk recorded in the bucket it closes, says in State
// whether rtk answered, and never lets rtk failing cost the bucket.
func TestTheReporterFilesWhatRTKRecorded(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)
	rep, _ := newTestReporter(t, b, r)
	var logged bytes.Buffer
	rep.log = slog.New(slog.NewTextHandler(&logged, nil))

	type reading struct {
		out   []byte
		found bool
		err   error
	}
	// A workstation where rtk is removed, and then a different tool called rtk
	// appears — the Rust Type Kit shares the name — before the real one returns.
	readings := []reading{
		{out: gainOf("2026-09-25:10:1000:100"), found: true},
		{out: gainOf("2026-09-25:12:1500:120"), found: true},
		{found: true, err: errors.New("signal: killed")},
		{found: false},
		{found: true, err: errors.New("exit status 2: error: no such command: gain")},
		{found: true, err: errors.New("exit status 2: error: no such command: gain")},
		{out: gainOf("2026-09-25:14:1700:140"), found: true},
		{out: gainOf("2026-09-25:15:1800:150"), found: true},
	}
	next := 0
	rep.gain = func(context.Context) ([]byte, bool, error) {
		one := readings[next]
		next++
		return one.out, one.found, one.err
	}

	for range readings {
		rep.report(t.Context())
	}

	got, _, _ := b.received()
	if len(got) != len(readings) {
		t.Fatalf("the backend saw %d heartbeats, want %d", len(got), len(readings))
	}
	want := make([]telemetry.RTK, len(readings))
	want[1] = telemetry.RTK{Commands: 2, Input: 500, Output: 20}
	// Read from scratch after rtk went: the reading after it is a baseline.
	want[7] = telemetry.RTK{Commands: 1, Input: 100, Output: 10}
	for i := range got {
		if got[i].Counters.RTK != want[i] {
			t.Errorf("bucket %d carries %+v, want %+v", i, got[i].Counters.RTK, want[i])
		}
	}
	for i, want := range []bool{true, true, false, false, false, false, true, true} {
		if got[i].State.RTK == nil || *got[i].State.RTK != want {
			t.Errorf("heartbeat %d reports rtk %v, want %v", i, got[i].State.RTK, want)
		}
	}

	// Said when it starts failing and when it stops, not at every interval: the
	// other tool would otherwise file a warning 288 times a day.
	if n := strings.Count(logged.String(), "gave no reading"); n != 2 {
		t.Errorf("the failure was logged %d times, want once per spell of it:\n%s", n, logged.String())
	}
	if !strings.Contains(logged.String(), "no such command") {
		t.Errorf("the log does not say what rtk said:\n%s", logged.String())
	}
}

// Nil is "not checked", never "absent": a reporter that does not read rtk, like an
// agent older than the field, says nothing about it — and so does one that has
// not read it yet, which is where the enrolment is sent from.
func TestRTKIsNotCheckedUntilItIsRead(t *testing.T) {
	b := newBackend(t)
	rep, _ := newTestReporter(t, b, NewRecorder(epoch))
	rep.report(t.Context())
	if got, _, _ := b.received(); len(got) != 1 || got[0].State.RTK != nil {
		t.Fatalf("a reporter that never reads rtk reported %v, want nothing", got[0].State.RTK)
	}

	rep.gain = func(context.Context) ([]byte, bool, error) { return nil, false, nil }
	if rep.currentState().RTK != nil {
		t.Error("rtk is reported before it was ever read")
	}
	rep.report(t.Context())
	if got, _, _ := b.received(); got[1].State.RTK == nil || *got[1].State.RTK {
		t.Errorf("a workstation without rtk reported %v, want false", got[1].State.RTK)
	}
}

// What rtk recorded across a sleep is in no window: the one closed at the sleep
// ends before it happened, and the next opens at the wake.
func TestRTKAcrossASleepIsInNoWindow(t *testing.T) {
	b := newBackend(t)
	now := epoch
	r := NewRecorder(now)
	rep, _ := reporterInHome(t, b, r, t.TempDir(), func() time.Time { return now })

	readings := [][]byte{
		gainOf("2026-09-25:10:1000:100"),
		gainOf("2026-09-25:40:9000:900"), // read at the wake, three hours of use later
		gainOf("2026-09-25:41:9100:910"),
	}
	rep.gain = func(context.Context) ([]byte, bool, error) {
		out := readings[0]
		readings = readings[1:]
		return out, true, nil
	}

	rep.collect()
	rep.awake = now
	now = now.Add(3 * time.Hour)
	if !rep.wake() {
		t.Fatal("three hours was not taken for a sleep")
	}
	now = now.Add(5 * time.Minute)
	rep.collect()

	buckets := rep.queue.buckets
	if len(buckets) != 3 {
		t.Fatalf("queued %d buckets, want the baseline, the one closed at the sleep, and the next", len(buckets))
	}
	if buckets[1].Counters.RTK != (telemetry.RTK{}) {
		t.Errorf("the window closed at the sleep carries %+v of rtk recorded after it ended",
			buckets[1].Counters.RTK)
	}
	if want := (telemetry.RTK{Commands: 1, Input: 100, Output: 10}); buckets[2].Counters.RTK != want {
		t.Errorf("the window after the wake carries %+v, want only what followed it, %+v",
			buckets[2].Counters.RTK, want)
	}
}

// A stop does not wait on rtk: its five seconds come out of what the service
// manager allows, which the last heartbeat needs.
func TestAStopDoesNotReadRTK(t *testing.T) {
	b := newBackend(t)
	rep, _ := newTestReporter(t, b, NewRecorder(epoch))
	calls := 0
	rep.gain = func(context.Context) ([]byte, bool, error) {
		calls++
		return gainOf("2026-09-25:1:10:1"), true, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rep.Run(ctx)

	if calls != 1 {
		t.Errorf("rtk was read %d times, want once — at the start, and not on the way out", calls)
	}
}

// PATH first, then where installers put rtk, which is what an agent under launchd
// or systemd needs: neither service PATH holds a Homebrew or a cargo directory.
func TestRTKPathLooksWhereInstallersPutIt(t *testing.T) {
	home := func() (string, error) { return "/home/ada", nil }
	var tried []string
	lookIn := func(found string) func(string) (string, error) {
		return func(name string) (string, error) {
			tried = append(tried, name)
			if name == found {
				return name, nil
			}
			return "", errors.New("not found")
		}
	}

	if got := rtkPath(lookIn("rtk"), home); got != "rtk" {
		t.Errorf("found %q, want the one on PATH", got)
	}

	for _, want := range []string{
		"/opt/homebrew/bin/rtk",
		"/home/linuxbrew/.linuxbrew/bin/rtk",
		filepath.Join("/home/ada", ".cargo", "bin", "rtk"),
		filepath.Join("/home/ada", ".local", "bin", "rtk"),
	} {
		tried = nil
		if got := rtkPath(lookIn(want), home); got != want {
			t.Errorf("found %q, want %q", got, want)
		}
		if tried[0] != "rtk" {
			t.Errorf("tried %v, want PATH first", tried)
		}
	}

	tried = nil
	if got := rtkPath(lookIn(""), home); got != "" {
		t.Errorf("found %q where there is nothing", got)
	}
	if !slices.Contains(tried, "/usr/local/bin/rtk") {
		t.Errorf("tried %v", tried)
	}
}

// stubRTK writes an executable called rtk running script, and returns its path.
func stubRTK(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	path := filepath.Join(t.TempDir(), "rtk")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil { //nolint:gosec // an executable is the point
		t.Fatal(err)
	}
	return path
}

// Gain runs the rtk it finds with the arguments rtk takes, and gives it nothing of
// the agent's environment but what it needs to find its history.
func TestGainRunsRTKWithoutTheAgentsSecrets(t *testing.T) {
	dir := t.TempDir()
	args, env := filepath.Join(dir, "args"), filepath.Join(dir, "env")
	stub := stubRTK(t, fmt.Sprintf("echo \"$@\" > %q\n/usr/bin/env > %q\necho '{\"daily\":[]}'\n", args, env))

	t.Setenv("PATH", filepath.Dir(stub))
	t.Setenv("NEVERSEEN_ENROLMENT_TOKEN", "enrol-me")
	t.Setenv("RTK_DB_PATH", "/data/rtk.db")

	out, found, err := Gain(t.Context())
	if err != nil || !found {
		t.Fatalf("found %v, error %v", found, err)
	}
	if string(bytes.TrimSpace(out)) != `{"daily":[]}` {
		t.Errorf("returned %q", out)
	}

	gotArgs, _ := os.ReadFile(args) //nolint:gosec // a path this test made
	if strings.TrimSpace(string(gotArgs)) != "gain --daily --format json" {
		t.Errorf("rtk was run with %q", gotArgs)
	}
	gotEnv, _ := os.ReadFile(env) //nolint:gosec // a path this test made
	for _, want := range []string{"RTK_TELEMETRY_DISABLED=1", "RTK_DB_PATH=/data/rtk.db", "HOME="} {
		if !strings.Contains(string(gotEnv), want) {
			t.Errorf("rtk's environment lacks %s:\n%s", want, gotEnv)
		}
	}
	if strings.Contains(string(gotEnv), "NEVERSEEN_") {
		t.Errorf("rtk was handed the agent's own settings:\n%s", gotEnv)
	}
}

func TestGainReportsNoRTK(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("the fallback locations are Unix ones")
	}
	for _, candidate := range []string{"/opt/homebrew/bin/rtk", "/usr/local/bin/rtk", "/home/linuxbrew/.linuxbrew/bin/rtk"} {
		if _, err := os.Stat(candidate); err == nil {
			t.Skipf("%s exists on this machine", candidate)
		}
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, found, err := Gain(t.Context()); found || err != nil {
		t.Errorf("found %v, error %v, on a machine with no rtk", found, err)
	}
}

// A failing rtk's error says what rtk said.
func TestGainSaysWhatRTKSaid(t *testing.T) {
	stub := stubRTK(t, "echo 'Error: database is locked' >&2\nexit 1\n")
	_, err := runGain(t.Context(), stub)
	if err == nil || !strings.Contains(err.Error(), "database is locked") {
		t.Errorf("error %v, want what rtk printed", err)
	}
}

// The bound holds when rtk is a wrapper whose own child hangs holding stdout —
// the case killing the direct child alone does not end.
func TestGainIsBoundedWhenAGrandchildHangs(t *testing.T) {
	stub := stubRTK(t, "sleep 5 &\nsleep 5\n")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := runGain(ctx, stub); err == nil {
		t.Error("a hung rtk gave a reading")
	}
	if took := time.Since(start); took > 200*time.Millisecond+rtkWaitDelay+time.Second {
		t.Errorf("returned after %s", took)
	}
}

// What rtk prints is bounded, so a binary that writes without end is refused
// rather than buffered whole.
func TestGainBoundsWhatRTKPrints(t *testing.T) {
	stub := stubRTK(t, fmt.Sprintf("head -c %d /dev/zero\n", 2*rtkMaxOutput))
	if _, err := runGain(t.Context(), stub); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("error %v, want the output refused", err)
	}
}

// A reading after rtk failed is a baseline, like one after a sleep. The failure
// has no reading of its own to cut the period at, so what rtk recorded while it
// failed cannot be told from what it recorded since — and differenced against
// the reading before the failure, two hours of rtk were filed as one window.
func TestRTKAfterAFailureIsInNoWindow(t *testing.T) {
	b := newBackend(t)
	now := epoch
	r := NewRecorder(now)
	rep, _ := reporterInHome(t, b, r, t.TempDir(), func() time.Time { return now })

	type reading struct {
		out []byte
		err error
	}
	readings := []reading{
		{out: gainOf("2026-09-25:10:1000:100")},
		{err: errors.New("database is locked")},
		{out: gainOf("2026-09-25:40:9000:900")}, // two hours of use later
		{out: gainOf("2026-09-25:41:9100:910")},
	}
	rep.gain = func(context.Context) ([]byte, bool, error) {
		next := readings[0]
		readings = readings[1:]
		return next.out, true, next.err
	}

	for range 4 {
		rep.collect()
		now = now.Add(5 * time.Minute)
	}

	buckets := rep.queue.buckets
	if len(buckets) != 4 {
		t.Fatalf("queued %d buckets, want four", len(buckets))
	}
	if buckets[2].Counters.RTK != (telemetry.RTK{}) {
		t.Errorf("the first window after the failure carries %+v: what rtk recorded while it "+
			"failed, filed as five minutes", buckets[2].Counters.RTK)
	}
	if want := (telemetry.RTK{Commands: 1, Input: 100, Output: 10}); buckets[3].Counters.RTK != want {
		t.Errorf("the window after that carries %+v, want only what followed it, %+v",
			buckets[3].Counters.RTK, want)
	}
}
