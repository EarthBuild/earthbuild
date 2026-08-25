# Native engine settings

The native engine is reached by the `earth-native` binary. These environment variables change what it
does; everything else in the engine is decided by the Earthfile.

Nothing here is required. The defaults are what a build gets when none of them is set, and each entry
says what happens then.

## Where things are kept

### `EARTH_CACHE_DIR`

The layer store, the action cache and the scratch a build works in.

Default: `$XDG_CACHE_HOME/earthbuild`, or `~/.cache/earthbuild`.

A build shares this with every other build on the machine, which is what makes a second build fast.
Two builds may use it at once.

### `EARTH_IMAGE_CACHE_DIR`

Where images pulled from a registry are kept, if it should be somewhere other than the store above.

Default: inside `EARTH_CACHE_DIR`.

### `EARTH_TRACE`

Whether a step's reads are watched.

Watching is how a step earns a second-tier cache hit: the engine records what the step actually
looked at, so the same step over a *different* base can reuse the result when nothing it read
differs. That is worth a great deal on a build whose bases move and nothing on a build that always
misses.

It is paid for on every intercepted system call. Measured on a step that reads four thousand small
files and does nothing else, watching costs twenty-five times; measured on this repository's own test
suite, it cost nothing that could be seen behind a virtual machine. Both are true of what they
measured, which is why the switch exists: the honest way to know what it costs on *your* build is to
run it both ways.

Set to `0` to run steps unwatched. Every step then misses the second tier and is cached only on its
declared inputs, which is correct and slower in the way that usually matters more.

Default: on.

### `EARTH_SCRATCH_TMPFS`

Puts the scratch directory on a tmpfs of the given size, as `4g` or `512m`.

Default: unset, and the scratch is on disk with the rest of the store.

**Worth about a quarter of a cold build's wall clock** - 1715 ms against 1289 ms on a 21-step build -
because a step's writes, the capture that reads them back, and the removal afterwards all happen
there.

**It is memory.** A step's scratch holds everything the step wrote before it becomes a layer, so a
build producing gigabytes produces them in RAM. Size it against the largest step a build has, not the
average, and leave it unset where that is not known.

A step that outgrows it fails with `no space left on device` and a message saying that is what
happened. A size that is not a number and a unit is refused rather than ignored; a percentage is
refused too, although the kernel would accept one.

## What a step is allowed

### `EARTH_ALLOW_HOST_DOCKER=1`

Lets a `WITH DOCKER` block use this machine's own docker daemon.

Default: unset, and it is refused.

**That daemon is root on this machine.** A step holding its socket can start a container with `/`
mounted and write anywhere, whatever user the step runs as, and no namespace the engine sets up
constrains it. Set this only where the machine is disposable.

A build running *inside* a container uses the daemon it is already inside without this setting: that
daemon belongs to the step this build is running in, and the decision to grant it was made one level
up.

## Where the pieces are

### `EARTH_GUESTD`

The path to the `earth-guestd` binary, which runs a step's filesystem operations.

Default: on Linux, the CLI runs the agent out of itself (`earth guestd`), so
there is nothing to find and nothing to set. On macOS the agent runs inside a
Linux VM and so must be a separate Linux binary, looked for next to the CLI.

Set it when you are testing an agent you built yourself.

### `EARTH_SANDBOX_MEMORY`

How much memory the sandbox VM is given, on macOS. Ignored elsewhere, where there is no VM.

Default: the backend's own.

### `EARTH_CLONE_TREES`

Whether a tree is placed by cloning it. On a filesystem with copy-on-write clones - APFS, and Linux
filesystems that support reflinks - a whole tree is placed in one call and shares its storage with
the original until something writes to it. Set to `0`, `false` or `no` to place trees by linking
each entry instead, which is what happens anyway when the source and destination are on different
filesystems.

Default: on.

### `EARTH_GUEST_IDLE`

How long a sandbox stays up with nothing to do, as a duration - `20m`, `2h`, `90s`. A sandbox that
stops too early costs one VM boot, about 0.4s, on the next build; one that never stops costs a VM
per interrupted build until the machine runs out. Zero means never stop.

Default: `30m`.

