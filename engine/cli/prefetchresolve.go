package cli

import (
	"errors"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// prefetchResolver starts every reference an Earthfile names at once and
// answers the interpreter from what it started.
//
// **A warm build is its image resolutions and nothing else.** Five cached steps
// on one image measured 0.22s, of which `plan` is 0.208s and the schedule
// 0.002s. Two distinct images measured 0.336s - the sum of the two, because the
// interpreter walks the file and resolves each `FROM` as it reaches it, and
// nothing about resolving one image depends on another.
//
// The memo in `Plan.pin` is what makes it once per *reference* (I17) and stays
// where it is; this changes *when* those lookups happen, not how many. A
// reference the scan never saw - one built from an ARG, or in an Earthfile this
// did not read - resolves inline exactly as it did before.
type prefetchResolver struct {
	resolve interp.ResolveImage
	mu      sync.Mutex
	going   map[string]chan resolved
}

// resolved is one reference's answer, kept so that every waiter gets it and the
// registry is asked once.
type resolved struct {
	to  string
	err error
}

func newPrefetchResolver(resolve interp.ResolveImage) *prefetchResolver {
	return &prefetchResolver{resolve: resolve, going: map[string]chan resolved{}}
}

// start asks for every reference at once, and returns without waiting.
//
// Duplicates are ordinary: an Earthfile naming one base in three targets is the
// case `Plan.pin`'s memo exists for, and starting three lookups here would put
// the memo back to work undoing this.
func (p *prefetchResolver) start(refs []string, platform string) {
	if p == nil || p.resolve == nil {
		return
	}

	for _, ref := range refs {
		p.begin(ref, platform)
	}
}

// begin starts one reference if nobody has, and answers whether it is going.
//
// **Keyed on the platform the resolver will actually use.** The prefetch is
// started with the build's platform and the interpreter calls back with the
// step's, which is empty for the default - so keying on the text as written put
// one image under two keys and resolved it twice, which is the opposite of the
// point. `resolveFor` is what settles the two, and it is idempotent.
func (p *prefetchResolver) begin(ref, platform string) chan resolved {
	key := ref + "\x00" + resolveFor(platform)

	p.mu.Lock()

	if ch, going := p.going[key]; going {
		p.mu.Unlock()

		return ch
	}

	// Buffered, so the goroutine finishes whether or not anybody ever asks: a
	// build that stops early must not leave a lookup wedged on a send.
	ch := make(chan resolved, 1)
	p.going[key] = ch

	p.mu.Unlock()

	go func() {
		to, err := p.resolve(ref, platform)
		ch <- resolved{to: to, err: err}
	}()

	return ch
}

// Resolve is the interpreter's Θ (green paper §3.4d).
//
// Waits for a prefetch when there is one and starts a lookup when there is not,
// so the interpreter cannot tell the difference except in how long it waits.
//
// **The answer is kept**, because a channel yields once and the interpreter may
// ask again - `Plan.pin` memoises, but this must not depend on that: an
// optimisation that is only correct while a caller happens to cache is one
// refactor from being wrong.
func (p *prefetchResolver) Resolve(ref, platform string) (string, error) {
	if p == nil || p.resolve == nil {
		return ref, nil
	}

	ch := p.begin(ref, platform)

	got, open := <-ch
	if !open {
		// Somebody already took the answer and put it back closed, which cannot
		// happen with the buffered channel above - stated so that a future
		// change to the buffering fails loudly here rather than returning an
		// empty reference that reads as "not pinned".
		return "", errPrefetchGone
	}

	// Put back for the next asker. One slot, one value, and only ever this
	// reference's own answer.
	ch <- got

	return got.to, got.err
}

// errPrefetchGone reports an answer that was taken and not put back, which the
// buffering above makes impossible. It exists so the impossible case has a name
// rather than an empty string that would read as an unpinned reference.
var errPrefetchGone = errors.New("the resolution was lost")
