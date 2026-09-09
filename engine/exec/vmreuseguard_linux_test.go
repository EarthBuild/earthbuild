//go:build linux

package exec

import "testing"

// A machine is only joined when its network outlives the build that made it.
//
// **The host runs the guest's network in its own process.** By default this
// engine gives a microVM its network without privilege: a tap in a namespace
// the shim made, and a userspace TCP/IP stack on this side of it - and that
// stack lives in the build. When the build exits it goes, and the machine is
// left with a tap nobody is servicing. A later build that joined such a machine
// got a guest that could not resolve a name:
//
//	fresh boot   rc=0, no DNS errors
//	attached     rc=1, `DNS: transient error (try again later)` from apk
//
// So a machine is joined only where the network was not this process's to
// provide - which is what EARTH_VM_TAP says. The rest of the reuse machinery
// is unaffected and becomes useful again as soon as the stack outlives the
// build rather than the other way round.
func TestAMachineIsJoinedOnlyWhenItsNetworkSurvives(t *testing.T) {
	t.Parallel()

	t.Setenv(EnvTap, "")

	if mayAttach() {
		t.Error("a machine would be joined although this build provides its" +
			" network, so the guest would be left without one")
	}

	t.Setenv(EnvTap, "tap0")

	if !mayAttach() {
		t.Error("a machine whose network came from outside this build is not" +
			" joined, so every build pays a boot for nothing")
	}
}
