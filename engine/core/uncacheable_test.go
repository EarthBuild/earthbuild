package core_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A build says which steps it refused to cache, and why.
//
// This engine will not cache a step carrying a cache mount: what it produces may
// depend on what was in the mount, and no key bounds that (I3). BuildKit and
// Earthly both cache such steps with the mount excluded from the key, so this is
// **stricter than either**, and the cost falls on exactly the slowest step in a
// real build - `mvn package`, `npm install`, `cargo build`.
//
// That is the decision, taken deliberately. What was wrong was that it happened
// in silence: the step rebuilt every time and the build said nothing, so the only
// way to discover it was to instrument the scheduler - which is how it *was*
// discovered, over three experiments (E224, E225, E226).
//
// A refusal that costs a minute a build should be legible in the build that pays
// it. The reason is derived from the operation rather than passed down, so a new
// way of becoming uncacheable is named the first time it happens rather than
// reported as "no cache" (E228).
func TestABuildNamesTheStepsItRefusedToCache(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		op   ir.Op
		want string
	}{
		{
			name: "a cache mount",
			op: ir.Op{
				Kind: ir.OpExec, Args: []string{"make"}, NoCache: true,
				Mounts: []ir.Mount{{ID: "m", Target: "/root/.m2"}},
			},
			want: "a cache mount",
		},
		{
			name: "a secret",
			op: ir.Op{
				Kind: ir.OpExec, Args: []string{"make"}, NoCache: true,
				SecretEnv: []string{"TOKEN"},
			},
			want: "a secret",
		},
		{
			name: "--no-cache",
			op:   ir.Op{Kind: ir.OpExec, Args: []string{"make"}, NoCache: true},
			want: "--no-cache",
		},
		{
			name: "WITH DOCKER",
			op:   ir.Op{Kind: ir.OpExec, Args: []string{"make"}, Docker: true},
			want: "a docker daemon",
		},
	} {
		s := newSched(newMemCache(), allBlobs{}, &observingExec{})
		s.Profiles = memProfiles{}
		s.Views = fixedView{fakeBase{}}

		base := &ir.Node{
			Op:       ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}},
			Platform: amd64,
		}

		op := tc.op
		n := &ir.Node{
			Op: op, Platform: amd64, Inputs: []*ir.Node{base},
			Meta: ir.Meta{Source: at(11)},
		}

		_, err := s.Run(context.Background(), &ir.Graph{Root: n})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if s.Stats.Uncacheable != 1 {
			t.Errorf("%s: %d steps reported uncacheable, want 1"+
				"\n  a step that rebuilds every build should say so in the"+
				" build that pays for it", tc.name, s.Stats.Uncacheable)

			continue
		}

		if !slices.ContainsFunc(s.Stats.UncacheableAt, func(w string) bool {
			return strings.Contains(w, tc.want) && strings.Contains(w, at(11))
		}) {
			t.Errorf("%s: reported %v, want a line naming %q and %q",
				tc.name, s.Stats.UncacheableAt, tc.want, at(11))
		}
	}
}
