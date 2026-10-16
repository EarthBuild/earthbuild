package core_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

// TestShowReport prints a real report, so the diagnostic's shape is reviewable
// rather than only asserted about.
func TestShowReport(t *testing.T) {
	t.Parallel()

	before := obs(map[string]byte{
		testRustSource: 1, testLockPath: 1, "vendor/x/lib.rs": 1, testGitHead: 1,
	})
	after := obs(map[string]byte{
		testRustSource: 2, testLockPath: 2, "vendor/x/lib.rs": 2, testGitHead: 2,
	})

	fmt.Print("\n" + core.Report(core.Diverge(
		&core.Record{Steps: []core.StepRecord{rec("s", before, 10)}},
		&core.Record{Steps: []core.StepRecord{rec("s", after, 11)}},
	)))
}

// TestShowRefusal prints a real refusal, so the diagnostic is reviewable rather
// than only asserted about.
func TestShowRefusal(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{Root: &ir.Node{
		Op:   ir.Op{Kind: ir.OpHost, Args: []string{"./deploy.sh"}},
		Meta: ir.Meta{Source: testSite},
	}}

	s := newSched(newMemCache(), allBlobs{}, &sim.Executor{Seed: 1})
	s.Capabilities = m1Caps()

	_, err := s.Run(context.Background(), g)
	if err != nil {
		fmt.Printf("\nError: %v\n", err)
	}
}
