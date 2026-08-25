package exec

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// EnvAllowLeakedSecrets lets a build save an image holding a secret it was
// given.
//
// **The check is on by default and this is the way out.** Refusing costs a build
// that used to pass, and a step that writes a credential on purpose - an
// `.npmrc`, a `.netrc` - is a real if unhappy pattern. Somebody doing it
// deliberately can say so; nobody doing it by accident has to know this exists.
const EnvAllowLeakedSecrets = "EARTH_ALLOW_LEAKED_SECRETS"

// **[GAP] The record does not survive the cache, and the exit points do not
// exist yet.**
//
// A layer that was scanned in *this* build is remembered here, in this process.
// A build that gets the same layer from the cache never runs the step, never
// scans, and knows nothing - so a cached leak would pass. The fix is a sidecar
// beside the layer in the store, the way `.unmarked` records what a capture
// learned; this is deliberately not that yet, because the thing it would guard
// is also missing.
//
// Nothing in this engine saves an image anywhere a secret could go. `SAVE IMAGE`
// is recorded and not performed, `RUN --push` is refused, and the only path that
// packs one is `WITH DOCKER --load`, which loads it into the build's own daemon.
// The refusal is wired there because it is the only exit that exists; when
// saving and pushing arrive they have to call it too, and the record has to
// outlive the process before either is trustworthy.
//
// leakedLayers remembers which layers hold a secret the build was given.
//
// **Recorded at capture and refused at the exit.** A layer on the builder's own
// disk has not gone anywhere; it becomes a leak when the image is saved or
// pushed, and that is the only place worth failing - the check is then paid once
// per exit rather than once per step, and a build that exports nothing cannot
// leak anything.
//
// Only layers this build produced are ever in here. A base layer arrived before
// the build did and is read-only to it, so it cannot contain a credential this
// build was handed.
type leakedLayers struct {
	mu sync.Mutex
	by map[ir.NodeID][]string
}

// noteLeaked records what the guest found in the layer a step produced.
func (e *Executor) noteLeaked(id ir.NodeID, found []string) {
	if len(found) == 0 {
		return
	}

	e.leaked.mu.Lock()
	defer e.leaked.mu.Unlock()

	if e.leaked.by == nil {
		e.leaked.by = map[ir.NodeID][]string{}
	}

	e.leaked.by[id] = found
}

// leakedIn is every finding among a stack's layers, sorted.
func (e *Executor) leakedIn(stack []ir.NodeID) []string {
	e.leaked.mu.Lock()
	defer e.leaked.mu.Unlock()

	var out []string

	for _, id := range stack {
		out = append(out, e.leaked.by[id]...)
	}

	sort.Strings(out)

	return out
}

// refuseLeakedImage stops an image that carries a credential from being written.
//
// The message names the secret and where it was found, and never the value: it
// goes to the build's output, which is the log the credential was being kept out
// of.
func (e *Executor) refuseLeakedImage(where string, stack []ir.NodeID) error {
	if os.Getenv(EnvAllowLeakedSecrets) != "" {
		return nil
	}

	found := e.leakedIn(stack)
	if len(found) == 0 {
		return nil
	}

	return fmt.Errorf("%s: a secret this build was given is in a layer of this"+
		" image, and the image is not written"+
		"\n  %s"+
		"\n  an image is saved to be used elsewhere, which is where the"+
		" credential would go"+
		"\n  keep the secret out of the layer, or set %s if it belongs there",
		where, strings.Join(found, "\n  "), EnvAllowLeakedSecrets)
}
