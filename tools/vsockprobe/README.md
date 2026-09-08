# vsockprobe

Measures whether a VMM's vsock carries a stream intact, and at what write size
it stops doing so.

This exists because `engine/guest/proto.go` caps every write to a guest
connection at 32 KiB, and that number is a property of the hypervisor rather
than of this engine. Without a way to re-measure it, the constant is folklore
the next firecracker release can quietly invalidate.

## What it found

Firecracker v1.13.1, guest kernel 6.18, x86_64:

| Bytes per `Write` | Result                                    |
| ----------------- | ----------------------------------------- |
| 8192              | clean                                     |
| 16384             | clean                                     |
| 32768             | clean                                     |
| 33792             | stream jumps back 32768 bytes, once/write |
| 65536             | same                                      |
| 1048576           | same                                      |

Only when the reader stalls. A reader that never pauses took 512 MB at 1 MiB
per write without a single fault, which is why this was invisible to every test
and showed up only under a real build.

The displacement is always exactly 32768 - half of firecracker's 64 KiB
per-connection TX ring (`CONN_TX_BUF_SIZE`). Bytes are not lost: a 32 KiB run
already delivered is delivered a second time, so a length-prefixed reader is
left permanently off by that much and fails far from the damage.

## Running it

    go run ./tools/vsockprobe -h        # see blast/ and check/

`blast` is PID 1 of a microVM: it writes a counting stream, where the
little-endian uint64 at every eight-byte offset is that offset over eight.
`check` reads it from the host and reports the first counter that is not the
one it expected, and by how far the stream is displaced.

    # in the guest: an initramfs whose /init is blast
    # on the host:
    check -uds <firecracker vsock socket> -slow 5ms -every 1048576

`-slow` is the point. It holds the reader up the way a build does while it is
hashing a layer, and without it the fault does not appear.
