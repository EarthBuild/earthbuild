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

// missingDevices names nothing off Linux, where deviceMounts binds nothing.
func missingDevices(string) []string { return nil }

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

func mountCgroup2(string) (func(), error) { return func() {}, nil }

func linkStdio(string) error { return nil }

// hostnameMount is nothing here.
//
// The linux one shadows the image's `/etc/hostname`, and shadowing needs a bind
// this platform has not got. Returning the mount anyway made every step on this
// platform have one, and a step with any mount at all takes a path that refuses
// with "cannot isolate the step: requires linux" - so six tests that had never
// been near a hostname went red (E765).
func hostnameMount() []Mount { return nil }

// hostsMountFor is what it always was here: a mount for declared entries, and
// nothing for a step that declared none.
//
// Not the linux rule, deliberately. There a step always gets one so its own
// name resolves (E768); here a step that declared nothing has no mounts at all,
// and giving it one sends it down a path that refuses with "requires linux"
// (E765). The name only has to resolve where a step could dial it.
func hostsMountFor(entries []string) []Mount {
	if len(entries) == 0 {
		return nil
	}

	return hostsMount(entries)
}

func mountDevPts(string) (func(), error) { return func() {}, nil }

// resolverMount is empty off Linux.
func resolverMount() []Mount { return nil }
