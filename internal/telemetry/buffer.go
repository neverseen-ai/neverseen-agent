package telemetry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"

	"github.com/neverseen-ai/neverseen-agent/internal/secure"
)

// maxBufferedBuckets caps the queue however long the outage and however short the
// interval.
//
// Two thousand and sixteen is a week of five-minute buckets, which is the same
// period maxWindowAge expresses in time — both are here because the interval is
// configurable, and an agent set to report every ten seconds would reach the age
// bound having queued sixty thousand buckets. Whichever bound is hit first, the
// oldest go and the loss is counted.
const maxBufferedBuckets = 2016

// maxBucketsPerRequest is how many buckets one batch carries.
//
// The bound is the backend's body limit, not politeness: at a few hundred bytes a
// bucket, sixty of them is a request of tens of kilobytes even with every category
// and several models on each — well inside what the service accepts, with room for
// the catalogue to grow. Sixty is also five hours of a five-minute cadence, so a
// week of backlog is thirty-four requests rather than two thousand, and an attempt
// a second drains it in about half a minute.
const maxBucketsPerRequest = 60

// bucket is one interval's counters, waiting for a backend that will take it.
type bucket struct {
	Window   telemetry.Window   `json:"window"`
	Counters telemetry.Counters `json:"counters"`
}

// buffer is the queue of buckets not yet delivered, mirrored to a file.
//
// A queue rather than one merged window, because the record has to keep its
// five-minute grain through an outage: merged, a backend down over a weekend comes
// back to a single bucket saying "eleven thousand requests, some time between
// Friday and Monday" — which cannot answer when a spike happened, when an agent
// restarted, or whether the policy changed halfway through. Queued, the outage
// costs nothing but the delay.
//
// On disk because the buffer is otherwise only as durable as the process: a
// workstation rebooted, suspended or updated during an outage would lose every
// queued bucket *and* the dropped counter that recorded the loss, which is the one
// outcome that counter exists to prevent.
//
// Touched only by the reporter's own goroutine, so it holds no lock. Nothing here
// is reachable from the request path.
type buffer struct {
	// path holds the closed buckets, rewritten only when one is closed or
	// delivered. The bucket in progress goes beside it under livePathFor(path),
	// rewritten far more often — two files rather than one because a snapshot every
	// thirty seconds that re-serialised the whole backlog would, during a long
	// outage on a busy workstation, put a megabyte of JSON on the disk twice a
	// minute for as long as the outage lasted.
	//
	// Derived rather than held as a second field, so a buffer cannot be built with
	// one path set and the other empty.
	path string

	buckets []bucket

	// live is the bucket still being filled, written between the intervals that
	// close them so that a hard kill costs a bounded amount rather than whatever
	// had accumulated. Nil once it has been closed into buckets.
	live *bucket

	// dropped counts buckets discarded for age or for the count bound since the
	// last time the figure was collected, so the loss is reported rather than
	// silent.
	dropped int
}

// bufferFile is the on-disk shape. A wrapper rather than a bare array, so a later
// field can be added without the file becoming unreadable to the version that
// wrote it.
type bufferFile struct {
	Buckets []bucket `json:"buckets"`

	// Dropped is the losses not yet reported. On disk with the buckets, because a
	// count that lived only in memory would be lost by exactly the crash the file
	// exists to survive — and a loss nobody counted is indistinguishable from a
	// quiet period, which is the one outcome the figure exists to prevent.
	Dropped int `json:"dropped,omitempty"`
}

// liveFile is the bucket in progress, in its own small file.
//
// Its own file, and its own type, because it is the one entry that is *not* final:
// the running process rewrites it every snapshot interval, and the interval that
// closes it replaces it with an ordinary bucket. Filed as an ordinary bucket only
// by the process that finds it after a crash.
type liveFile struct {
	Live bucket `json:"live"`
}

