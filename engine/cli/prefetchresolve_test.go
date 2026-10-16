package cli

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// slowResolver answers after a delay and records how many answers were in
// flight at once, so a test can ask whether resolution overlapped rather than
// timing it and hoping.
type slowResolver struct {
	delay time.Duration
	mu    sync.Mutex
	live  int
	most  int
	calls atomic.Int64
	fail  map[string]bool
}

func (s *slowResolver) resolve(ref, _ string) (string, error) {
	s.calls.Add(1)

	s.mu.Lock()
	s.live++

	if s.live > s.most {
		s.most = s.live
	}

	s.mu.Unlock()

	time.Sleep(s.delay)

	s.mu.Lock()
	s.live--
	s.mu.Unlock()

	if s.fail[ref] {
		return "", errors.New("unreachable")
	}

	return ref + "@sha256:deadbeef", nil
}

func (s *slowResolver) peak() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.most
}

// TestEveryImageAnEarthfileNamesIsResolvedAtOnce.
//
// **A warm build is its image resolutions and nothing else.** Measured: five
// cached steps on one image cost 0.22s, of which 0.208s is `plan` and 0.002s is
// the schedule. Two distinct images cost 0.336s, which is the sum of the two -
// they are resolved one after the other, on the interpreter's walk, and nothing
// about resolving one depends on the other.
//
// The memo in `Plan.pin` is still what makes it once per reference (I17); this
// is about when those lookups happen, not how many.
func TestEveryImageAnEarthfileNamesIsResolvedAtOnce(t *testing.T) {
	t.Parallel()

	slow := &slowResolver{delay: 60 * time.Millisecond}
	r := newPrefetchResolver(slow.resolve)

	refs := []string{"python:3.13-slim", "alpine:3.20", "golang:1.26-alpine"}

	started := time.Now()
	r.start(refs, "linux/arm64")

	for _, ref := range refs {
		got, err := r.Resolve(ref, "linux/arm64")
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}

		if got != ref+"@sha256:deadbeef" {
			t.Errorf("%s resolved to %q", ref, got)
		}
	}

	took := time.Since(started)

	if peak := slow.peak(); peak < len(refs) {
		t.Errorf("at most %d resolutions were in flight at once, want %d:"+
			"\n  they are independent round trips and a build waits for all of them",
			peak, len(refs))
	}

	// Generous, because a slow machine must not fail this - the assertion that
	// matters is the concurrency above. This only catches a prefetch that
	// silently became serial.
	if took > 3*slow.delay {
		t.Errorf("resolving %d references took %v, which is the serial cost",
			len(refs), took)
	}
}

// TestAReferenceNobodyPrefetchedIsStillResolved: the prefetch reads the
// Earthfile's text, and a build can name an image the text does not - through an
// ARG, or from an Earthfile the scan never saw. Those have to resolve inline,
// exactly as they did before this existed.
func TestAReferenceNobodyPrefetchedIsStillResolved(t *testing.T) {
	t.Parallel()

	slow := &slowResolver{delay: time.Millisecond}
	r := newPrefetchResolver(slow.resolve)

	r.start([]string{"alpine:3.20"}, "linux/arm64")

	got, err := r.Resolve("python:3.13-slim", "linux/arm64")
	if err != nil {
		t.Fatal(err)
	}

	if got != "python:3.13-slim@sha256:deadbeef" {
		t.Errorf("an unprefetched reference resolved to %q", got)
	}
}

// TestAPrefetchedReferenceIsAskedForOnce: a prefetch that also resolved inline
// would double every build's round trips, which is the opposite of the point.
func TestAPrefetchedReferenceIsAskedForOnce(t *testing.T) {
	t.Parallel()

	slow := &slowResolver{delay: time.Millisecond}
	r := newPrefetchResolver(slow.resolve)

	r.start([]string{"alpine:3.20"}, "linux/arm64")

	for range 3 {
		_, err := r.Resolve("alpine:3.20", "linux/arm64")
		if err != nil {
			t.Fatal(err)
		}
	}

	if n := slow.calls.Load(); n != 1 {
		t.Errorf("one reference cost %d round trips", n)
	}
}

// TestAFailedPrefetchIsReportedToTheCaller: an unreachable registry leaves the
// reference as written, and the interpreter needs the error to say so. Swallowing
// it here would turn a reported unpinned build into a silent one.
func TestAFailedPrefetchIsReportedToTheCaller(t *testing.T) {
	t.Parallel()

	slow := &slowResolver{delay: time.Millisecond, fail: map[string]bool{"alpine:3.20": true}}
	r := newPrefetchResolver(slow.resolve)

	r.start([]string{"alpine:3.20"}, "linux/arm64")

	_, err := r.Resolve("alpine:3.20", "linux/arm64")
	if err == nil {
		t.Error("a registry that could not be reached was reported as a success")
	}
}

// TestOnePlatformSpeltTwoWaysIsOneResolution.
//
// **The prefetch and the interpreter do not spell the platform the same way.**
// The prefetch is started with the build's platform; the interpreter calls back
// with the step's, which is empty when it wants the default. Keyed on the text
// as written, one image went under two keys and was resolved twice - a warm
// two-image build went from 0.37s to 0.44s, which is a prefetch making things
// worse.
//
//nolint:paralleltest // resolveFor consults the machine's default platform
func TestOnePlatformSpeltTwoWaysIsOneResolution(t *testing.T) {
	slow := &slowResolver{delay: time.Millisecond}
	r := newPrefetchResolver(slow.resolve)

	// Started for the machine's default, spelt out.
	r.start([]string{"alpine:3.20"}, resolveFor(""))

	// Asked for by a step that did not name one, which means the same thing.
	_, err := r.Resolve("alpine:3.20", "")
	if err != nil {
		t.Fatal(err)
	}

	if n := slow.calls.Load(); n != 1 {
		t.Errorf("one image on one platform cost %d round trips", n)
	}
}
