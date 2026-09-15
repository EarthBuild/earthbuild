# Plan: making a fleet move less

A fleet is worth having when the work is bigger than one machine. Whether it *is*
worth having comes down to one quantity: how many bytes have to move before a
step can run. Everything here is aimed at that number.

The prior art is rebuck2, which reached distributed Buck2 over an iroh mesh and
wrote down what it cost. Two of its findings decide the shape of this plan
before any experiment is designed.

**The work is not the problem.** "A warm build's buck2 critical path is ~2
seconds - the minutes are almost entirely distributed-system overhead." So an
experiment that measures compute measures the wrong thing. Every question below
is about movement, waiting, or a round trip.

**Wall-clock cannot answer any of it.** "Same code ran 17m and 46m." rebuck2's
answer was deterministic counters and a regression gate that fails in seconds,
not a stopwatch. This engine already emits the counter that matters -
`fetchedBytes` and `fetchMillis` are in every reply (C.3.1) - so the instrument
exists and has never been read in anger.

## E-F0 - the instrument, before any question

**Question.** Can two variants be compared at all?

An in-process fleet over loopback, data placed asymmetrically on purpose, run
from a test rather than a CI lap. rebuck2's `bench-fleet` "proved locality
(146x) and hot-CAS (584x) before any CI lap", which is the point: a finding that
needs a CI run to see is a finding nobody will iterate on.

**Reports** bytes moved, round trips, and steps delegated. Not seconds.

**Exit.** A harness that fails in seconds when mesh traffic rises above a
computed baseline. Everything after this is measured with it, and nothing before
it is believed.

## E-F1 - what does a fleet move today?

**Question.** Layer-granular or fragment-granular, in practice?

The machinery for the smaller answer exists: `Fragmenter` sends part of a layer
with the manifest as its proof, because a layer's digest authenticates no subset
of itself (E284); `TreeMissing` asks which *directories* are absent rather than
which layers; `𝜈(𝑑)` names a directory by its contents so two bases holding one
directory hold one node. What is unknown is which of these a real delegation
uses.

**Instrument.** `fetchedBytes` per step against the size of the base it stood
on. A ratio near 1 means layers are moving whole.

**Why first.** Every later experiment is a claim about reducing this number, and
none of them can be believed without knowing it.

## E-F2 - locality dispatch

**Question.** Does placement know who already holds the inputs?

rebuck2's largest single win, and it is not close: "Mesh traffic -60x (5-11 GiB
-> 0.07 GiB)", "146x less mesh traffic, 3.2x faster at 4 workers". The mechanism
is to score each worker against the heaviest inputs of the step and prefer the
holder, with "a 500ms patience window (delay scheduling)" - a step waits briefly
for the machine that has its data rather than starting immediately on one that
does not.

This engine places by platform and capacity. Whether it considers holdings at
all is the question; if it does not, this is the highest-value change available
and the number above says by how much.

**Careful.** Patience is a scheduling change and this engine has an invariant
about it: two runs of one build must consider the same machines in the same
order (I12). A delay window must not make placement depend on arrival timing.

## E-F3 - staging, and how many round trips it takes

**Question.** Does a worker fetch its inputs one at a time?

rebuck2 calls this "the single biggest invisible bottleneck": "materialize()
fetched one blob per awaited round-trip (~12/s peer-bound); big substrate crate
forests spent 10-22 min staging before rustc started". The fix was "a
level-by-level tree walk with one batched prefetch per depth, then one batch over
all file blobs and 64-wide concurrent writes".

This engine's fault-in is a request in the direction nothing else travels - the
guest asking the host for a file it touched. It is exactly right for
correctness and is, by construction, one file per round trip. `fills.go` closes
its listener "as soon as it has its connection".

**Instrument.** Round trips per step, and staging start-to-finish. rebuck2 also
found that logging those two timestamps made the stalls "diagnose themselves".

## E-F4 - materialise by link, not by copy

**Question.** How much of a step's setup is spent copying bytes that are
already on the disk?

rebuck2 measured it on 2,220 files / 385 MB: **ext4 306ms -> 68ms (4.5x),
APFS 1,222ms -> 384ms (3.2x), NTFS 1,172ms -> 469ms (2.5x)**. This engine
copies: `DirStore.Materialise` opens each output with `O_CREATE|O_EXCL` and
writes it.