// loadBuffer reads the queue at path. A missing file is the normal state of an
// agent whose backend has been answering, and not an error.
func loadBuffer(path string) (*buffer, error) {
	b := &buffer{path: path}

	// A queue that cannot be read costs everything it held, and the loss is counted
	// rather than passed over. Counted as one, because the file did not parse and
	// how many buckets were in it is exactly what is unknown — one is not the true
	// figure, it is the difference between a gap in the record and a period that
	// looks quiet. That distinction is the whole reason the counter exists, and the
	// live file five lines below had it while this one did not: the caller logs the
	// error and carries on, save() then finds nothing queued and no loss declared,
	// and deletes the evidence.
	raw, err := os.ReadFile(filepath.Clean(path))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		b.dropped++
		return b, fmt.Errorf("read the buffered buckets: %w", err)
	default:
		var stored bufferFile
		if err := json.Unmarshal(raw, &stored); err != nil {
			b.dropped++
			return b, fmt.Errorf("parse %s: %w", path, err)
		}
		b.dropped = stored.Dropped
		b.buckets = b.keepUsable(stored.Buckets)
	}

	// The bucket the previous process did not survive to close, filed as one of its
	// own — newest, which is where it belongs. Its window ends at the last snapshot
	// rather than at the moment of the kill, so what was counted in between is
	// genuinely gone; the window says exactly which period it covers, so nothing is
	// attributed to a period nobody measured.
	//
	// A live file that cannot be read costs only that one bucket, so it is counted
	// and stepped over rather than failing the whole recovery.
	live, err := os.ReadFile(livePathFor(b.path))
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		b.dropped++
		return b, fmt.Errorf("read the bucket in progress: %w", err)
	}

	var stored liveFile
	if err := json.Unmarshal(live, &stored); err != nil {
		b.dropped++
		return b, fmt.Errorf("parse %s: %w", livePathFor(b.path), err)
	}
	b.buckets = append(b.buckets, b.keepUsable([]bucket{stored.Live})...)
	return b, nil
}

// livePathFor names the live file beside the queue it belongs to.
func livePathFor(path string) string { return filepath.Clean(path) + ".live" }

// keepUsable drops buckets nothing could do anything with, counting each one.
//
// A bucket with no window start has no age, so it could never be abandoned and
// would be carried for the life of the agent. Dropping just that one and counting
// it is the whole point: refusing the file outright would throw away every good
// bucket beside it, and leave no record that any of them existed.
func (b *buffer) keepUsable(buckets []bucket) []bucket {
	usable := buckets[:0]
	for _, kept := range buckets {
		if kept.Window.Start.IsZero() {
			b.dropped++
			continue
		}
		usable = append(usable, kept)
	}
	return usable
}

// setLive records the bucket in progress, or clears it once it has been closed.
//
// Clearing it is not housekeeping. Left in place, the counters an interval just
// closed into a real bucket would still be on disk as a live one, and the next
// process to read them would deliver both — doubling every number for that period
// on the dashboard.
func (b *buffer) setLive(kept *bucket) {
	b.live = kept
}

// add queues a bucket, and enforces both bounds.
func (b *buffer) add(kept bucket, now time.Time) {
	b.buckets = append(b.buckets, kept)
	b.prune(now)
}

// prune discards what is too old or too numerous, counting every loss.
//
// Oldest first in both cases: given a bound, the recent five minutes are the ones
// somebody is still investigating.
//
// Age is measured from where a bucket *ends*, not where it starts. What the bound
// limits is how long undelivered data is held, and a laptop suspended for a week
// wakes up with one bucket covering the whole week — because no interval elapsed
// while it slept. Measured from the start, that bucket would be discarded the
// moment it was closed, and a week of the person's activity would be counted as a
// gap in the record instead of reported.
func (b *buffer) prune(now time.Time) {
	fresh := b.buckets[:0]
	for _, kept := range b.buckets {
		if now.Sub(kept.Window.End) > maxWindowAge {
			b.dropped++
			continue
		}
		fresh = append(fresh, kept)
	}
	b.buckets = fresh

	if excess := len(b.buckets) - maxBufferedBuckets; excess > 0 {
		b.dropped += excess
		b.buckets = b.buckets[excess:]
	}
}

