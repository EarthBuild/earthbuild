package dockerutil

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/util/containerutil"
)

// pullThatFailsAtFirst is a frontend whose pull fails `fail` times and then
// works, and which reports the image as present afterwards.
//
// The interface is embedded rather than implemented: a method this test does
// not expect to be called has no body, so calling one panics and says which -
// louder than a zero value that quietly satisfies the caller.
type pullThatFailsAtFirst struct {
	containerutil.ContainerFrontend

	fail    int
	pulls   int
	tags    int
	removes int
}

// eof is the error CI produced, verbatim apart from the digest: a blob GET
// against buildkit's session registry, closed by the server mid-request.
const eof = `image pull: command failed: docker pull 127.0.0.1:34113/sess-x/pullping:img-0: ` +
	`failed to copy: httpReadSeeker: failed open: failed to do request: ` +
	`Get "https://127.0.0.1:34113/v2/sess-x/pullping/blobs/sha256:4305db": EOF: exit status 1`

func (f *pullThatFailsAtFirst) ImagePull(_ context.Context, _ ...string) error {
	f.pulls++
	if f.pulls <= f.fail {
		return errors.New(eof)
	}

	return nil
}

func (f *pullThatFailsAtFirst) ImageInfo(
	_ context.Context, refs ...string,
) (map[string]*containerutil.ImageInfo, error) {
	out := map[string]*containerutil.ImageInfo{}
	for _, r := range refs {
		out[r] = &containerutil.ImageInfo{ID: "sha256:present"}
	}

	return out, nil
}

func (f *pullThatFailsAtFirst) ImageTag(_ context.Context, _ ...containerutil.ImageTag) error {
	f.tags++

	return nil
}

// The pull is followed by an rmi of the local-registry ref, so the fake needs
// this too; it is the step that stops the loopback name lingering in the daemon.
func (f *pullThatFailsAtFirst) ImageRemove(_ context.Context, _ bool, _ ...string) error {
	f.removes++

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

	fe := &pullThatFailsAtFirst{fail: 1}

	err := dockerPullLocalImage(context.Background(), fe, "127.0.0.1:34113", "sess-x/pullping:img-0", "final:tag")
	if err != nil {
		t.Fatalf("a pull that fails once should succeed on retry, got: %v", err)
	}

	if fe.pulls != 2 {
		t.Errorf("pulled %d times, want 2 (one failure, one retry)", fe.pulls)
	}

	if fe.tags != 1 {
		t.Errorf("tagged %d times, want 1 - the retry must not skip the tag", fe.tags)
	}
}

// A pull that never works still fails, and says what went wrong.
//
// Retrying must not turn a broken frontend into a hang or a silent success, and
// the error the caller sees must still name the transport failure rather than
// "tried three times".
func TestAPullThatNeverWorksStillFails(t *testing.T) {
	t.Parallel()

	fe := &pullThatFailsAtFirst{fail: 99}

	err := dockerPullLocalImage(context.Background(), fe, "127.0.0.1:34113", "sess-x/pullping:img-0", "final:tag")
	if err == nil {
		t.Fatal("a pull that always fails must return an error")
	}

	if !strings.Contains(err.Error(), "EOF") {
		t.Errorf("the underlying transport error should survive the retry wrapper, got: %v", err)
	}

	if fe.pulls < 2 {
		t.Errorf("only tried %d times; a transient-looking failure should be retried", fe.pulls)
	}
}
