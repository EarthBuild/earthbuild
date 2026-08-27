//go:build !linux

package guest

// bindMounts is unavailable off Linux.
//
// The guest only ever runs on Linux - inside a VM on macOS - so this exists to
// keep the package building on the machine developing it, and refuses rather
// than pretending: a step that ran without its cache mount would report success
// having built without the cache it asked for.
func bindMounts(_, _, _, _ string, _ []Mount) (func(), error) {
	return func() {}, ErrCannotIsolate
}

// deviceMounts is empty off Linux: there is no step filesystem to put them in.
func deviceMounts() []Mount { return nil }

// mountProc has nothing to do off Linux, which is not the same as failing to do
// it.
//
// Success is the correct answer here rather than a degraded one: a step off
// Linux is never chrooted - `isolate` refuses first - so it sees the machine's
// own `/proc` and there is nothing to mount for it. The previous comment said
// "unavailable", which reads like the silent substitution E391 found on the
// other backend; the difference is that here nothing was promised.
func mountProc(string) (func(), error) { return func() {}, nil }

func mountSys(string) (func(), error) { return func() {}, nil }

// resolverMount is empty off Linux.
func resolverMount() []Mount { return nil }
