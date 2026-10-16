package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step no worker can run says what it asked for and what there was.
//
// `BUILD --platform=linux/amd64` on a machine whose sandbox runs linux/arm64 is
// a real refusal - this engine has no emulation, and building the wrong
// architecture silently would be worse than saying no (green paper I10, and the
// case that invariant names). But what it said was:
//
//	schedule FROM alpine:3.24.1 (image): no eligible worker
//
// which names neither the platform the step asked for, nor the platforms this
// machine has, nor anything to do about it. Two of this repository's own
// targets end there - `+for-linux` and `+smoke-test` - and the message sends the
// reader looking for a broken worker rather than a cross-platform build (E68).
func TestAStepNoWorkerCanRunSaysWhy(t *testing.T) {
	t.Parallel()

	// A build for amd64 on a machine with only an arm64 worker.
	img := &ir.Node{
		Op:       ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}},
		Platform: ir.Platform{OS: testOS, Arch: testArch2},
		Meta:     ir.Meta{Source: at(4)},
	}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: testLocal, Platform: ir.Platform{OS: testOS, Arch: testArch}}},
		Executor: &squashingExec{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: img})
	if err == nil {
		t.Fatal("a step for a platform no worker has was scheduled anyway")
	}

	for _, want := range []string{
		"linux/amd64", // what the step asked for
		"linux/arm64", // what this machine has
		at(4),         // where it was asked
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%v", want, err)
		}
	}
}

// A step nothing can run for a reason other than the platform still says so.
//
// The message must not claim the platform is the problem when it is not: a
// worker excluded for any other reason produces the same "nothing was eligible"
// with a different explanation, and inventing a platform mismatch would send
// the reader somewhere there is nothing to find.
func TestAStepWithNoWorkersAtAllIsNotBlamedOnThePlatform(t *testing.T) {
	t.Parallel()

	img := &ir.Node{
		Op:       ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}},
		Platform: ir.Platform{OS: testOS, Arch: testArch},
		Meta:     ir.Meta{Source: at(4)},
	}

	s := &core.Scheduler{Workers: nil, Executor: &squashingExec{}}

	_, err := s.Run(context.Background(), &ir.Graph{Root: img})
	if err == nil {
		t.Fatal("a step was scheduled with no workers at all")
	}

	if strings.Contains(err.Error(), "linux/arm64 and this build has") {
		t.Errorf("a build with no workers was told its platform was wrong:\n%v", err)
	}

	if !strings.Contains(err.Error(), "no workers") {
		t.Errorf("the refusal does not say there were no workers:\n%v", err)
	}
}