// pending is how many buckets are waiting.
func (b *buffer) pending() int {
	return len(b.buckets)
}

// next returns the buckets one batch will carry, oldest first.
func (b *buffer) next() []bucket {
	return b.buckets[:min(len(b.buckets), maxBucketsPerRequest)]
}

// delivered forgets the n oldest buckets, which the backend has taken.
func (b *buffer) delivered(n int) {
	b.buckets = b.buckets[min(n, len(b.buckets)):]
}

// takeDropped returns the losses since the last call, for the next bucket to
// report.
func (b *buffer) takeDropped() int {
	n := b.dropped
	b.dropped = 0
	return n
}

// save mirrors the closed buckets to disk, and the bucket in progress with them.
//
// Called when a bucket is closed or delivered, which is rare — every interval at
// most. saveLive is the one called often.
//
// TODO: what a hard kill costs is bounded by snapshotInterval rather than zero —
// counters recorded after the last snapshot are gone. Driving it to zero means
// writing on every request, which is a cost the request path should not pay.
func (b *buffer) save() error {
	// Nothing queued and no loss to declare: the file goes, so an agent whose
	// backend is answering leaves nothing behind for the next start to re-file.
	// The dropped count keeps it alive on its own — a loss nobody counted looks
	// exactly like a quiet period.
	if len(b.buckets) == 0 && b.dropped == 0 {
		if err := removeFile(b.path); err != nil {
			return fmt.Errorf("remove the buffered buckets: %w", err)
		}
	} else if err := writeFile(b.path, bufferFile{Buckets: b.buckets, Dropped: b.dropped}); err != nil {
		return fmt.Errorf("write the buffered buckets: %w", err)
	}

	return b.saveLive()
}

// saveLive mirrors just the bucket in progress, or removes it once it has been
// closed into an ordinary one.
//
// Its own call because it is the frequent one: every snapshot interval while there
// is anything to write. Rewriting the whole queue that often would, during a long
// outage on a busy workstation, put a megabyte of JSON on the disk twice a minute
// for as long as the outage lasted.
func (b *buffer) saveLive() error {
	if b.live == nil {
		if err := removeFile(livePathFor(b.path)); err != nil {
			return fmt.Errorf("remove the bucket in progress: %w", err)
		}
		return nil
	}
	if err := writeFile(livePathFor(b.path), liveFile{Live: *b.live}); err != nil {
		return fmt.Errorf("write the bucket in progress: %w", err)
	}
	return nil
}

// writeFile encodes content to path, atomically.
//
// Written to a neighbouring file and renamed over the target, because the point of
// these files is to survive the kinds of event that interrupt a write: a torn file
// would be unreadable at the next start, which loses exactly what the persistence
// was added to keep.
func writeFile(path string, content any) error {
	clean := filepath.Clean(path)

	raw, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	// A queued bucket holds no content — counts and category names only — but it does
	// hold the identity a backend knows this machine by, and it sits beside the
	// control key in the same directory. One rule for the whole of ~/.neverseen.
	temporary := clean + ".tmp"
	if err := secure.WriteFile(temporary, append(raw, '\n')); err != nil {
		return err
	}
	if err := os.Rename(temporary, clean); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// removeFile deletes path and any half-written temporary beside it.
//
// The temporary goes too, because nothing else ever will: the installer leaves
// ~/.neverseen/ alone on purpose, so a temporary left by a write that failed
// between os.WriteFile and os.Rename would sit there for the life of the machine.
func removeFile(path string) error {
	clean := filepath.Clean(path)
	if err := os.Remove(clean + ".tmp"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(clean); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
