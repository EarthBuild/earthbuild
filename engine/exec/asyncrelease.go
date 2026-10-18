package exec

import (
	"os"
	"runtime"
	"strconv"
	"sync"
)

// EnvAsyncRelease releases a step's base after the step's answer, instead of
// before it.
//
// **Because releasing is most of a step.** Measured per step on Linux, twenty
// deep: `exec` 26.00ms, of which `release` is 18.55ms and `run` - the command
// the Earthfile actually asked for - is 6.05ms. Seventy-one per cent of a step
// is taking down the mount its work has already finished with.
//
// The release is `unix.Unmount` then `os.RemoveAll`, 15.8ms and 3.5ms, against
// the 5us the mount cost to make. Nothing reads through the handle after the
// step: the result is committed and captured first, and what is released is the
// *host's* handle on the materialised base, not the guest's bind mounts - those
// come down inside the request, before the answer, and `capture` does read under
// them (E813).
//
// **Bounded, because the kernel bounds it anyway.** Thirty-two overlay unmounts
// take 87ms one at a time and 36ms sixteen at a time: `namespace_sem` is held
// for write through each, so beyond a handful the releases queue on the kernel
// rather than finishing sooner. Eight is past the knee and short of pointless.
//
// Off by default. A release moved behind the answer is a mount that is still up
// when the next step starts, and the failure that would cause - a sandbox out of
// mounts - appears under load rather than in a test. `Close` waits for the
// outstanding ones, so a build never exits leaving mounts behind.
const EnvAsyncRelease = "EARTH_ASYNC_RELEASE"

// releaseWidth is how many releases may be in flight. Zero means off.
func releaseWidth() int {
	raw := os.Getenv(EnvAsyncRelease)

	switch raw {
	case "", "0", "false", "no":
		return 0
	}

	n, err := strconv.Atoi(raw)
	if err == nil {
		if n < 1 {
			return 0
		}

		return n
	}

	return min(runtime.NumCPU(), 8)
}

// releaser holds the releases that have not finished yet.
//
// One per executor, and waited for by `Close`: what is deferred is when a mount
// comes down, never whether it does.
type releaser struct {
	once sync.Once
	slot chan struct{}
	wg   sync.WaitGroup
}

// release runs the teardown, behind the step's answer when that was asked for
// and in front of it otherwise.
func (r *releaser) release(width int, undo func()) {
	if width < 1 {
		undo()

		return
	}

	r.once.Do(func() { r.slot = make(chan struct{}, width) })

	r.wg.Go(func() {
		r.slot <- struct{}{}
		defer func() { <-r.slot }()

		undo()
	})
}

// wait blocks until every deferred release has finished.
func (r *releaser) wait() { r.wg.Wait() }
