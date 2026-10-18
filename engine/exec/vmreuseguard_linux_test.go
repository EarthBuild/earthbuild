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
		{"", true, "nothing said: a machine outlives its build by default"},
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

// TestReuseCanBeDeclined states the one direction that must keep working,
// apart from the table above.
//
// **A reused machine is a weaker boundary than a fresh one**, and that trade
// was decided rather than assumed: a guest serving a second build carries the
// first's kernel state and page cache - not its agent, which is a new process
// per build, nor its steps, which run in their own overlays, and the layer
// store was shared already. A default nobody can turn off is not a default,
// and this is the way back to a machine per build.
func TestReuseCanBeDeclined(t *testing.T) {
	t.Setenv(EnvReuse, "0")

	if mayAttach() {
		t.Fatal("a build that asked for a machine of its own would join one" +
			" another build left running, so there is no way back to the" +
			" stronger boundary")
	}
}
