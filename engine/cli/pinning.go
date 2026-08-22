package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

// imageResolver supplies Θ to the interpreter (green paper §3.4d).
//
// One round trip per distinct reference, for the manifest only: no blob is read
// and nothing is written to a store, so pinning costs the same whether the image
// is already cached or has never been seen.
//
// **A registry that cannot be reached does not fail the build.** The resolver
// returns the error, the interpreter leaves the reference as written, and the
// build proceeds unpinned - which is what it did before this existed. Refusing
// instead would make an offline machine unable to build something it has every
// layer of, and that is a worse failure than a key that is coarser than it
// should be. What it must not do is claim to have pinned: an unresolved
// reference is absent from the record below.
func (g *engine) imageResolver(ctx context.Context) interp.ResolveImage {
	// Where each registry issues tokens, remembered beside the images it serves:
	// both are per machine rather than per project, and both are the same for
	// every build on it. An error here costs the round trip it would have saved
	// and nothing else (E535).
	challenges, err := imageCacheDir()
	if err != nil {
		challenges = ""
	}

	return func(ref, platform string) (string, error) {
		to, err := image.Resolve(ctx, ref, image.Options{
			Platform: resolveFor(platform), Challenges: challenges,
		})
		if err != nil {
			// Said once, where it can be acted on. An unpinned build is not a
			// failed build, but it is a build whose keys are coarser than they
			// look, and silence here is indistinguishable from a build that had
			// nothing to pin.
			fmt.Fprintf(os.Stderr, "note: %s was not pinned: %v\n"+
				"  the build uses the reference as written, so a tag that moves"+
				" is not a new key\n", ref, err)
		}

		return to, err
	}
}

// recordPinning says what each mutable reference resolved to.
//
// Provenance, not input (B.3): a build that cannot say which image it used
// cannot be compared with the one before it, and comparing them is how a moved
// tag is told from a changed Earthfile (B.4). Printed rather than only stored
// because the question it answers - "why did this rebuild?" - is usually asked
// at a terminal.
//
// Silent when nothing resolved, which is a build whose references were already
// digests or one that had no resolver. Saying "0 pinned" on every ordinary build
// would train the reader to skip the line on the day it matters.
func recordPinning(w io.Writer, pinned map[string]string) {
	if w == nil || len(pinned) == 0 {
		return
	}

	refs := make([]string, 0, len(pinned))
	for ref := range pinned {
		refs = append(refs, ref)
	}

	// Sorted: map order is not stable, and a provenance record that reorders
	// between runs cannot be diffed against the run before it.
	sort.Strings(refs)

	for _, ref := range refs {
		fmt.Fprintf(w, "  pinned                    %s -> %s\n", ref, pinned[ref])
	}
}

// resolveFor is the platform a reference is resolved for.
//
// **The sandbox's, not this process's.** A plan for the native platform names
// none, and a registry asked for nothing in particular is asked for the platform
// the asking program was built for - which on macOS is `darwin/arm64`, a
// platform no image has. Every reference then failed to pin with `no manifest
// for darwin/arm64`, on the one platform this engine is developed on, and the
// build carried on unpinned exactly as designed.
//
// The same lesson as E503: a darwin worker that declared `darwin/arm64` to the
// fleet was never given a step, because the platform that matters is the one
// steps run on. Images are linux images however this engine was built.
//
// A platform the plan does name is honoured: a cross build asking for
// `linux/amd64` means it.
func resolveFor(platform string) string {
	if platform == "" {
		return exec.DefaultPlatform()
	}

	return platform
}
