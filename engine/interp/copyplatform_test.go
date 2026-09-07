package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `COPY --platform=linux/amd64 +target/artifact dest` builds that target for
// that platform and takes the artifact from it.
//
// FROM and BUILD already carry a platform into a referenced target; COPY
// refusing it was the same inconsistency `--pass-args` was. The flag exists
// because a build often needs one artifact from an architecture other than the
// one it is running on - a cross-compiled binary being the ordinary case.
func TestCopyPlatformBuildsTheTargetThere(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
producer:
    FROM alpine:3.22
    RUN compile > /out.bin
    SAVE ARTIFACT /out.bin

main:
    FROM alpine:3.22
    COPY --platform=linux/amd64 +producer/out.bin /dst/
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var producer *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && len(n.Op.Args) > 0 &&
			n.Meta.Description == "RUN compile > /out.bin" {
			producer = n
		}
	}

	if producer == nil {
		t.Fatalf("the producing step is not in the graph:\n%s", describe(p.Graph.Nodes()))
	}

	if producer.Platform.OS != testOS || producer.Platform.Arch != testArch {
		t.Errorf("the producer runs on %+v, want linux/amd64", producer.Platform)
	}
}

// Without the flag the producer runs where the caller does.
func TestCopyWithoutPlatformStaysWhereItIs(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
producer:
    FROM alpine:3.22
    RUN compile > /out.bin
    SAVE ARTIFACT /out.bin

main:
    FROM --platform=linux/arm64 alpine:3.22
    COPY +producer/out.bin /dst/
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == "RUN compile > /out.bin" && n.Platform.Arch == testArch {
			t.Error("the producer was built for a platform nobody asked for")
		}
	}
}

// A platform that is not one is refused rather than pulled.
func TestCopyPlatformMustBeAPlatform(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
producer:
    FROM alpine:3.22
    SAVE ARTIFACT /out.bin

main:
    FROM alpine:3.22
    COPY --platform=nonsense/ +producer/out.bin /dst/
`, testMain)
	if err == nil {
		t.Fatal("a malformed platform was accepted")
	}
}
