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

### `EARTH_FLEET_DISCOVER`

Whether a fleet uses relays and endpoint discovery to reach machines it cannot dial directly. Set to
any non-empty value to turn it on. Off by default: it was on for one increment, and a worker given
the driver's address - a path that had been working - joined and was then given no work (E505).

Default: off.