### `EARTH_CLONE_EXPORTS`

Whether a saved artifact is copied by the filesystem rather than by reading and writing its bytes.

On APFS a clone shares the extents and diverges on the first write, so an exported file costs almost
nothing to produce and behaves exactly like a copy when you edit it. A 45MB binary went from 0.24s to
0.015s. Where the store and the destination are on different volumes, or the filesystem cannot clone,
the copy happens as it always did.

Set to `0` to copy always. The switch exists because cloning has been blamed for a fault once before
and turned out to be innocent, and a build that can be told to copy is one whose next mystery can be
bisected in a single command.

Default: on.

### `EARTH_SHARE_EXPORTS`

Whether a saved artifact may be taken from the store instead of being sent out of the sandbox.

The store is a disk both sides can read. When the file a build is exporting is one the store already
holds, unmodified, the sandbox says where it is rather than writing 45MB back across the shared
mount, and the host takes it from its own filesystem. Exporting this repository's own binary went
from 0.585s to 0.001s, and a warm build from 1.18s to 0.84s.

The sandbox answers this way only when it can prove the file is the store's file unchanged - not
rewritten by the step, not deleted, not a directory or a link, and in a layer it holds pristine.
Anything it cannot prove is sent the ordinary way, so the switch changes what a build costs and not
what it produces: the artifact is identical in bytes, mode and timestamp either way.

Set to `0` to always send the bytes. Keep it for bisecting, and for the same reason
`EARTH_CLONE_EXPORTS` exists - being able to run one build both ways is what turns "the artifact
looks right" into "the artifact is the same".

Default: on.

### `EARTH_GUEST_DENTRY_LIMIT`

How many looked-up names a sandbox holds before it releases them, as a count.

A store shared from the host costs the host one open file descriptor per name the sandbox has looked
up, held until the sandbox forgets it. There is a ceiling on those, it is not in either kernel's
documented limits, and nothing can ask about it - a build simply stops with `too many open files in
system` on a path that looks like the sandbox's. `earth +earthly` reached it on this repository's own
`examples` directory.

So the sandbox watches what it is holding and lets go before the ceiling. The cost is that the next
walk of the same tree is cold: about 201µs a file rather than 96µs. The cost of not doing it is the
build.

Zero turns the release off, for a machine with descriptors to spare or a build that reads a large
tree repeatedly.

Default: `100000`.

### `EARTH_FLEET_DISCOVER`

Whether a fleet uses relays and endpoint discovery to reach machines it cannot dial directly. Set to
any non-empty value to turn it on. Off by default: it was on for one increment, and a worker given
the driver's address - a path that had been working - joined and was then given no work (E505).

Default: off.

### `EARTH_TIMINGS`

Makes a build say where its time went. Set to any non-empty value. Each line is one phase of one
step - `materialise`, `run`, `capture`, and the materialiser's own sub-phases - reported as the phase
ends rather than summarised at exit, so a build that is slow at step 900 of 1000 says so at step 900.

The switch is forwarded into the sandbox, so phases timed inside the guest appear in the same output
as those timed outside it.

Default: off.

### `EARTH_IMAGE_LAYERS`

Stores a pulled image as one directory per layer rather than one merged tree. Set to any non-empty
value.

The merged form unpacks every layer into a single directory, which costs the whole image once and
means each layer's blob is read, decompressed and written under a lock the next layer waits on. Kept
apart, layers unpack independently and the result becomes a stack the step above stands on directly -
worth up to 38% of an image's unpack when no single layer dominates it, and nothing at all when one
does (Amdahl: the largest layer is the floor).

The trade is depth. Every step above the image then binds a deeper stack, at roughly 0.67ms per layer
per step. A 22-layer base pays that on every step of the build; whether it repays depends on how many
steps there are, which is why this is a setting and not the default.

Experimental. The layers this produces are byte-identical in effect to the merged tree - same files,
same permissions, same adopted config - but the storage layout differs, so a cache filled one way is
not reused by the other.

Default: off.

### `EARTH_IMAGE_STREAM`

Unpacks each layer as its bytes arrive rather than after the whole blob has landed. Set to any
non-empty value. Only meaningful with `EARTH_IMAGE_LAYERS`, which is what makes it pay.

