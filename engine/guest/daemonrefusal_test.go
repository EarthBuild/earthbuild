package guest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

// A daemon asked for badly is refused by the guest that would have run it.
//
// The check is worth nothing unless something calls it, and *a mechanism that is
// not running and one that found nothing produce the same output* is the failure
// class this project has recorded most often. So the assertion goes through the
// client: a step is sent asking for a daemon it did not describe, and the
// refusal has to come back.
func TestAStepAskingForADaemonBadlyIsRefused(t *testing.T) {
	t.Parallel()

	c := pair(t, &sim.Materialiser{})

	h, err := c.Materialise(context.Background(), []ir.NodeID{{1}})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = h.Release() })

	_, err = c.RunStep(context.Background(), h, guest.Step{
		Argv:   []string{testTrue},
		Daemon: &guest.Daemon{}, // asked for, described nowhere
	}, nil)

	if err == nil {
		t.Fatal("a step asking for a daemon it did not describe ran anyway")
	}

	if !strings.Contains(err.Error(), "root") {
		t.Errorf("the refusal did not reach the caller intact: %v", err)
	}
}
