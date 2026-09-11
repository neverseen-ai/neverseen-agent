package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neverseen-ai/neverseen-agent/pkg/telemetry"

	"github.com/neverseen-ai/neverseen-agent/internal/secure"
)

func bucketAt(start time.Time, requests int) bucket {
	return bucket{
		Window:   telemetry.Window{Start: start, End: start.Add(5 * time.Minute)},
		Counters: telemetry.Counters{Requests: requests},
	}
}

func TestBufferRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "buffer.json")

	// Nothing there is the normal state of an agent whose backend is answering,
	// and it is not an error.
	queue, err := loadBuffer(path)
	if err != nil || queue.pending() != 0 {
		t.Fatalf("loadBuffer on a missing file = (%d, %v), want (0, nil)", queue.pending(), err)
	}

	for i := range 3 {
		queue.add(bucketAt(epoch.Add(time.Duration(i)*5*time.Minute), i+1), epoch.Add(time.Hour))
	}
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}

	back, err := loadBuffer(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.pending() != 3 {
		t.Fatalf("%d buckets came back, want 3", back.pending())
	}
	// In order, oldest first: a queue that came back shuffled would file an
	// outage out of sequence.
	for i, kept := range back.buckets {
		if kept.Counters.Requests != i+1 {
			t.Errorf("bucket %d carries %d requests, want %d", i, kept.Counters.Requests, i+1)
		}
	}

	// Readable only by its owner, like the identity beside it: it is a record of
	// what one person's workstation did, on a machine that may have other users.
	if ok, why := secure.IsRestricted(path); !ok {
		t.Errorf("the buffer file is readable by more than its owner: %s", why)
	}

	// Emptied, the file goes: an agent whose backend is answering leaves nothing
	// behind, and a stale file would be re-read and re-filed at the next start.
	back.delivered(3)
	if err := back.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the buffer file survived an emptied queue: %v", err)
	}
}

// Both bounds, and both counted. Either one going unreported would make a lost
// period indistinguishable from a quiet one.
func TestBufferBoundsAreEnforcedAndCounted(t *testing.T) {
	t.Run("age", func(t *testing.T) {
		queue := &buffer{path: filepath.Join(t.TempDir(), "buffer.json")}
		queue.add(bucketAt(epoch, 1), epoch)
		queue.add(bucketAt(epoch.Add(maxWindowAge), 2), epoch.Add(maxWindowAge))

		// A week and a bit after the first bucket ended, it is past the bound.
		queue.prune(epoch.Add(maxWindowAge + time.Hour))
		if queue.pending() != 1 || queue.buckets[0].Counters.Requests != 2 {
			t.Errorf("%d buckets left, want only the recent one", queue.pending())
		}
		if dropped := queue.takeDropped(); dropped != 1 {
			t.Errorf("dropped = %d, want 1", dropped)
		}
		// Reported once. Counted again, a single loss would inflate the gap every
		// five minutes for as long as the agent ran.
		if dropped := queue.takeDropped(); dropped != 0 {
			t.Errorf("dropped = %d on the second read, want 0", dropped)
		}
	})

	t.Run("count", func(t *testing.T) {
		queue := &buffer{path: filepath.Join(t.TempDir(), "buffer.json")}
		// One over the bound, all of them fresh: it is the count that has to bite,
		// because the interval is configurable and an agent reporting every ten
		// seconds reaches the age bound having queued sixty thousand buckets.
		for i := range maxBufferedBuckets + 1 {
			at := epoch.Add(time.Duration(i) * 5 * time.Minute)
			queue.add(bucketAt(at, i), at)
		}

		if queue.pending() != maxBufferedBuckets {
			t.Errorf("%d buckets queued, want the bound of %d", queue.pending(), maxBufferedBuckets)
		}
		if dropped := queue.takeDropped(); dropped != 1 {
			t.Errorf("dropped = %d, want 1", dropped)
		}
		// The oldest went, not the newest: given a bound, the recent five minutes
		// are the ones somebody is still investigating.
		if queue.buckets[0].Counters.Requests != 1 {
			t.Errorf("the queue starts at bucket %d, want 1 — the oldest is what goes",
				queue.buckets[0].Counters.Requests)
		}
	})
}

