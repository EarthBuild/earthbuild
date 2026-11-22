package image

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// progressSuffix names the file beside a blob saying how far it has got.
const progressSuffix = ".progress"

// **A blob is read while it is being written**, so the reader has to be told how
// far the writer has reached: pages beyond it are zeros, and a cached zero is a
// zero kept (E683). The marker is a file rather than a message because the two
// sides are a host and a guest with a shared mount between them, and a small
// file rewritten by the host was read fresh at the writer's cadence in every
// run.
//
// **Staleness costs latency and never correctness.** A marker that lags means
// the reader waits; it can never mean the reader takes a byte that is not there.
// That asymmetry is why this is sound without any ordering guarantee from the
// filesystem.

// WriteProgress records how many of a blob's bytes are on disk.
func WriteProgress(blob string, n int64) error {
	return writeMarker(blob, strconv.FormatInt(n, 10))
}

// WriteProgressFailure records that the fetch gave up, and why.
//
// **Not a number**, so a reader cannot mistake it for progress. A fetch is a
// network and it can fail; a reader still waiting for a byte that is never
// coming is a build that hangs with nothing to say, which is the worst outcome
// available here.
func WriteProgressFailure(blob string, cause error) error {
	return writeMarker(blob, "!"+cause.Error())
}

func writeMarker(blob, body string) error {
	// Written whole and renamed into place: a reader that catches a half-written
	// number reads a smaller one, which would be harmless, but one that catches
	// a half-written "!" reads a failure that has not happened.
	at := blob + progressSuffix

	tmp := at + ".tmp"

	err := os.WriteFile(tmp, []byte(body), 0o600)
	if err != nil {
		return fmt.Errorf("record the blob's progress: %w", err)
	}

	err = os.Rename(tmp, at)
	if err != nil {
		return fmt.Errorf("record the blob's progress: %w", err)
	}

	return nil
}

// ReadProgress is how far a blob has got, or why it will get no further.
//
// A blob nothing has said anything about reads as zero rather than as an error:
// the fetch may not have started, which is the ordinary case for a reader that
// arrived first.
func ReadProgress(blob string) (int64, error, error) {
	b, err := os.ReadFile(blob + progressSuffix) //nolint:gosec // a path this engine derived
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil
		}

		return 0, nil, fmt.Errorf("read the blob's progress: %w", err)
	}

	body := strings.TrimSpace(string(b))

	if after, ok := strings.CutPrefix(body, "!"); ok {
		return 0, errors.New(after), nil
	}

	n, err := strconv.ParseInt(body, 10, 64)
	if err != nil {
		// **Deliberately not an error.** A marker that is neither a number nor
		// a failure is one caught mid-write, and the reader's answer to "I do
		// not know yet" is to wait - which is the same answer it gives to a
		// marker that is not there. Reporting a parse failure would turn a
		// millisecond of raciness into a failed build.
		return 0, nil, nil //nolint:nilerr // see above: unknown is "wait", not "fail"
	}

	return n, nil, nil
}

// awaitPoll is how often a waiting reader looks again.
//
// **Matched to how fresh the answer can be, not to how fast one would like it.**
// A marker written by the host is seen by the guest about 460ms later on
// average - measured both ways, rewritten in place and renamed into position,
// and it makes no difference (E688). Polling every 2ms therefore asked the
// shared filesystem two hundred times for each answer that could have changed,
// while the unpack it was overlapping wanted the same channel.
const awaitPoll = 25 * time.Millisecond

// AwaitProgress waits until a blob has more than `have` bytes on disk.
//
// Three outcomes and no fourth: it grew, the fetch gave up and said why, or
// nothing happened for `patience`. **A reader with no deadline is a build that
// hangs with nothing to say**, which is a thing this engine has produced before
// and taken some trouble to diagnose (E673) - so the wait ends, and the message
// names the blob and how far it had got.
func AwaitProgress(blob string, have int64, patience time.Duration) (int64, error) {
	deadline := time.Now().Add(patience)

	for {
		n, failed, err := ReadProgress(blob)
		if err != nil {
			return 0, err
		}

		if failed != nil {
			return 0, fmt.Errorf("the fetch of %s failed: %w", blob, failed)
		}

		if n > have {
			return n, nil
		}

		if time.Now().After(deadline) {
			return 0, fmt.Errorf("waited %s for %s to pass %d bytes and it did not;"+
				" the fetch has neither progressed nor reported a failure",
				patience, blob, have)
		}

		time.Sleep(awaitPoll)
	}
}