Today's ladder puts ~12ms of the 17.6ms per-action remote overhead in setup and
teardown, so this is aimed at the right half.

**The hazard is named and this engine has it.** rebuck2's two blockers were CAS
blobs not marked read-only, and "`set_exec()` performs `chmod 0o755` on
hardlinked files, stomping the read-only protection and leaking to all
concurrent actions sharing that blob". `engine/guest/copy.go` calls
`os.Chmod(dst, mode)`. A naive switch to hardlinks reproduces that bug exactly.

Their remedies transfer: encode the executable bit in the stored blob's own
permissions (`0o555` vs `0o444`) so nothing has to chmod afterwards, and mark
blobs read-only at store time so a careless write fails with EACCES rather than
corrupting silently. Note the policy they state: "the target is careless actions
... rather than adversarial same-user code."

Also transferable: on APFS `fs::copy` already clones copy-on-write, which gives
mutation safety with no read-only enforcement at all; and hardlinks across a
tmpfs/ext4 boundary fail with `EXDEV`, which decides where an exec directory may
live.

## E-F5 - send what the step will read, before it asks

**Question.** Can staging be one transfer instead of many faults?

The engine already records ω, what each step actually read, because Κ₂ needs it.
That is a per-step list of exactly which files mattered, from last time. Combined
with `Fragmenter`, a driver could send one authenticated fragment holding
precisely those files, and a step would fault on nothing.

This is the one lever here that is this engine's own rather than borrowed: the
observation set was built for cache correctness and happens to be the answer to
"what should I have sent?".

**Kill criterion.** If E-F3's batched staging already collapses the round trips,
this buys the difference between "everything under the base" and "the files
actually read" - which is only worth having where bases are large and reads are
sparse. Measure before building.

## E-F7 - one pool of tokens, not three

**Question.** How many processes does a 32-core machine actually run?

Three layers each choose a width and none of them knows about the others. A
client picks its own - bazel's `--jobs`, buck2's threads. This service bounds
actions at `MaxActions`, which defaults to NumCPU. And inside each action a
compiler fans out again: cargo and rustc size themselves from the machine they
think they are on, which is the whole machine, every time. Thirty-two actions
each running a cargo that believes it has thirty-two cores is not slow, it is
thrashing - and the engine's only current defence is `PidsMax`, which is a
fork-bomb guard rather than a scheduler.

`MaxActions` chose the lesser of two evils and said so: "two pools can
oversubscribe a machine, which is slow, and prefer slow". A jobserver removes
the choice. It is a fifo holding N tokens; anything that wants to run a process
takes one and gives it back. GNU make defined it, and **cargo and rustc already
speak it** - a build that finds `MAKEFLAGS=--jobserver-auth=fifo:PATH` uses the
pool instead of inventing a width.

**The shape is one this engine now has twice.** A per-machine fifo, bound into
each step on the ephemeral mount that already carries the WITH RE socket and
would carry a daemon's, and named in the environment. What is new is that the
engine should draw from the same pool: if `MaxActions` and `Parallelism` and
cargo's `-j` are all tokens from one fifo, the machine's width is one number
held by the kernel rather than three guesses that multiply.

**Settled: one instance per VM.** The tokens stand for cores and the cores
belong to the machine, so the pool belongs there too - which is the argument
that put the execution service in `guestd` rather than the host, and it holds
here for the same three reasons. The VM outlives the build, so a pool scoped to
a build would be rebuilt on a machine whose load did not change. Two builds can
share a warm sandbox, and per-build pools would let each fan out to the whole
machine while believing it was being polite. And in a fleet a worker *is* a VM,
so a per-VM pool and a worker's announced `capacity` are two names for one
number - which is an argument for making them literally one rather than two
settings that can disagree.

**And it dissolves the leak.** A pool that outlives builds is worse to leak
into: a token lost today shrinks every build tomorrow, with nothing to notice
it. But the guest starts and reaps every step itself, already tears its mounts
down, and already counts work in flight for the idle rule with `begin` and
`end`. That makes it the one party able to return a dead step's tokens without
being asked. Holding tokens on the step's behalf stops being a precaution against a
careless build and becomes the only accounting that can be correct, because the
guest is the only party that sees a step end whether or not it meant to.

