package cli

import (
	"strings"
	"testing"
)

// A stalled build says whether its guest's network is still moving.
//
// **The distinction the note cannot otherwise make.** A step fetching a large
// base image over a thin link and a step holding a connection the remote will
// never answer are the same observation - one step, running a long time, no
// progress elsewhere - and the advice for them is opposite. Bytes separate
// them, and nothing else available at that moment does.
//
// The microVM hang that prompted this moved no bytes at all: the remote had
// accepted the connection and sent nothing, so every counter stood still while
// the step waited.
func TestAStallSaysWhetherTheNetworkIsMoving(t *testing.T) {
	t.Parallel()

	moving := netLine(traffic{sent: 100, received: 200, known: true}, traffic{sent: 140, received: 9000, known: true})
	if !strings.Contains(moving, "8.6 KiB") {
		t.Errorf("a network that received 8800 bytes did not say so: %s", moving)
	}

	if strings.Contains(moving, "not moving") {
		t.Errorf("a moving network was called stopped: %s", moving)
	}

	still := netLine(traffic{sent: 100, received: 200, known: true}, traffic{sent: 100, received: 200, known: true})
	if !strings.Contains(still, "not moving") {
		t.Errorf("a network that carried nothing was not called stopped: %s", still)
	}

	// Sent-only counts as movement: a step retrying a request is doing
	// something, even though nothing is coming back.
	if strings.Contains(netLine(traffic{known: true}, traffic{sent: 1, known: true}), "not moving") {
		t.Error("a network that sent a byte was called stopped")
	}
}

// A sandbox with no network to report says nothing rather than zero.
//
// Zero bytes and "this backend cannot tell you" are different statements, and
// printing the first for the second would have the note assert something it
// does not know - the namespace backend uses the host's network and counts
// nothing.
func TestASandboxThatCannotCountSaysNothing(t *testing.T) {
	t.Parallel()

	if line := netLine(traffic{known: false}, traffic{known: false}); line != "" {
		t.Errorf("a backend that counts no bytes still produced a line: %s", line)
	}
}

// Background chatter is not a transfer, and is not described as one.
//
// **Observed, not anticipated.** The first end-to-end firing was a stalled
// `RUN sleep 400` - a step doing no networking whatever - and the note read
// "the guest's network is still moving: 0 B out, 1.7 KiB in". The bytes were
// real: a guest's link carries ARP and the odd DHCP renewal whether or not any
// step is using it. But "still moving" is the sentence that tells a reader
// their download is progressing, and here it was flatly the wrong reading of
// the very case the line exists to judge.
//
// Three bands rather than two, because the honest answer to "is the network
// doing anything" has a middle: nothing, background, and a transfer.
func TestBackgroundChatterIsNotCalledATransfer(t *testing.T) {
	t.Parallel()

	// What a link does while nothing uses it.
	chatter := netLine(traffic{known: true}, traffic{received: 1741, known: true})
	if strings.Contains(chatter, "still moving") {
		t.Errorf("1.7 KiB of chatter was called a transfer: %s", chatter)
	}

	if !strings.Contains(chatter, "1.7 KiB") {
		t.Errorf("the note hid the figure it was judging: %s", chatter)
	}

	// What a step fetching something looks like.
	transfer := netLine(traffic{known: true}, traffic{sent: 4096, received: 9 << 20, known: true})
	if !strings.Contains(transfer, "still moving") {
		t.Errorf("9 MiB was not called a transfer: %s", transfer)
	}

	// Nothing at all stays its own case: it is the one that says the step is
	// waiting on something that will not arrive.
	if !strings.Contains(netLine(traffic{known: true}, traffic{known: true}), "not moving") {
		t.Error("a silent network was not called silent")
	}
}
