package dockerutil

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// pullThatFailsAtFirst is a puller whose pull fails `fail` times and then
// works.
type pullThatFailsAtFirst struct {
	fail  int
	pulls int
}

// eof is the error CI produced, verbatim apart from the digest: a blob GET
// against buildkit's session registry, closed by the server mid-request.
const eof = `image pull: command failed: docker pull 127.0.0.1:34113/sess-x/pullping:img-0: ` +
	`failed to copy: httpReadSeeker: failed open: failed to do request: ` +
	`Get "https://127.0.0.1:34113/v2/sess-x/pullping/blobs/sha256:4305db": EOF: exit status 1`

func (f *pullThatFailsAtFirst) PullImage(_ context.Context, _ ...string) error {
	f.pulls++
	if f.pulls <= f.fail {
		return errors.New(eof)
	}

	return nil
}

// A pull that fails once succeeds on the retry.
//
// **The failure is transient by construction.** The image is one buildkitd has
// just published to a session-scoped registry on loopback, so it exists; a bare
// `EOF` from that server is a closed connection, not an answer. Measured at
// roughly one job-run in a hundred, which is frequent enough to fail a build a
// few times a month and rare enough that nobody can reproduce it on demand.
func TestAPullThatFailsOnceIsRetried(t *testing.T) {
	t.Parallel()

	p := &pullThatFailsAtFirst{fail: 1}

	err := pullWithRetry(context.Background(), p, "127.0.0.1:34113/sess-x/pullping:img-0")
	if err != nil {
		t.Fatalf("a pull that fails once should succeed on retry, got: %v", err)
	}

	if p.pulls != 2 {
		t.Errorf("pulled %d times, want 2 (one failure, one retry)", p.pulls)
	}
}

// A pull that never works still fails, and says what went wrong.
//
// Retrying must not turn a broken engine into a hang or a silent success, and
// the error the caller sees must still name the transport failure rather than
// "tried three times".
func TestAPullThatNeverWorksStillFails(t *testing.T) {
	t.Parallel()

	p := &pullThatFailsAtFirst{fail: 99}

	err := pullWithRetry(context.Background(), p, "127.0.0.1:34113/sess-x/pullping:img-0")
	if err == nil {
		t.Fatal("a pull that always fails must return an error")
	}

	if !strings.Contains(err.Error(), "EOF") {
		t.Errorf("the underlying transport error should survive the retry wrapper, got: %v", err)
	}

	if p.pulls != pullAttempts {
		t.Errorf("tried %d times, want %d", p.pulls, pullAttempts)
	}
}
