package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

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

// Each reading counts what the days gained since the last one — and nothing that
// rtk's own bookkeeping moved. Every case here is one a difference of totals gets
// wrong.
func TestRTKTallyCountsWhatTheDaysGained(t *testing.T) {
	var tally rtkTally
	steps := []struct {
		name string
		out  []byte
		want telemetry.RTK
	}{
		{
			// Whatever rtk holds when the agent starts happened before it: counted
			// here, a restart would file ninety days of rtk in one bucket.
			name: "the first reading is a baseline",
			out:  gainOf("2026-09-24:100:5000:500", "2026-09-25:10:1000:100"),
		},
		{
			name: "today grew",
			out:  gainOf("2026-09-24:100:5000:500", "2026-09-25:13:1600:130"),
			want: telemetry.RTK{Commands: 3, Input: 600, Output: 30},
		},
		{
			// Midnight UTC between two readings: the end of yesterday and the
			// start of today both count.
			name: "a new day, and the end of the last one",
			out:  gainOf("2026-09-24:100:5000:500", "2026-09-25:15:1800:150", "2026-09-26:2:40:4"),
			want: telemetry.RTK{Commands: 4, Input: 240, Output: 24},
		},
		{
			// rtk deletes rows past ninety days whenever it records a command, so
			// the summary falls here; the oldest day going, or shrinking, is not
			// activity and not a negative either.
			name: "the purge takes the oldest day",
			out:  gainOf("2026-09-25:15:1800:150", "2026-09-26:3:60:6"),
			want: telemetry.RTK{Commands: 1, Input: 20, Output: 2},
		},
		{
			// `rtk gain --reset`: every day shrinks or goes. What ran since the
			// reset cannot be told apart, so the reading becomes the baseline.
			name: "a reset counts nothing",
			out:  gainOf("2026-09-26:1:10:1"),
		},
		{
			name: "and the next reading counts from it",
			out:  gainOf("2026-09-26:4:70:7"),
			want: telemetry.RTK{Commands: 3, Input: 60, Output: 6},
		},
		{
			// Nothing rtk does adds a day before one already read.
			name: "a past day appearing is not new activity",
			out:  gainOf("2026-09-20:50:900:90", "2026-09-26:4:70:7"),
		},
	}
	for _, step := range steps {
		got, err := tally.add(step.out)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got != step.want {
			t.Errorf("%s: counted %+v, want %+v", step.name, got, step.want)
		}
	}

	if _, err := tally.add([]byte("rtk: database is locked")); err == nil {
		t.Error("a reading that is not JSON was accepted")
	}
}

// The reporter files what rtk recorded in the bucket it closes, says in State
// whether rtk is there, and never lets rtk failing cost the bucket.
func TestTheReporterFilesWhatRTKRecorded(t *testing.T) {
	b := newBackend(t)
	r := NewRecorder(epoch)
	rep, _ := newTestReporter(t, b, r)

	readings := []struct {
		out   []byte
		found bool
		err   error
	}{
		{out: gainOf("2026-09-25:10:1000:100"), found: true},
		{out: gainOf("2026-09-25:12:1500:120"), found: true},
		{found: true, err: errors.New("signal: killed")},
		{found: false},
	}
	next := 0
	rep.gain = func(context.Context) ([]byte, bool, error) {
		reading := readings[next]
		next++
		return reading.out, reading.found, reading.err
	}

	for range readings {
		rep.report(t.Context())
	}

	got, _, _ := b.received()
	if len(got) != len(readings) {
		t.Fatalf("the backend saw %d heartbeats, want %d", len(got), len(readings))
	}
	if want := (telemetry.RTK{Commands: 2, Input: 500, Output: 20}); got[1].Counters.RTK != want {
		t.Errorf("the second bucket carries %+v, want %+v", got[1].Counters.RTK, want)
	}
	for i, want := range []bool{true, true, true, false} {
		if got[i].State.RTK != want {
			t.Errorf("heartbeat %d reports rtk installed %v, want %v", i, got[i].State.RTK, want)
		}
	}
	if got[2].Counters.RTK != (telemetry.RTK{}) {
		t.Errorf("a failed reading counted %+v", got[2].Counters.RTK)
	}
}
