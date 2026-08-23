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

Default: found next to `earth-native`.

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