A layer's fetch and its own unpack are otherwise serial. Merged, that costs nothing measurable -
the engine is unpacking some *other* layer while this one arrives - but with the layers apart the
largest layer is the entire critical path, and at its tail there is nothing else left to overlap
with. Streaming makes those two concurrent, which is worth 14-24% of a cold `FROM` on top of what
keeping the layers apart already saves.

The digest is checked after the unpack, because with a stream that is the only place it can be. The
layer goes into a directory of its own that is discarded on any failure, so bytes that turn out not
to match are never kept - but a build does write them to disk before it knows, which is the reason
this is a setting rather than the default.

Default: off.

### `EARTH_UNPACK_IN_GUEST`

Has the guest unpack an image's layers rather than the host. Set to any non-empty value; only
meaningful with `EARTH_IMAGE_LAYERS`.

**The host cannot grant what an archive declares.** An unprivileged unpack tolerates a refused
`chown`, cannot create a device node, and cannot set an attribute in the `security.` namespace, so
the layer that lands is not quite the layer the image describes - and three separate mechanisms
exist to paper over the difference. Unpacking as root inside the guest removes all three questions
at once.

It is also where the layer store is going, for a reason that has nothing to do with privilege. A
shared directory is reached over virtiofs, and every metadata operation on it is a round trip across
the VM boundary. Measured from inside the guest on one layer of `golang:1.26-alpine`: unpacking into
the shared store takes 4.67s against 2.18s into the block device the guest owns, and reading it all
back 6.04s against 1.47s - about 0.31ms per file a step opens.

**This moves the unpack and not yet the store**, so with the layers still on the shared mount it is
slower than leaving it off. The two are separated deliberately: the wiring can be exercised before
the move it exists for.

Default: off.

### `EARTH_STORE_IN_VM`

Puts the layer store on the block device the guest owns rather than in a directory shared from the
host. Set to any non-empty value. Implies `EARTH_UNPACK_IN_GUEST`, because the host cannot write a
device it does not have.

**A shared directory is reached over virtiofs, and every metadata operation on it is a round trip
across the VM boundary.** Measured from inside the guest on one layer of `golang:1.26-alpine`:

```text
                        shared store    the guest's volume
unpack the layer            4.67s             2.18s
read all of it, cold        6.04s             1.47s
read all of it, warm        4.72s             0.12s
```

About 0.31ms per file a step opens - half a second on a cold `go build`, and invisible in every
phase this engine records, because it is spread through the step's own execution.

This is E511's principle applied to the rest of the store. That experiment moved CACHE mounts onto
the volume for the same reason and said why: outliving the build does not mean the host must see it.

**What it costs is the cache's lifetime.** The volume belongs to the sandbox and goes when the
sandbox does, so layers live as long as the machine rather than as long as a directory you own -
`scripts/reset-native-sandbox.sh` and a changed sandbox setting both take them. An export also stops
being able to come straight out of the store, since the host can no longer read it, and falls back
to the ordinary path.

**Both cache tiers ask rather than stat.** They used to read the host's own filesystem, and with the
layers inside the VM a repeat build cached nothing at all - `0 hit, 4 miss`, every prediction stale
with `/bin/sh is gone from the base`, which was literally true of the base as the host could see it.

Presence and views now cross the wire:

```text
build 1 (cold)          8.96s   0 hit, 4 miss
build 2                 0.25s   3 hit, 1 miss
one step changed        0.30s   2 hit, 2 miss, 1 unpredicted
```

The view is asked for a prediction's whole set of paths at once. A round trip per file would cost
more than the tier saves, and the paths are known before the view is needed - the profile is read
first.

Default: off.

## `EARTH_TRACE_PIN`

Puts a traced step and the thread answering its syscalls on the same vCPU.

A step is observed by a seccomp filter: every `openat`, `statx` or `execve` stops the caller until
this engine has read the path and let it through. That round trip is the price of L2, and under a
hypervisor almost all of it is the *wakeup* rather than the work - each half is a vmexit, because an
idle vCPU has halted and has to be resumed by the VMM.

The same test, unchanged, in three places:

