package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `BUILD --platform=linux/amd64 +target` builds it for that platform.
//
// The platform reaches every step of the target, and therefore its key: the
// same command on two architectures is two steps producing two filesystems.
func TestBuildPlatformReachesTheTarget(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    BUILD --platform=linux/amd64 +other

other:
    FROM alpine:3.22
    RUN make
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var found bool

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "make") {
			found = true

			if n.Platform.OS != testOS || n.Platform.Arch != testArch {
				t.Errorf("the step runs on %+v, want linux/amd64", n.Platform)
			}
		}
	}

	if !found {
		t.Fatal("the target was not built")
	}
}

// The same target on two platforms is two builds.
func TestTwoPlatformsAreTwoBuilds(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    BUILD --platform=linux/amd64 +other
    BUILD --platform=linux/arm64 +other

other:
    FROM alpine:3.22
    RUN make
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "make") {
			seen[n.Platform.OS+"/"+n.Platform.Arch] = true
		}
	}

	if len(seen) != 2 {
		t.Errorf("two platforms produced %d builds: %v", len(seen), seen)
	}
}

// A malformed platform is refused rather than parsed into something arbitrary.
func TestMalformedPlatformsAreRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    BUILD --platform=nonsense/ +other

other:
    FROM alpine
    RUN make
`, testMain)
	if err == nil {
		t.Fatal("a malformed platform was accepted")
	}
}

// `FROM --platform` does the same for a base.
func TestFromPlatform(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM --platform=linux/arm64 +other
    RUN main-step

other:
    FROM alpine:3.22
    RUN other-step
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "other-step") && n.Platform.Arch != "arm64" {
			t.Errorf("the base's step runs on %+v, want arm64", n.Platform)
		}
	}
}

// `COPY --build-arg` passes an argument to the target the artifact comes from.
func TestCopyBuildArg(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    COPY --build-arg tag=v2 +other/out /dst/

other:
    FROM alpine:3.22
    ARG tag=own
    RUN echo $tag
    SAVE ARTIFACT /out
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "echo v2") {
		t.Errorf("the argument did not reach the target:\n%s", got)
	}
}

// `SAVE IMAGE --push` records that the image is meant to be pushed. Pushing
// happens when the *invocation* asks for it, so the flag is a declaration
// rather than a side effect, and a build that does not push is not ignoring it.
func TestSaveImagePushIsRecorded(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    SAVE IMAGE --push myorg/tool:latest
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Images) != 1 {
		t.Fatalf("collected %d images, want 1", len(p.Images))
	}

	if !p.Images[0].Push {
		t.Error("the image is not marked for pushing")
	}
}

var _ = ir.Platform{}
