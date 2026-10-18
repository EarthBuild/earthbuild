package cli

import (
	"fmt"
	"io"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// netCounter is a sandbox that can say how much its guest's network has moved.
//
// Optional, and only the microVM backend implements it: a sandbox using the
// host's own network has no separate figure to give, and the host's totals
// would describe this machine rather than the build.
type netCounter interface {
	NetBytes() (sent, received uint64)
}

// chatter is the most a link carries while nothing is using it.
//
// ARP, the odd DHCP renewal, a neighbour advertisement. A stalled `RUN sleep
// 400` moved 1.7 KiB in five minutes with no step touching the network at all,
// and calling that "still moving" told the reader their download was
// progressing when nothing of the sort was happening. 64 KiB, because anything
// a step is genuinely fetching clears it by orders of magnitude and nothing
// idle comes close.
const chatter = 64 << 10

// traffic is a reading of a guest's network counters, or the absence of one.
type traffic struct {
	sent     uint64
	received uint64
	known    bool
}

// readTraffic takes a reading, if the sandbox keeps one.
func readTraffic(sb exec.Sandbox) traffic {
	c, ok := sb.(netCounter)
	if !ok {
		return traffic{}
	}

	sent, received := c.NetBytes()

	return traffic{sent: sent, received: received, known: true}
}

// netLine says whether the guest's network moved between two readings.
//
// **The distinction the stall note cannot otherwise make.** A step fetching a
// large base image over a thin link and a step holding a connection the remote
// will never answer present identically - one step, running a long time,
// nothing else progressing - and the right response to them is opposite. Bytes
// tell them apart, and at that moment nothing else does.
//
// Empty when the backend counts nothing, because "zero bytes" and "this
// backend cannot tell you" are different statements and only one of them is
// true here.
func netLine(before, now traffic) string {
	if !before.known || !now.known {
		return ""
	}

	sent, received := now.sent-before.sent, now.received-before.received

	switch {
	case sent == 0 && received == 0:
		return "  the guest's network is not moving: nothing sent and nothing received since\n" +
			"  the last check, so a step waiting on one is waiting on something that will\n" +
			"  not arrive\n"

	case sent+received < chatter:
		return fmt.Sprintf(
			"  the guest's network is idle but for background traffic: %s out, %s in since\n"+
				"  the last check, which is a link keeping itself up rather than a step using it\n",
			bytesHuman(sent), bytesHuman(received))

	default:
		return fmt.Sprintf("  the guest's network is still moving: %s out, %s in since the last check\n",
			bytesHuman(sent), bytesHuman(received))
	}
}

// bytesHuman renders a byte count the way a person reads one.
func bytesHuman(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// stallReporter writes stall notes, each carrying what the guest's network did
// since the previous one.
//
// Stateful because the interesting quantity is a difference: a total says how
// much a build has fetched, and the question here is whether anything is
// happening *now*.
//
// Not concurrency-guarded, because the scheduler calls OnStall from one ticker
// goroutine. Two schedulers get two reporters.
func stallReporter(w io.Writer, sb exec.Sandbox) func(string) {
	last := readTraffic(sb)

	return func(note string) {
		now := readTraffic(sb)

		fmt.Fprint(w, note+netLine(last, now))

		last = now
	}
}
