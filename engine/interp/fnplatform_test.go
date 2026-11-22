package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A FUNCTION's platform arguments are its caller's.
//
// A function is inlined into the target that calls it, so `ARG TARGETARCH`
// inside one names what that target builds. Its state was made fresh with no
// platform, which read as the native one: under `FROM --platform=linux/amd64`
// on an arm64 machine, the target said amd64 and its function said arm64.
// Stock earth says amd64 in both. A function that downloads a per-arch binary
// fetched the wrong one, and the checksum it carried passed.
//
// The control is the same ARG in the target itself, which was already right.
func TestAFunctionBuildsForItsCallersPlatform(t *testing.T) {
	t.Parallel()

	src := versioned + `
SHOW:
    FUNCTION
    ARG TARGETARCH
    RUN echo "fn=[$TARGETARCH]"

viafn:
    FROM --platform=linux/amd64 alpine:3.22
    ARG TARGETARCH
    RUN echo "target=[$TARGETARCH]"
    DO +SHOW
`

	p, err := interp.Build(src, "viafn", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatal(err)
	}

	nodes := p.Graph.Nodes()
	runs := make([]string, 0, len(nodes))

	for _, n := range nodes {
		runs = append(runs, strings.Join(n.Op.Args, " "))
	}

	all := strings.Join(runs, "\n")

	if !strings.Contains(all, "target=[amd64]") {
		t.Fatalf("the control failed - the target itself did not read amd64:\n%s", all)
	}

	if !strings.Contains(all, "fn=[amd64]") {
		t.Errorf("a FUNCTION called under FROM --platform=linux/amd64 did not read amd64:\n%s", all)
	}
}
