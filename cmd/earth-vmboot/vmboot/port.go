// Package vmboot carries what the host and the guest must agree on.
//
// A package of its own, and only a constant in it, so that the host backend and
// the guest's PID 1 cannot drift: a port the two sides define separately is a
// guest that boots, listens, and is never spoken to.
package vmboot

// VsockPort is where the agent waits for the host inside the guest.
const VsockPort = 5555
