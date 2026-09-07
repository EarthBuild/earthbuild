package exec

import "fmt"

// consoleKeeper is a sandbox that kept what its guest wrote to the console.
//
// Optional: only a backend that gives its guest a console of its own has one to
// quote. A sandbox sharing this machine's kernel has no such thing, and an
// empty heading would be worse than silence.
type consoleKeeper interface {
	ConsoleTail() string
}

// withConsole adds the guest's own last words to a failure, when there are any.
//
// **A guess and a fact were being printed as one message.** A handshake timeout
// advised "it is running and not speaking: check the sandbox agent is the one
// this build produced", which sends the reader to rebuild a binary. In the case
// that prompted this the guest's console said something else entirely - five
// virtio devices failing to probe with EBUSY, on a guest that had otherwise
// booted and announced itself ready - and nothing in the error hinted that a
// console existed to look at.
//
// Wrapped with %w so the failure stays matchable: this adds evidence, it does
// not replace the error.
func withConsole(err error, sb Sandbox) error {
	k, ok := sb.(consoleKeeper)
	if !ok {
		return err
	}

	tail := k.ConsoleTail()
	if tail == "" {
		return err
	}

	return fmt.Errorf("%w%s", err, tail)
}
