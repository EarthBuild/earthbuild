//go:build linux

package exec

import "testing"

// A machine is joined only when the operator asked for it.
//
// **This rule used to be about the network, and no longer is.** By default this
// engine gives a microVM its network without privilege: a tap in a namespace
// the shim made, and a userspace stack on this side of it - and that stack lives
// in the build. So a machine left running was left with a tap nobody serviced,
// and a later build that joined one got a guest that could not resolve a name:
//
//	fresh boot   rc=0, no DNS errors
//	attached     rc=1, `DNS: transient error (try again later)` from apk
//
// The reading was that a machine may be joined only where the network came from
// outside the build - which is what EARTH_VM_TAP says. That was too strong. The
// stack does not have to *outlive* the build, only to be re-establishable, and
// between builds a guest is idle and a tap with no reader drops nothing anybody
// sent (E981). What was missing was a way to get a socket for a tap that already
// exists, and the server the shim leaves inside the namespace is that.
//
// So both kinds of network can be rejoined now, and what is left to decide is
// whether the operator wants a machine that outlives its build at all - which
// is a question about the boundary rather than about the network.
func TestAMachineIsJoinedOnlyWhenAskedFor(t *testing.T) {
	for _, c := range []struct {
		set  string
		want bool
		why  string
	}{
		{"", false, "nothing was said, and a weaker boundary is not a default"},
		{"0", false, "the operator said no"},
		{"false", false, "the operator said no"},
		{"no", false, "the operator said no"},
		{"1", true, "the operator asked for it"},
	} {
		t.Setenv(EnvReuse, c.set)

		if got := mayAttach(); got != c.want {
			t.Errorf("%s=%q read as mayAttach=%v, wanted %v (%s)",
				EnvReuse, c.set, got, c.want, c.why)
		}
	}
}

// TestReuseIsOffUntilAsked states the direction of the default alone, because
// the table above would pass just as well with every answer inverted together.
//
// **A reused machine is a weaker boundary than a fresh one**, and the boundary
// is what this backend is for. A guest serving a second build carries the
// first's kernel state and page cache - not its agent, which is a new process
// per build, nor its steps, which run in their own overlays, but not nothing
// either. That is a trade worth offering and not worth taking unasked.
func TestReuseIsOffUntilAsked(t *testing.T) {
	t.Setenv(EnvReuse, "")

	if mayAttach() {
		t.Fatal("a build nobody asked would join a machine another build left" +
			" running, which is a weaker boundary than the one it thinks it has")
	}
}
