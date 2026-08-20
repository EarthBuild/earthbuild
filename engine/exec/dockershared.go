package exec

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// sharedDockerFor decides whether this block may be given a daemon that is not
// its own.
//
// **Two correct decisions that contradict each other.** The interpreter says a
// `WITH DOCKER` block with no `--cache-id` starts with an empty daemon and is
// therefore cacheable (E354). The only daemon this engine can offer today is
// *this machine's*, which holds whatever every previous build left in it.
//
// A step declared to be a function of its inputs, handed state that is not its
// inputs, and cached under a key that says nothing about what it saw, is a false
// cache hit waiting for the second build (I3). So the block that asked for
// isolation does not get a shared daemon - it is refused, and the refusal names
// the way out that exists today rather than only the one that does not (E355).
//
// The trust gate comes first and is not something a `--cache-id` buys past: a
// step holding this machine's socket has root on this machine whatever its cache
// says (E145).
func sharedDockerFor(
	cache string, look func(string) (string, bool), allowed bool, ready Readiness,
) ([]guest.Mount, string, error) {
	// **What a peer sent, checked here.** The interpreter checks this name too
	// and that check protects the author (E358); this one runs where the value
	// arrived from a driver this worker did not write (A5, C.3), and it is the
	// name a directory will be made of the moment there is a daemon to give one
	// (E360).
	if cache != "" {
		if err := checkCacheName(cache); err != nil {
			return nil, "", fmt.Errorf("this WITH DOCKER block names a cache"+
				" this machine will not use: %w", err)
		}
	}

	mounts, note, err := hostDockerMounts(look, allowed)
	if err != nil {
		return nil, "", err
	}

	if cache == "" {
		return nil, "", fmt.Errorf(
			"this WITH DOCKER block asked for a daemon of its own and this"+
				" engine has only the one on this machine, which holds what"+
				" earlier builds left in it\n"+
				"  a block with no --cache-id is cached, and caching a step"+
				" that read another build's images would serve that result"+
				" again when the images have changed\n"+
				"  add --cache-id=<name> to say the block shares a cache, which"+
				" marks it uncacheable and is honest about what it sees\n"+
				"  a daemon of its own is not built yet, %s", couldHost(ready))
	}

	// **The name promises a separation this engine cannot give.** E354's promise
	// is that blocks naming the same cache see each other's images and blocks
	// naming different ones do not. With the daemon on this machine there is one
	// storage area and every block shares it, so half the promise holds and half
	// does not - and which half is not visible from the Earthfile (E362).
	//
	// A note rather than a refusal: the block works, the sharing it asked for
	// happens, and what it does not get is the separation from *other* names,
	// which most uses do not rely on. Refusing would take away the only
	// configuration that runs today.
	return mounts, joinNotes(note, fmt.Sprintf(
		"this block named the cache %q, and the daemon on this machine has one"+
			" storage area that every block shares - so a build separating"+
			" its caches by name does not get that here", cache)), nil
}

// joinNotes puts two reasons together, keeping whichever there is.
//
// One line each and no blank one between: this is the single place a step says
// why its daemon behaved oddly, and a message with a hole in it is one people
// stop reading (E146).
func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}

	return a + "\n" + b
}

// couldHost turns a readiness into the half-sentence that follows "not built
// yet".
//
// **Two pieces of news in one message.** That this engine has not built a daemon
// of its own is about the engine, and changes when somebody writes it. That this
// machine could not host one anyway is about the machine, and changes when the
// operator acts. An operator who reads only the first waits for a release that
// will not help them (E361).
func couldHost(ready Readiness) string {
	if ready.OK {
		return "and this machine could host one when it is"
	}

	if ready.Why == "" {
		return "and whether this machine could host one has not been checked"
	}

	return "and this machine is not ready for one either:\n  " + ready.Why
}
