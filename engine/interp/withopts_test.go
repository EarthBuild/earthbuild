package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

const loadOptsSrc = `
app:
    FROM alpine:3.22
    ARG flavour=plain
    RUN build-$flavour
    SAVE IMAGE app:latest

main:
    FROM alpine:3.22
    ARG flavour=fancy
`

// The options that carry something into the loaded target behave as they do
// everywhere else.
//
// `--build-arg`, `--pass-args` and `--platform` are the same three FROM, BUILD
// and COPY already take, and a construct that spelled them differently would be
// a construct people have to learn twice.
func TestLoadCarriesArgumentsAndPlatform(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		opts   string
		want   string // a step description that must be in the graph
		absent string
	}{
		{
			"--build-arg", "--build-arg flavour=custom --load +app",
			"RUN build-custom", "RUN build-plain",
		},
		{
			"--pass-args", "--pass-args --load +app",
			"RUN build-fancy", "RUN build-plain",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+loadOptsSrc+`
    WITH DOCKER `+tc.opts+`
        RUN docker images
    END
`, testMain)
			if err != nil {
				t.Fatal(err)
			}

			text := describe(p.Graph.Nodes())
			if !strings.Contains(text, tc.want) {
				t.Errorf("%q is not in the graph:\n%s", tc.want, text)
			}

			if strings.Contains(text, tc.absent) {
				t.Errorf("%q is in the graph, so the option did not reach the target", tc.absent)
			}
		})
	}
}

// `--platform` builds the loaded target somewhere else.
func TestLoadPlatformBuildsTheTargetThere(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
app:
    FROM alpine:3.22
    RUN compile
    SAVE IMAGE app:latest

main:
    FROM alpine:3.22
    WITH DOCKER --platform=linux/amd64 --load +app
        RUN docker images
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == "RUN compile" {
			if n.Platform.Arch != testArch {
				t.Errorf("the loaded target runs on %+v, want linux/amd64", n.Platform)
			}

			return
		}
	}

	t.Errorf("the loaded target is not in the graph:\n%s", describe(p.Graph.Nodes()))
}

// `--cache-id` is accepted, and what it means is written into the step.
//
// **This test used to assert the opposite**, and the behaviour changed by
// decision rather than by drift: sharing the inner daemon's storage is what
// people reach for most of the time, and the isolation that a test of this
// engine's own cache behaviour needs is what a block with no `--cache-id`
// already gives (E354).
//
// It is not accepted-and-ignored, which is what the old refusal existed to
// prevent: the id reaches the key, and the steps of the block are marked
// uncacheable, because a daemon holding what an earlier build left is not a
// function of this step's inputs (I3).
func TestCacheIDIsAcceptedAndMeansSomething(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --cache-id=layers
        RUN docker images
    END
`, testMain)
	if err != nil {
		t.Fatalf("--cache-id was refused: %v", err)
	}

	var seen int

	for _, n := range p.Graph.Nodes() {
		if !n.Op.Docker {
			continue
		}

		seen++

		if n.Op.DockerCache != "layers" {
			t.Errorf("a step of the block names cache %q (%s)",
				n.Op.DockerCache, n.Meta.Description)
		}

		if !n.Op.NoCache {
			t.Errorf("a step sharing a daemon cache is cacheable (%s)",
				n.Meta.Description)
		}
	}

	if seen == 0 {
		t.Fatal("no step of the block was given a daemon")
	}
}

// A platform that is not one is refused rather than carried.
func TestLoadPlatformMustBeAPlatform(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
app:
    FROM alpine:3.22
    SAVE IMAGE app:latest

main:
    FROM alpine:3.22
    WITH DOCKER --platform=nonsense/ --load +app
        RUN docker images
    END
`, testMain)
	if err == nil {
		t.Fatal("a malformed platform was accepted")
	}
}

var _ = ir.OpExec