// A file half-written by an interrupted process must not be read as a queue: it
// would report counters nobody measured.
func TestATornBufferFileIsRefusedNotGuessed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buffer.json")
	if err := os.WriteFile(path, []byte(`{"buckets":[{"counters":{"requests":3`), 0o600); err != nil {
		t.Fatal(err)
	}

	queue, err := loadBuffer(path)
	if err == nil || queue.pending() != 0 {
		t.Errorf("a truncated file was accepted: %d buckets, err %v", queue.pending(), err)
	}
}

// One unusable bucket costs one bucket, not the backlog beside it — and the loss
// is counted, because a bucket that vanished without a figure to show for it looks
// exactly like a quiet five minutes.
func TestAnUnusableBucketIsSteppedOverAndCounted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buffer.json")
	// The middle one has no window start: it has no age, so it could never be
	// abandoned and would be carried for the life of the agent.
	body := `{"buckets":[
		{"window":{"start":"2026-08-25T12:00:00Z","end":"2026-08-25T12:05:00Z"},"counters":{"requests":1}},
		{"counters":{"requests":99}},
		{"window":{"start":"2026-08-25T12:05:00Z","end":"2026-08-25T12:10:00Z"},"counters":{"requests":2}}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	queue, err := loadBuffer(path)
	if err != nil {
		t.Fatal(err)
	}
	if queue.pending() != 2 {
		t.Fatalf("%d buckets survived, want the 2 that were usable", queue.pending())
	}
	if queue.buckets[0].Counters.Requests != 1 || queue.buckets[1].Counters.Requests != 2 {
		t.Errorf("the surviving buckets are %+v, want the first and the third", queue.buckets)
	}
	if dropped := queue.takeDropped(); dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
}

// The dropped count is on disk with the buckets. In memory alone it would be lost
// by exactly the crash the file exists to survive, and CLAUDE.md names that as the
// one outcome the figure exists to prevent.
func TestTheDroppedCountSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buffer.json")

	queue := &buffer{path: path}
	queue.add(bucketAt(epoch, 1), epoch)
	queue.dropped = 3
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}

	back, err := loadBuffer(path)
	if err != nil {
		t.Fatal(err)
	}
	if dropped := back.takeDropped(); dropped != 3 {
		t.Errorf("dropped = %d after a restart, want 3", dropped)
	}

	// And a loss with nothing queued still keeps the file alive: an empty queue
	// that discarded the count would report the gap as a quiet period.
	only := &buffer{path: path}
	only.dropped = 2
	if err := only.save(); err != nil {
		t.Fatal(err)
	}
	back, err = loadBuffer(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.pending() != 0 || back.takeDropped() != 2 {
		t.Errorf("a queue holding only a loss came back as %d buckets and %d dropped",
			back.pending(), back.dropped)
	}
}

// Nothing queued and nothing lost leaves nothing behind — including the temporary
// a failed write can strand. The installer leaves ~/.neverseen/ alone on purpose,
// so nothing else ever would.
func TestAnEmptyQueueLeavesNoFilesBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "buffer.json")

	queue := &buffer{path: path}
	queue.add(bucketAt(epoch, 1), epoch)
	queue.setLive(&bucket{Window: telemetry.Window{Start: epoch, End: epoch}})
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}
	// A write that failed between the temporary and the rename.
	if err := os.WriteFile(path+".tmp", []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	queue.delivered(1)
	queue.setLive(nil)
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		names := make([]string, 0, len(left))
		for _, entry := range left {
			names = append(names, entry.Name())
		}
		t.Errorf("the state directory still holds %v", names)
	}
}

// One batch carries a bounded slice of the queue, so no single body can be too
// large for the backend to accept — and so a week offline is dozens of requests
// rather than two thousand.
func TestOneBatchCarriesABoundedSlice(t *testing.T) {
	queue := &buffer{path: filepath.Join(t.TempDir(), "buffer.json")}
	for i := range maxBucketsPerRequest + 5 {
		at := epoch.Add(time.Duration(i) * 5 * time.Minute)
		queue.add(bucketAt(at, i), at)
	}

	next := queue.next()
	if len(next) != maxBucketsPerRequest {
		t.Fatalf("one batch offers %d buckets, want %d", len(next), maxBucketsPerRequest)
	}
	if next[0].Counters.Requests != 0 {
		t.Errorf("the batch starts at bucket %d, want the oldest", next[0].Counters.Requests)
	}

	queue.delivered(len(next))
	if queue.pending() != 5 {
		t.Errorf("%d buckets left, want 5", queue.pending())
	}
	if queue.buckets[0].Counters.Requests != maxBucketsPerRequest {
		t.Errorf("the queue now starts at bucket %d, want %d",
			queue.buckets[0].Counters.Requests, maxBucketsPerRequest)
	}
}

// A queue file that cannot be read is a loss, and a loss has to be counted.
//
// The counter exists to keep a gap in the record from looking like a quiet period,
// and this is the path where that mattered most and did not happen: loadBuffer
// returned the error, NewReporter logged it and carried on, and the next save found
// nothing queued and no loss declared — so it deleted the file and nothing anywhere
// recorded that a backlog had existed.
//
// Counted as one, deliberately. The file did not parse, so how many buckets it held
// is exactly what cannot be known; one is not the true figure, it is the difference
// between "some counters were lost" and silence.
func TestAnUnreadableQueueCountsItsLoss(t *testing.T) {
	for name, content := range map[string]string{
		"truncated mid-object": `{"buckets":[{"window":`,
		"not JSON at all":      "\x00\x00\x00\x00",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "buffer.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			b, err := loadBuffer(path)
			if err == nil {
				t.Fatal("an unreadable queue was read without complaint")
			}
			if got := b.takeDropped(); got != 1 {
				t.Errorf("an unreadable queue reported %d losses, want 1 — a loss nobody "+
					"counted looks exactly like a quiet period", got)
			}
		})
	}

	// The live file beside it has always counted its own, and the two must not come
	// apart again: this is one function, and half of it counting was the bug.
	path := filepath.Join(t.TempDir(), "buffer.json")
	if err := os.WriteFile(path, []byte(`{"buckets":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(livePathFor(path), []byte(`{"live":`), 0o600); err != nil {
		t.Fatal(err)
	}

	b, err := loadBuffer(path)
	if err == nil {
		t.Fatal("an unreadable live bucket was read without complaint")
	}
	if got := b.takeDropped(); got != 1 {
		t.Errorf("an unreadable live bucket reported %d losses, want 1", got)
	}
}

// A torn queue is the very event the live file exists for: the crash that tore
// it is the one whose last minutes the live file holds. Returning on the queue's
// error left that file unread, and the first save then removed it — one loss
// counted while a bucket that had survived was erased uncounted.
func TestATornQueueDoesNotLoseTheLiveBucketBesideIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buffer.json")
	if err := os.WriteFile(path, []byte(`{"buckets":[{"window":`), 0o600); err != nil {
		t.Fatal(err)
	}
	live := bucket{Window: telemetry.Window{Start: epoch, End: epoch.Add(time.Minute)}}
	live.Counters.Requests = 7
	raw, err := json.Marshal(liveFile{Live: live})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(livePathFor(path), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	b, err := loadBuffer(path)
	if err == nil {
		t.Fatal("a torn queue was read without complaint")
	}
	if got := b.pending(); got != 1 {
		t.Fatalf("queued %d buckets, want the live one that survived the crash", got)
	}
	if got := b.buckets[0].Counters.Requests; got != 7 {
		t.Errorf("the surviving bucket carries %d requests, want 7", got)
	}
	if got := b.takeDropped(); got != 1 {
		t.Errorf("a torn queue beside a good live bucket reported %d losses, want 1", got)
	}
}
