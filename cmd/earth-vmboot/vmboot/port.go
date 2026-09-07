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

// ExportPort is where the host asks for a staged artifact.
//
// **The control channel only.** The bytes go on the export device, not down
// this connection: the host sends the staged path and reads back a byte count,
// and the artifact itself is written once to a block device the host then
// reads. See ExportDev.
const ExportPort = 5557

// ExportDev is the block device an artifact leaves the guest on, and ExportAt
// is the same device seen by the host.
//
// **It carries a stream, not a filesystem.** A formatted volume the host mounts
// would put a kernel filesystem parser on metadata the sandbox authored, which
// is the surface the VM boundary was added to remove; a tar is parsed in
// userspace by code that refuses what it does not like. It also disposes of the
// "the host must trust the unmount happened" problem, because there is no
// unmount.
const ExportDev = "/dev/vdb"

// StoreAt is where the guest mounts the block device carrying the layer store.
//
// Shared for the same reason the ports are: the host names blobs it has placed
// by a path *inside* the guest, and a path the two sides spell separately is a
// guest that has the bytes and is told to open them somewhere else. That is not
// hypothetical - it is what happened, and the guest reported `no such file or
// directory` for a blob it was holding.
const StoreAt = "/store"
