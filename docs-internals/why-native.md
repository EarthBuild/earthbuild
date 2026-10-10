# What the native engine does that buildkit did not

A running list, added to as things are established. **Each entry says what kind
of claim it is**, because the interesting ones are measured and the tempting
ones are not:

* **measured** - there is a number in this repository and where it came from.
* **structural** - it follows from the design; no measurement needed or possible.
* **claimed** - believed, not yet demonstrated. These are the ones to attack.

## Correctness

**A cache hit is verified, not assumed** *(structural)*. I3 forbids a false hit
and I4 gives the lookup no error variant: a tier either returns a result whose
layers the store still holds, or it misses. `Lookup` refuses an entry whose
result has been collected, so a store that lost a layer reruns the step instead
of serving a key for something that is gone.

**The engine says when a step is not reproducible** *(measured)*. Nothing in the
key changed and the output did is a sentence buildkit has no way to form,
because it has nothing to compare against. This engine fired it today on a
pinned image digest across two store configurations, and it was true.

**An observed-input tier** *(structural)*. Κ₂ keys a step on what it actually
read rather than on what it was given, so an edit to a file no step opened does
not invalidate anything. Buildkit's cache is keyed on inputs as declared.

## Distribution

**A fleet that beats one machine** *(measured)*. Two attempts at a distributed
buildkit failed. This engine builds 64 steps in 49.58s across a Mac and an x86
box against 96.07s on the Mac alone - **1.94x from two sixteen-core machines**,
twice each arm. The arithmetic of when that holds, and the four things it needed,
are in [plan-fleet-experiments.md](plan-fleet-experiments.md); it does not hold
on a small build with a large base, and that is written down there too.

**Workers need no inbound address** *(structural)*. A worker dials its driver
and the connection carries assignments back, so a machine behind any NAT can
join. Blobs travel the same way.

**A step fetches the part of a base it reads** *(measured, partly)*. Two files
of 5,410 for `go version`, 1,752 for a cold `go build`. The machinery is built
and instrumented; what it is worth end-to-end is E-F5 and not yet answered.

## Platform

**macOS is a first-class build host** *(measured)*. Steps run in an Apple
`container` VM, amd64 runs through Rosetta, and the layer store lives on the
guest's own device because APFS is case-insensitive and would otherwise collide
two files in a layer differing only in case. Rosetta ran 64 amd64 steps in 95.9s
against 95.5s native on the x86 box.

**No daemon to be out of step with** *(structural)*. `buildkitd` is a
long-running process holding the cache, and a fork of it has to be deployed
before a build can use it. Here the engine is the binary that runs the build.

## Interfaces

**It is a remote-execution service** *(measured)*. buck2 and bazel build against
it over REAPI, which means their caches and this one are the same cache.

## Diagnostics

**Errors name the thing and what to do** *(measured, everywhere)*. A worker that
emptied its store says the filesystem is full and which knob is set too high; a
missing base element reports the room the store has; a refusal carries whether
anything was even asked. Most of this file's own findings were found by reading
a message that had been written to be read.

## Not yet true, and worth writing down

* **Faster than buildkit on a cold build** - unestablished either way here.
* **A fleet that pays on a small build** - shipping a 1 GiB base over wifi
  costs 59s, and no transport work changes that. Only prediction (E-F5) and
  locality (E-F2) move it.
* **More than two machines** - nothing here has been run on three.