**Instrument.** Peak process count and run queue depth against the pool size,
for a build of many compiling actions. The failure being measured is not
slowness but collapse: a machine at 30x oversubscription pages, and the wall
clock stops being a function of the work.

**Two hazards, both real.**

A leaked token shrinks the pool permanently. A process killed between taking and
returning one takes a slot out of the machine for the life of the fifo, and a
build that leaks steadily ends up serialised with no error anywhere. Whatever
takes a token must return it from a defer that a kill cannot skip - which in
practice means the engine holds tokens on behalf of a step rather than trusting
the step to hand them back.

And injecting `MAKEFLAGS` into an action changes an environment the *client*
specified, under a key the client computed. That is defensible only because a
token count decides how many processes run and not what they produce - but it is
an environment this engine added to an action it did not write, and the argument
should be stated rather than assumed. A build that embeds its own parallelism in
an output would break it.

## E-F6 - a gate, so none of it rots

rebuck2 ends with a "perf-regression gate: asserts mesh traffic under computed
baseline and driver-local reads beat relay >=2x. Fails regression in seconds."

This engine's equivalent is a ratchet, which it already uses for the corpus and
which already caught a silent improvement going unrecorded. A bytes-moved
ratchet is the same idea pointed at the number this plan exists to reduce.

## What is deliberately not here

Correctness fixes of the kind rebuck2 needed first - it served "17k invalid AC
hits -> 34k client failures" before hardening. This engine started from the
other end: I3 forbids a false hit, I4 gives Λ no error variant, and every blob
is verified against the name it was fetched under (A5, I2). That is the debt
rebuck2 paid down and this engine has not taken on, and it is why the plan can
open with performance instead.

**First.** E-F0, then E-F1. Nothing else is worth arguing about until a fleet
build can say how many bytes it moved.

## E-F1 - first two-machine result (2026-09-15)

Mac driver (arm64, Apple backend, store in the VM) and the x86 box as a worker,
over the LAN. Three defects, in the order they have to be fixed.

**1. The fleet wrapper hid the guest store.** `guestStoreAskers` was asked of
the build's executor, which with a fleet is `fleet.Delegating` - a wrapper that
runs steps and holds nothing. So the driver printed "this executor cannot be
asked what it holds, so this build caches nothing" and transferred no layer into
its own sandbox. Fixed: the question is unwrapped to the local executor, because
which machine runs a step does not move that machine's store.

**2. The blob plane reads a directory that is not the store.** The driver's
keeper is `&fleet.Layers{Root: sb.StoreDir()}`, and on the Apple backend with
`EARTH_STORE_IN_VM` that is a *host* path while the layers are at
`/var/lib/earthbuild/fast/store` inside the VM. A layer a worker produced is
therefore fetched into somewhere no step can materialise from:

```text
materialise the base for Earthfile:67: 3909d5dc… is in this step's base and
this store holds neither a layer nor a declaration for it
  looked for /var/lib/earthbuild/fast/store/layers/3909d5dc…
```

Open. This is the structural one: the *store questions* have already been moved
into the guest one at a time (`StoreHas`, `StoreTree`, `ViewDigests`,
`WhyStaleIn`), and the blob plane is the half that has not followed.

**3. A liveness bound is applied to the work.** `Rendezvous.ask` gives a worker
`defaultReach` = 10s to answer, and `askOver` sets that deadline on the stream
it then reads the *result* off. The comment argues "a live worker answers a
control message in milliseconds", which is true of a control message and false
of an assignment: the worker fetches inputs and runs the step first. A step
longer than 10s fails as `no length: deadline exceeded` and the worker is
dropped as a corpse. Not configurable - there is no env for `Reach`.

Delegating a `FROM rust:1.83-alpine` reproduced it exactly. A bigger constant
reinstates E256; the fix is an early acknowledgement so liveness and completion
stop sharing one timer.

**Not a defect: platform eligibility.** Three runs read as "placement declines
to delegate a saturated driver" until the variable turned out to be the
platform - an unpinned step is the driver's arch, and the amd64 worker cannot
take an arm64 step. With `FROM --platform=linux/amd64` and `EARTH_PARALLELISM=2`
the same build delegated. Rosetta lets the Mac run amd64; it does not let the
box run arm64.

**Bytes moved: still unmeasured.** Every run so far reports `0 B in 0 fetch(es)`
because the worker already held the base. The number this plan exists to reduce
needs defect 2 fixed and a cold worker store.

