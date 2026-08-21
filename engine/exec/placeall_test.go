package exec

import (
	"path/filepath"
	"testing"
	"time"
)

// A failure part-way through does not strand the caller.
//
// The workers consume from a channel the caller is still filling. A worker that
// gives up on the first error stops consuming, and if every worker does so the
// caller blocks on a send that nobody will ever receive - forever, at no CPU,
// with the work already on disk and nothing to indicate what is being waited
// for.
//
// That is what a build looked like from outside: `go mod download` completed,
// 450MB landed in the cache mount, and the process then sat at 0% for as long as
// it was allowed to. The capture that follows a step squashes layers, squashing
// links a tree, and linking a tree is this.
//
// *A producer that outlives its consumers.* The bound is what makes it possible:
// with an unbounded queue the send would never block and the bug would be a
// dropped error instead of a hang.
func TestAFailurePartWayThroughDoesNotStrandTheCaller(t *testing.T) {
	t.Parallel()

	// More jobs than any worker pool, so the caller is certainly still sending
	// when the first failure happens.
	files := make([]linkJob, 5000)
	for i := range files {
		// A source that does not exist: every job fails, immediately.
		files[i] = linkJob{
			from:      filepath.Join(t.TempDir(), "absent"),
			to:        filepath.Join(t.TempDir(), "out"),
			exclusive: true,
		}
	}

	done := make(chan error, 1)

	go func() { done <- placeAll(files) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("every entry failed and it reported success")
		}

	case <-time.After(20 * time.Second):
		t.Fatal("placeAll did not return: the caller is blocked sending to workers that stopped receiving")
	}
}

// The first failure is the one reported.
//
// Later ones are usually consequences, and a caller handed whichever error won a
// race is handed a different story every run.
func TestTheReportedFailureIsStable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	files := []linkJob{{from: filepath.Join(dir, "absent"), to: filepath.Join(dir, "out"), exclusive: true}}

	first := placeAll(files)
	if first == nil {
		t.Fatal("a missing source reported success")
	}

	for range 5 {
		if got := placeAll(files); got.Error() != first.Error() {
			t.Errorf("reported %v, then %v", first, got)
		}
	}
}