| where                      | untraced | traced  | ratio |
| -------------------------- | -------- | ------- | ----- |
| bare metal x86, 32 core    | 1.018µs  | 8.857µs | 9x    |
| Apple VM arm64, 4 vCPU     | 0.389µs  | 50.56µs | 130x  |
| Apple VM arm64, **1** vCPU | 0.61µs   | 2.19µs  | 4x    |

The untraced call is 2.6x *faster* in the VM, so this is not a slow guest - it is the crossing. The
guest keeps all four vCPUs either way; only the two ends of the round trip share one, which the step
inherits across fork the same way it inherits the filter.

That table is the round trip alone. The test filters its own thread and then works on it, so every
notification is recognised as the engine's own and answered without reading a path - which is the
right isolation for measuring the crossing and the reason the figures below, which carry the
handler too, are 8.5µs per call rather than 2.2µs.

End to end, in the engine:

```text
step                                    pin off   pin on
20k traced stats of one file              1.219s   0.169s   7.2x
find /usr/local/go -type f (15k files)    2.114s   1.126s   2.0x
```

The second is smaller because it is no longer the wakeup that costs: fifteen thousand *distinct*
paths through a five-layer overlay is real filesystem work, and what remains after pinning is mostly
that. A step that asks about the same paths repeatedly - a configure script, a package manager, a
compiler's include search - is the shape this helps most.

**What it costs is a step's parallelism**, and that is measured rather than argued:

| pin         | 20k traced stats | 4-way parallel CPU |
| ----------- | ---------------- | ------------------ |
| off         | 1.204s           | 0.645s             |
| both ends   | 0.125s           | 2.308s             |
| tracer only | 1.218s           | 0.674s             |

2.9x against a step that wants four vCPUs, for 9.6x on one that floods the tracer; a
single-threaded step is untouched either way. The steps that flood the tracer are the
single-threaded ones and the steps that want four vCPUs make few path calls - but that is an
observation and not a policy, which is why this is a switch and not the default.

The third row is why it cannot be half done. Pinning only the answering thread would have been
adaptive by construction, and it buys nothing: the step is the thread that has to be woken, and
nothing pulls it onto the tracer's CPU (E685).

Flipping it makes a different sandbox, deliberately: the guest reads this at start, so a machine
already running was started with whatever the previous build said (E549).

Default: off.

## `EARTH_STREAM_TO_GUEST`

Lets the guest unpack a layer while the host is still fetching it.

A layer cannot normally be unpacked until its blob has landed, so the largest layer of
`golang:1.26-alpine` fetches for 1.4s and then unpacks, where nothing about the second depends on
the first having finished.

**The digest still gates the last byte.** The host announces progress one byte short of the end
however much has arrived, and only verification releases the rest - so a guest that has taken
everything it was offered still holds an unfinished layer, and an unfinished layer is never placed.
A substituted blob therefore cannot be built on however early it was read. That is the same
guarantee the host's own streaming unpack gets by discarding its directory, arranged to work where
the reader is on the other side of a VM and cannot be reached after the fact.

**It pays, and only because the answer does not come from a file.** A guest reading a blob as it
arrives has to know how far the host has written it. Asked of the shared mount, that answer is about
460ms old, and the guest spent the fetch waiting rather than unpacking - the head start and the
waiting cancelled exactly. Asked over the fault-in socket, which is guest-to-host already and has no
filesystem in it, the answer costs a wakeup:

| stream | cold            | unpack:guest       |
| ------ | --------------- | ------------------ |
| off    | 6.52 5.20 4.94s | 4.764 3.382 3.300s |
| on     | 4.81 4.14 4.13s | 3.074 2.487 2.489s |

The largest layer's own unpack gets *longer* - 2.36s against 1.99s - because it starts before its
bytes have arrived and is paced by the fetch. The phase around it is what shortens, which is the
point: the waiting moved inside the work.

Turning this on starts the fault-in relay for the sandbox, which a local build does not otherwise
need. Off by default because it is new and because a build that waits on a blob is a build that can
wait for ever if the two sides disagree - which they did once, for five minutes, before the progress
marker was made the floor beneath the socket rather than the alternative to it (E688).

Default: off.
