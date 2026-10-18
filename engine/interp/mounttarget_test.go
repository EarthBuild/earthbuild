package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A relative mount target means the step's working directory, not the root.
//
// `--mount=target=.` is how a Dockerfile binds its context at the directory the
// step runs in. Anchored at `/` instead, the view is bound **over the root
// filesystem** - and a step that binds a source tree at `/` then cannot find
// `/bin/sh`, which is what it reports:
//
//	fork/exec /bin/sh: no such file or directory
//	the image does not have this program
//
// buildkit's own Dockerfile does this in the stage that computes its version
// (`WORKDIR /src`, then `RUN --mount=target=. ...`), so the failure is not a
// corner: it is the first thing a real Dockerfile with a bound view does.
func TestARelativeMountTargetIsUnderTheWorkingDirectory(t *testing.T) {
	t.Parallel()

	dir := contextHolding(t, "data/f", "hello")

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WORKDIR /src
    RUN --mount=type=bind,source=data,target=. ls
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			if !m.View {
				continue
			}

			if m.Target == "/" {
				t.Fatal("the view is bound at /, over the whole filesystem:" +
					" a step that does this cannot find /bin/sh")
			}

			if m.Target != "/src" {
				t.Errorf("the view is bound at %q, not at the working directory", m.Target)
			}
		}
	}
}

// An absolute target is left where it was written.
func TestAnAbsoluteMountTargetIsNotMoved(t *testing.T) {
	t.Parallel()

	dir := contextHolding(t, "data/f", "hello")

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WORKDIR /src
    RUN --mount=type=bind,source=data,target=/elsewhere ls
`, testMain, interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	var seen string

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			if m.View {
				seen = m.Target
			}
		}
	}

	if seen != "/elsewhere" {
		t.Errorf("an absolute target became %q", seen)
	}
}

// And the same rule for a cache mount, which had it already via CACHE.
func TestARelativeCacheTargetIsUnderTheWorkingDirectory(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WORKDIR /src
    RUN --mount=type=cache,target=out make
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	text := describe(p.Graph.Nodes())
	if strings.Contains(text, "/out") && !strings.Contains(text, "/src/out") {
		t.Errorf("a relative cache target was anchored at the root:\n%s", text)
	}
}

// CACHE with an absolute path is not moved under the working directory either.
//
// The same rule, in the place that had it first. `filepath.Join("/", workdir,
// "/x")` is `/workdir/x`, so the join alone was wrong for an absolute target -
// it just never showed, because a CACHE under a WORKDIR is the ordinary case
// and an absolute one is the rarer.
func TestAnAbsoluteCacheTargetIsNotMoved(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WORKDIR /src
    CACHE /var/cache/apk
    RUN apk add curl
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			if m.Target != "" && m.Target != "/var/cache/apk" {
				t.Errorf("the cache is mounted at %q, not where it was written", m.Target)
			}
		}
	}
}
