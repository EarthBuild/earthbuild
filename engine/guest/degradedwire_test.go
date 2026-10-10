package guest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step that ran unbounded says so, to the host, while the build is running.
//
// The guest already records why limits could not be applied - I11 says degrade
// if you must but say so, and the reason was recorded faithfully. It was then
// printed **after `Serve` returns**: at guest shutdown, on stderr, when the
// build is over and nobody is reading. A build whose every step ran without the
// memory ceiling it asked for mentioned it, if at all, too late to matter and
// somewhere nobody looks.
//
// "Say so" means to the party that asked. The host asks the guest for
// observations, capabilities and versions over the protocol; the reason a limit
// was not applied travels the same way.
//
// The fixture is free off Linux: `cgroup_other.go` degrades every time, which
// is the honest behaviour there and a deterministic test everywhere else.
func TestAnUnboundedStepTellsTheHostWhy(t *testing.T) {
	// Runs a step, so it needs the namespace a step is confined in - and
	// inside one on Linux this becomes the *real* case rather than the
	// platform stub: the cgroup root is there and cannot be written.
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Parallel()

	root := stepRoot(t)

	c := pairWith(t, &guest.Server{
		Mat:        &fixedRootMat{root: root},
		Unconfined: true,
		Limits:     guest.Limits{MemoryMax: 16 << 20},
	})

	h, err := c.Materialise(context.Background(), []ir.NodeID{{1}})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = h.Release() }()

	_, _, err = c.ExecStream(context.Background(), h, []string{sh(t), "-c", testTrue}, nil, nil)
	if err != nil {
		t.Fatalf("a step that could not be limited did not run: %v", err)
	}

	reason := c.Degraded()
	if reason == "" {
		t.Fatal("the step ran without the memory ceiling it was given and the" +
			" host was not told:\n  I11 is degrade-and-say-so, and saying so at" +
			" shutdown on the guest's stderr is not saying so")
	}

	// The reason, not just the fact: "limits not applied" leaves a reader to
	// guess between an unmounted cgroup filesystem, a delegated subtree they
	// cannot write, and a platform that has none.
	if !strings.Contains(reason, "cgroup") && !strings.Contains(reason, "platform") {
		t.Errorf("the reason names neither a cause nor a remedy: %q", reason)
	}
}

// A step with no limits asked for is not degraded.
//
// The companion. "Report a degradation" is satisfiable by reporting one always,
// and then every build carries a warning about a ceiling nobody wanted - which
// is how a warning stops being read.
func TestAStepWithNoLimitsIsNotDegraded(t *testing.T) {
	// Runs a step, so it needs the namespace a step is confined in - and
	// inside one on Linux this becomes the *real* case rather than the
	// platform stub: the cgroup root is there and cannot be written.
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Parallel()

	root := stepRoot(t)

	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), []ir.NodeID{{1}})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = h.Release() }()

	_, _, err = c.ExecStream(context.Background(), h, []string{sh(t), "-c", testTrue}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if reason := c.Degraded(); reason != "" {
		t.Errorf("a step that asked for no limits reported a degradation: %q", reason)
	}
}
