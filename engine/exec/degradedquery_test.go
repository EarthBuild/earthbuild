package exec_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// Asking whether a build was unbounded does not boot a sandbox.
//
// `Degraded` reads the guest's answer, and the guest is behind a lazily-started
// client - so the obvious implementation calls `client()`, which starts one.
// A build whose every step was cached or local is entitled never to boot a
// sandbox at all, and the first version of this method took that away by
// asking the question: on a local-only build it dereferenced a nil sandbox and
// panicked.
//
// **A query with a side effect is not a query.** Asserted on the sandbox's own
// boot counter, because "did not panic" would pass against a version that
// booted one and then answered correctly.
func TestAskingAboutLimitsDoesNotBootASandbox(t *testing.T) {
	t.Parallel()

	sb := &countingSandbox{confines: true, store: t.TempDir()}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	if got := e.Degraded(); got != "" {
		t.Errorf("a build that ran nothing reported a degradation: %q", got)
	}

	if boots, _ := sb.counts(); boots != 0 {
		t.Errorf("asking whether limits were applied booted %d sandboxes", boots)
	}
}