## E-F1 - the measurement, and the two bounds that collide

Mac driver, x86 box as worker, LAN. `EARTH_STORE_IN_VM=0` so the driver's blob
keeper reads the store it actually has (defect 2 above, routed around rather
than fixed).

**The happy path works, and the central claim holds.** Eight steps on a 7.9 MiB
amd64 base, worker cold:

```text
7 step(s) delegated, 3 here; compute-bound (87%)
  transfer 1.95s for 7.9 MiB in 1 fetch(es), slowest 977ms
  compute 13.886s · queue 0s · wire 77ms
```

One fetch for seven steps. `provision.go`'s "what is present is not fetched" is
true, and a worker that keeps its store between steps is worth what it claims.

**`--platform` does not reach a depending target.** `fromSpec` takes
`opts.Platform` from the `FROM` line being read, so `FROM +common` adopts
nothing from `common` and the node is labelled with the *driver's* architecture.
Placement believes the label, an amd64 worker is ineligible for a step that will
in fact run amd64 content, and the fleet is offered only the steps that name a
platform literally. Pinning every target's `FROM` took the same build from 1
delegated to 7, with nothing else changed.

**Two bounds that cannot both be satisfied.** Repeating the run against the
1032 MiB `rust:1.83-alpine` base:

```text
no worker took Earthfile:51 (the worker stopped answering after 1 attempt(s):
this is not a well-formed assignment: no length: deadline exceeded)
0 delegated, 6 local        # and the worker's store: 4.0K
```

A cold worker must fetch the base before it can run anything, and the whole
assignment round is bounded at 10s (defect 3). A GB does not cross a LAN in ten
seconds, so the worker is declared dead mid-fetch, its store stays empty, and
the *next* assignment finds it just as cold. **A worker whose base does not fit
inside the liveness bound can never warm up.** Nothing in the fleet recovers
from this on its own; it is not a slow path but an absorbing state.

That is the whole result. The mechanism is sound and the bounds are wrong.

**Unexplained, low confidence.** One run reported `FROM rust:1.83-alpine
NON-DETERMINISM: nothing in the key changed and the output did` across the
store-in-VM boundary - the same pinned digest unpacked to two layer IDs. It may
be an artefact of moving the store rather than of the unpack. Worth a look
before it is quoted as a determinism failure.

## E-F1 - the number, at last

With F1 (liveness split from completion) and the fault-in accounting in, the
same build that reported nothing reports this - Mac driver, cold x86 worker,
1032 MiB `rust:1.83-alpine` base, six steps:

```text
4 step(s) delegated, 6 here; transfer-bound (99%)
  transfer 1m49.385s for 1.0 GiB in 1 fetch(es), slowest 54.686s
  compute 0s · queue 0s · wire 17ms
```

**The base crossed once.** That is E-F1's question answered on a base worth
moving, and it is the number E-F6's ratchet goes on.

It is also the case for everything in the v1 plan after F2. A gigabyte at
~10 MiB/s of useful throughput against steps that cost a second each is a fleet
that is 99% transfer-bound: correct, and useless. E-F2's locality dispatch,
E-F3's batching and E-F5's prediction all exist to move that number, and none of
them could be evaluated while it read zero.

Two things still visible in that run and not yet chased:

* `a worker would not take Earthfile:51 (1 of 2 input(s) ... some blobs could
  not be fetched)` - only four of ten steps were delegated;
* `compute 0s` beside four delegated steps, which no step costs.

## F3 verified - an unpinned Earthfile now delegates

The same eight-step build with `--platform` on the base **only**, which is how
anybody would actually write it:

```text
3 step(s) delegated, 1 here; compute-bound (83%)
  transfer 26ms for 849.2 KiB in 1 fetch(es), slowest 26ms
```

Before the inheritance fix that build delegated one step - the `FROM` itself,
the only node that named a platform. Nothing else changed.

**Next, and it is now the largest remaining refusal.** Every two-machine run so
far has carried one of these:

```text
a worker would not take Earthfile:35 (materialise the base for : 3909d5dc… is
in this step's base and this store holds neither a layer nor a declaration
for it)
```

A step is assigned before the base it stands on has arrived. `primeAll` is meant
to prevent exactly that, so either it is not covering this case or the
assignment does not wait on it - and with F1 in, waiting is now expressible.
