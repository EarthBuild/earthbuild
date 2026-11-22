//go:build linux

package exec

import "strings"

// lostGuest adds the guest's console when the failure is that the guest
// stopped talking.
//
// **The same question as a failed handshake, asked later.** A guest that never
// answered already quotes its console; one that stopped part-way through a
// build did not, though it is the same guest and the same console. The engine
// reported only that the far end stopped writing - which a panic in the agent,
// an OOM kill and a kernel oops all produce identically, and all three explain
// themselves on the console.
//
// Narrow on purpose: a step that merely exited non-zero leaves a healthy guest,
// and its last console line has nothing to do with the failure.
func lostGuest(err error, sb Sandbox) error {
	if err == nil || !strings.Contains(err.Error(), "guest connection lost") {
		return err
	}

	return withConsole(err, sb)
}
