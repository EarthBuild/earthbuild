//go:build linux

package main

import (
	"slices"
	"testing"
)

// The agent is told its store is the device, and not left on its default.
//
// **The guest's default is `/var/lib/earthbuild`, which in a microVM is the
// initramfs**: a tmpfs the size of the guest's memory that nothing else can
// read and that goes when the machine does. The store is the block device
// mounted at /store, and nothing else tells the agent so - the first build in a
// VM planned, ran, and failed looking for a layer under `/var/lib/earthbuild`
// that the host had unpacked somewhere else entirely.
func TestTheAgentIsToldTheStoreIsTheDevice(t *testing.T) {
	t.Parallel()

	if !slices.Contains(agentEnv(nil), "EARTH_GUEST_ROOT="+storeAt) {
		t.Errorf("the agent is not told where its store is: %v", agentEnv(nil))
	}
}

// What the kernel passed in survives, because the boot arguments are the only
// way a setting reaches a guest.
func TestTheBootEnvironmentIsKept(t *testing.T) {
	t.Parallel()

	got := agentEnv([]string{"EARTH_TRACE_PIN=1"})

	if !slices.Contains(got, "EARTH_TRACE_PIN=1") {
		t.Errorf("a setting given at boot did not reach the agent: %v", got)
	}
}
