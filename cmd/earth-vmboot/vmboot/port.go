// Package vmboot carries what the host and the guest must agree on.
//
// A package of its own, and only a constant in it, so that the host backend and
// the guest's PID 1 cannot drift: a port the two sides define separately is a
// guest that boots, listens, and is never spoken to.
package vmboot

// VsockPort is where the agent waits for the host inside the guest.
const VsockPort = 5555

// BulkPort is where blob bytes arrive.
//
// **A channel of its own, because the agent's frames cannot hold a layer**: the
// protocol is length-prefixed JSON with a size limit, so a 45 MB layer would
// have to be base64-encoded and cut into pieces. See bulk.SendBlob.
//
// Served by PID 1 rather than by the agent, because PID 1 is what mounted the
// device the blobs land on and the agent finds them afterwards by path - which
// is the same thing it does on every backend that shares a filesystem.
const BulkPort = 5556
