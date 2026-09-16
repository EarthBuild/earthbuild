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

## The refusals were a full disk, and then they were not

Two attributions of the recurring `a worker would not take …` refusal were
wrong before the evidence was read properly. It was not a priming race, and it
was not the collector ordering backwards (a layer a worker fetches *is* in its
index - `OpenIndex` fills from disk). The x86 box's root filesystem was at 100%
with 5.6 G free against `defaultStoreFree` of 8 GiB, so the guest agent emptied
the worker's store every boot:

```text
earth-guestd: removed 2 layers, freed 1.0 GiB, 0 layers and 0 B left
```

Nothing joined that to the step which then failed for a missing layer, on
another machine, in another log. Both now say so - `Report.Short`, and the free
space carried in the refusal itself, which reaches the driver because the
refusal does.

**Re-measured on the box's second disk** (185 G free), with the guest agent
collecting nothing:

```text
6 step(s) delegated, 6 here; transfer-bound (99%)
  transfer 2m4.41s for 1.0 GiB in 1 fetch(es), slowest 1m2.191s
  compute 0s · queue 0s · wire 43ms
```

Six delegated against four on the full disk, and the worker keeps its 1.1 G.

**Still open, and now the top item.** `compute 0s` beside six delegated steps is
not a slow fleet, it is six refusals: `DurationMillis` is only set by a reply
that ran something, and the account counts a refusal as delegated. All six were
refused with

```text
1 of 2 input(s) for a delegated step: some blobs could not be fetched
```

so the fleet fetched a gigabyte, refused every step, and the driver did all the
work. Which of the two inputs could not be fetched is not yet known.

## E-F1 - the fleet builds the build

With declarations movable, the same Earthfile on the same two machines:

```text
6 step(s) delegated, 0 here; transfer-bound (76%)
  transfer 1m49.746s for 1.0 GiB in 1 fetch(es), slowest 54.873s
  compute 33.256s · queue 0s · wire 36ms
```

**Every step ran on the worker and none were refused.** The day's progression,
same workload throughout:

| State                | Delegated | Ran here | Compute recorded |
| -------------------- | --------- | -------- | ---------------- |
| before F1            | 0         | all      | -                |
| F1, on a full disk   | 4         | 6        | 0s (all refused) |
| F1, second disk      | 6         | 6        | 0s (all refused) |
| declarations movable | 6         | 0        | 33.256s          |

`compute 0s` was never a slow fleet: `DurationMillis` is set only by a reply
that ran something, and the account counts a refusal as delegated. Six
delegated steps with no compute were six refusals, and the driver quietly built
everything itself.

**Next number to attack.** 1.0 GiB in 1m49.746s is about 9.6 MiB/s, which is an
order of magnitude under what this LAN does. Transfer is 76% of the build and
the base is fetched once, so there is nothing left to save by fetching less
often - the remaining win is in the transfer itself (E-F3's batching) and in not
needing the whole base at all (E-F5's prediction).

## The transfer is not slow; the link is - and the first number was the wrong unit

Two corrections to the paragraph above, which read `1.0 GiB in 1m49.746s` as
"about 9.6 MiB/s, an order of magnitude under what this LAN does".

**Both machines are on wifi.** Not a gigabit LAN: the driver is 802.11ax on
5 GHz and the worker answers on `wlp5s0`. Raw `scp` of 500 MiB over that path,
compression off, measures **22.1 MiB/s**. That is the ceiling, and it was
asserted rather than measured.

**`transfer` is a sum over steps and was divided by one payload.** Six delegated
steps each report their own `FetchMillis`, and five of them spent it waiting on
the uplink lock for the one fetch that was actually happening - `uplink` counts
that wait as transfer time deliberately, so a queue is not billed to the network
(E336). The single fetch is `slowest`:

| Reading                 | Time   | Rate       |
| ----------------------- | ------ | ---------- |
| slowest single fetch    | 57.5s  | 17.8 MiB/s |
| summed across six steps | 115.0s | 8.9 MiB/s  |
| raw scp, same path      | 22.7s  | 22.1 MiB/s |

So the fleet moves a gigabyte at about **80% of what scp manages** on the same
link. There is no factor of three in the transport and no factor of ten
anywhere; packing is not the cost either, measured at 1.066s for 847 MB
(757.9 MiB/s).

**What this redirects.** A build that is 77% transfer-bound here is not paying
for a bad transport, it is paying to move a 1032 MiB base across wifi to save
33s of compute. Nothing in E-F3's batching can beat a link that is already
80% used. The remaining wins are the ones that move **less**: E-F5's prediction
(fetch the tenth of a base a step reads) and E-F2's locality dispatch (put the
step where the base already is). Those were always the interesting experiments;
this says they are the only ones.

## GitHub: the data plane never leaves the relay

The place this most needs to work, and the first place the instrument could
say anything about it. Three runners, driver plus two workers, `fleet-e2e`.

Before today the workflow passed and reported `transfer 0s for 0 B in 0
fetch(es)` - the fault-in accounting gap. With that fixed:

```text
4 step(s) delegated, 1 here; compute-bound (82%)
  transfer 6.124s for 7.9 MiB in 1 fetch(es), slowest 6.124s
```

7.9 MiB in 6.124s is about 1.3 MiB/s between two machines in one datacentre.
The route says why:

```text
fetched from 0ab2a4ec… over relay:https://use1-1.relay.n0.iroh-canary.iroh.link./,
  ip:74.235.90.91:28737 sent 0 B received 0 B
```

**A direct path is validated, multipath is negotiated, and it carries nothing
in either direction.** The relay does all of it, and which relay varied by run:
`usw1`, `use1`, and once `aps1`, which is Mumbai, for two runners in the
United States.

Three things were tried and are recorded because two of them failed:

* **Waiting for hole punching before transferring.** Works - the direct path is
  validated on every connection - and changes nothing: 6.213s, 8.226s, 9.095s
  against 6.124s without. Off by default, mechanism kept.
* **Reading `BytesSent` to see which path carried the transfer.** Wrong
  counter: a fetcher is a receiver, so its send counter is the size of its
  request whatever path carries the reply. Both directions are reported now,
  and they agree - the direct path is idle.
* **Suspecting multipath was not negotiated.** It is. The connection has two
  validated paths, a selector that documents a preference for direct over
  relay, and no bytes on the direct one.

**What to try next**, in order of how much is under this engine's control:

1. Dial the blob connection at the peer's validated direct address with no
   relay in the endpoint address at all, so there is nothing to fall back to.
   The address is known - it is in the route line above.
2. Pin the relay map to a region near the fleet, so the fallback is at least
   not Mumbai.
3. Ask upstream whether migration is meant to happen here.

Worth stating plainly: on GitHub this is a bigger lever than prediction or
locality. The build is 82% compute-bound *because* it is small; a real base
over a 1.3 MiB/s route would not be.

## Splitting one number into two ended the argument

Three attempts to make GitHub's fleet transfer faster all missed, because
`transfer` covered reaching a peer and moving bytes with one figure. Two
figures, one run:

```text
fetched from fb05f586… over ip:57.151.129.40:37969
  (reached in 3363ms, read in 302ms)
```

7.9 MiB in 302ms is 26 MiB/s. The transport was never slow. Measured both ways
on the same workload:

| Route  | Reached | Read   | Rate       |
| ------ | ------- | ------ | ---------- |
| relay  | 403ms   | 1394ms | 5.7 MiB/s  |
| direct | 3363ms  | 302ms  | 26.2 MiB/s |

So each route wins one half, and both of the obvious answers are wrong. The
relay really is 4.6x slower to read from - the first theory was right about
that - but *waiting* for a direct path costs a flat three seconds, which is
more than the relay loses on any fetch this size. Forcing direct made the
build slower; leaving it on the relay left 4.6x on the table.

**Neither, then.** The first fetch takes whatever path is up and the punching
happens behind it, so by the second fetch a direct connection is waiting. A
build with one fetch is exactly as fast as before; a build with many pays the
punching once, which is the shape of every real build - a base, then everything
standing on it. CI: 5.941s, the best of nine runs, with no added latency.

**What is left is not in the transport.** Reaching a peer costs 0.4s to 3s
before anything moves, paid per peer. On a small build that is most of the
fleet's cost and it is fixed rather than proportional, which is the signature
this project has learnt to recognise (E335, E337). Warming the blob connection
at join time, while the driver is still planning, would take it off the critical
path entirely.

## Taking the setup off the critical path

Reaching a peer costs more than reading from it, and none of it is proportional
to the bytes. The holders are known one line after an assignment arrives, which
on a prime is before any step needs them, so that is where the connections are
opened now - in the background, nothing waiting on them.

Same workload, same 7.9 MiB, across the day:

| State                    | Transfer reported    | Compute-bound |
| ------------------------ | -------------------- | ------------- |
| this morning             | `0s for 0 B`         | 66%           |
| fault-in accounted       | `6.124s for 7.9 MiB` | 82%           |
| connections opened early | `417ms for 0 B`      | 92%           |
| priming accounted        | `433ms for 7.9 MiB`  | 90%           |

**The third row is the interesting one.** Opening connections early worked, and
hid the transfer: the base now arrives during the prime, a prime's reply was
discarded, and a build that fetched 7.9 MiB reported moving nothing. E-F0's
failure exactly, reintroduced by making the fleet faster - and a number that
reads zero only when things go *well* is worse than one that always reads zero,
because the first time it is believed.

Counted as transfer and not as a delegated step: a build with four steps and two
primes reporting six steps is an account that quietly does not add up (E270).

Fourteen times less transfer on the critical path for the same bytes, and the
instrument still says what crossed.

## The two environments have opposite bottlenecks

The same engine, the same split of reaching from reading, on the two fleets this
project has:

| Fleet                    | Reached | Read    | Payload | Rate       |
| ------------------------ | ------- | ------- | ------- | ---------- |
| GitHub, three runners    | 403ms   | 302ms   | 7.9 MiB | 26.2 MiB/s |
| LAN, Mac driver plus box | 13ms    | 58418ms | 1.0 GiB | 17.5 MiB/s |

**On GitHub the cost is getting to the machine; on the LAN it is the wire.** The
work that made the CI fleet fourteen times cheaper - opening holders before a
step needs them - is worth thirteen milliseconds here, because a worker told
where its driver is dials it directly and there is nothing to discover. And the
wifi link is already carrying 79% of what `scp` manages over it, so there is
nothing left in the transport either.

That is the honest state of "can a fleet beat one machine". It can, when the
compute it moves is large against the base it has to ship. On this LAN that
means a base of 1 GiB buys 34s of compute across one worker, which it does not:
`transfer-bound (77%)`, 92.09s of wall clock against 85.69s this morning, inside
the noise.

**So the remaining work is all about moving less**, and it is the same list it
was before the transport was ruled out:

* E-F5, prediction: fetch the tenth of a base a step reads. The machinery exists
  and is what moved the gigabyte; what is missing is a profile good enough to
  predict from.
* E-F2, locality: put the step where the base already is, which is what took
  rebuck2's mesh traffic down sixty-fold.

A wired link would raise the LAN ceiling and is worth having for measurement,
but it changes which side of the line this workload falls on rather than
removing the line.

## F4 - a Mac can drive a fleet

Every measurement above used `EARTH_STORE_IN_VM=0`, and that is the setting
that is wrong. Darwin keeps the layer store on the guest's block device by
default for a correctness reason: APFS is case-insensitive, so two files in a
layer differing only in case collide on the shared mount.

With the store where it belongs, `fleet.Layers` read a host directory holding
nothing, so a driver held the base of its own build and could offer none of it.
Now:

```text
3 step(s) delegated, 0 here; compute-bound (99%)
  transfer 28ms for 849.2 KiB in 1 fetch(es), slowest 28ms
  compute 5.21s · queue 0s · wire 16ms
```

No `caches nothing`, no refusals, every step on the worker.

The transport was already there. `SAVE IMAGE` has carried a layer out of such a
store since E556 - a second `container exec`, the guest binary in a mode that
does one thing, and a pipe - as an OCI blob. The fleet speaks a different pack,
so this is the same journey in that format and the same journey back, with
`fleet.Layers` doing the packing at both ends rather than a second encoder for
one wire format.

**Not yet exercised:** a base of any size through this path. The pack is
buffered whole in host memory, which is what `fleet.Layers.Get` already did, but
849 KiB and 1 GiB are different questions about a pipe.

## The baseline was crippled, and the honest comparison is brutal

Every fleet run above used `EARTH_PARALLELISM=2` on the driver, because without
it nothing is delegated: placement is least-loaded-first and a driver with
sixteen cores and six steps takes all six. That setting was necessary to
exercise the fleet and it makes the comparison meaningless, which was not said
until now.

The same six steps, same base, on this Mac alone at its own parallelism:

| Arrangement                     | Wall clock |
| ------------------------------- | ---------- |
| one machine, 16 cores           | **6.48s**  |
| fleet, worker warm              | 33.50s     |
| fleet, worker cold (1 GiB base) | 95.58s     |

**The fleet is five times slower warm and fifteen times slower cold**, and no
amount of transport work changes that: shipping a 1032 MiB base over 17.5 MiB/s
of wifi costs 59 seconds, and the entire build is 6.5 seconds of work.

That is not a defect. It is the arithmetic of the thing, and it is worth writing
down because every experiment above was implicitly asking the wrong question.
The right one is where the line falls:

```text
one machine:  ceil(steps / cores) x duration
fleet:        that, less what a worker takes, plus base_bytes / link
```

With sixteen cores, 5.4s steps and a 1 GiB base over wifi, a second machine
does not repay its own base until the build is around a thousand steps deep.
Halve the base or wire the link and that number falls by the same factor;
neither changes the shape.

**What follows for the endgame.** A fleet earns its keep when the machine is
saturated and the base is small against the compute - which is what rebuck2's
21-hour jobs were. Two things move the line and they are the two experiments
left: E-F5's prediction, which makes the base cost a tenth of what it does (a
step reads two files of 5,410 for `go version`, 1,752 for a cold `go build`),
and E-F2's locality, which stops the base being shipped again for every chain.
Both attack `base_bytes`, and that is the only term this engine controls.

## The fleet moved the work; it did not share it

`EARTH_PARALLELISM` is one semaphore over **every** step, delegated ones
included - so the fleet runs above were not merely measured against a crippled
baseline, they were themselves crippled: two steps in flight while a worker sat
with thirty-two free slots.

Removed, with a workload that saturates the driver on its own - 64 steps, more
than either machine has cores, and a 7.9 MiB base so transfer is not the story:

| Arrangement                | Wall clock |
| -------------------------- | ---------- |
| one machine (Mac, Rosetta) | 95.90s     |
| driver plus worker         | 95.47s     |

A dead heat, and the summary says why: **`64 delegated, 0 local`**. The driver
ran nothing at all.

**Because a Mac cannot be eligible for an amd64 step.** Placement applies
emulation as a *second pass*, considered only when no machine can run a step
natively, and the argument for that is in the code: "emulated work runs on the
order of a hundred times slower, because every instruction goes through an
interpreter". So the box was always eligible and the Mac never was, and the
fleet substituted one machine for the other rather than adding them.

**The argument does not hold for Rosetta.** The same 64 amd64 steps: 95.90s on
the Mac through Rosetta against 95.47s native on the x86 box. Not a hundred
times; not two. The rule is right for qemu-class emulation and silently
excludes the only second machine this fleet has.

That is the finding. A heterogeneous fleet of one arm64 Mac and one x86 box can
only ever *move* a single-platform build, never share it, until placement can
weigh a cheap emulator against a busy native machine. E-F2 and E-F5 attack
`base_bytes`; this attacks the term before it, which is whether a machine is
allowed to help at all.

## A translator is not an interpreter, and then: the concurrency ceiling

Rosetta is admitted to the first pass, and the Mac joins:

```text
32 step(s) delegated, 32 here; compute-bound (99%)
  transfer 0s for 0 B in 0 fetch(es)
```

A perfect split, from `64 delegated, 0 local`. And the wall clock barely moves:
95.90s on one machine against 92.27s on two.

**Because a fleet cannot run more steps at once than the driver has cores.**
`Scheduler.Parallelism` defaults to the *driver's* `runtime.NumCPU()` and gates
every step through one semaphore, delegated ones included. Two machines of
sixteen cores each therefore run sixteen steps at a time, not thirty-two: the
split is real and both machines are half idle.

| Arrangement           | Wall   | Waves of 16 |
| --------------------- | ------ | ----------- |
| one machine, 16 cores | 95.90s | 4.0         |
| fleet, 32/32 split    | 92.27s | 3.8         |

Sixty-four steps, four waves either way. Adding a machine added no concurrency,
which is the one thing adding a machine is for.

That is the last structural blocker, and it is the same field that made every
earlier comparison meaningless from the other direction. The limit means two
things that need separating: how much work *this machine* takes at once, which
is a property of this machine, and how much work the *build* has in flight,
which is a property of the fleet. `Delegating.Room` already exists for the
first.

## The fleet beats one machine

Two arms, twice each, 64 steps on a 7.9 MiB base, nothing constrained:

| Arrangement           | Runs         | Mean   | Spread |
| --------------------- | ------------ | ------ | ------ |
| one machine, 16 cores | 95.90, 96.24 | 96.07s | 0.34s  |
| Mac plus x86 box      | 68.32, 65.15 | 66.73s | 3.17s  |

**1.44x**, and both arms are tight enough that it is not noise. `32 delegated,
32 here` on both fleet runs.

That is the question this plan opened with, answered the right way round for the
first time. It needed four things, and only the last of them was about moving
bytes:

* a worker that is not dropped for being busy (F1);
* a step labelled with the platform its base is, so a machine can be eligible
  for it (F3);
* a translator admitted to placement's first pass, so the Mac is a machine at
  all on an amd64 build rather than a spectator;
* a build allowed as many steps in flight as the fleet has cores, rather than as
  many as the driver has.

**The gap from 2x is the next question.** Two machines of sixteen cores and a
perfect split should be two waves, not the ~2.8 this implies. Stragglers,
imbalance in what Rosetta and the x86 box each cost per step, or a tail where
one machine finishes and the other still has work - `compute` says 24.1s per
delegated step against a 96s/4-wave single-machine figure that implies the same,
so the per-step costs are close and the loss is in the shape of the schedule
rather than in either machine.

## 1.94x, and the missing half was the harness

The gap from 2x was mine. The driver waits for its fleet before running
anything - §4.7.3 requires a schedule computed against a known inventory - so a
worker that joins late delays the whole build. This harness slept ten seconds
before starting one, and the worker then took its own time to boot and join.

Started as soon as the driver publishes its address instead:

| Arrangement           | Runs         | Mean   | Speedup   |
| --------------------- | ------------ | ------ | --------- |
| one machine, 16 cores | 95.90, 96.24 | 96.07s | -         |
| fleet, worker late    | 68.32, 65.15 | 66.73s | 1.44x     |
| fleet, worker ready   | 50.30, 48.86 | 49.58s | **1.94x** |

Two machines of sixteen cores, 1.94x. There is no meaningful gap left to
explain on this workload: the split is even, the per-step costs match, and what
remains is the one wave neither machine can avoid.

**The measurement to keep is the middle row, not the bottom one.** A fleet whose
workers join when the build starts is a fleet in a laboratory. In CI the runners
start together and the wait is real; on a desk the worker is a daemon that was
already there. Both are legitimate and they are seventeen seconds apart, so a
result quoting either without saying which is not a result.

## E-F5 - a prediction is worth its round trips

Every run above bumped the step's body, so no step ever had a history and the
cache line said `6 unpredicted` each time. Run the *same* step twice with
`--no-cache`, cold worker both times, 1032 MiB base:

| Run             | Bytes   | Fetches | Transfer | Wall   |
| --------------- | ------- | ------- | -------- | ------ |
| no profile      | 1.7 MiB | 3       | 18.534s  | 39.43s |
| profile, first  | 1.1 MiB | 1       | 2.231s   | 23.31s |
| profile, second | 1.1 MiB | 1       | 2.348s   | 22.95s |

**The bytes barely move and the time falls eightfold**, which is the whole
argument for priming: a fault is a round trip, and three of them cost 18.5s
where one batch costs 2.3s. E292 said so and this is the number.

Also worth recording: 1.7 MiB against a 1032 MiB base, on a worker that had
never seen it. Earlier runs of this same Earthfile moved the whole gigabyte -
not because prediction was off but because the base contains a declaration, the
driver could not serve one, and the worker fell back to fetching whole layers.
Fixing that turned 1.0 GiB into 1.7 MiB before any prediction was involved, and
the two are easy to confuse: **the lazy path only pays when it is reachable at
all.**

One run without a profile, two with, and the without cannot be repeated without
clearing the profile store - so the 18.5s is a single measurement. The fetch
counts are structural and are the part to believe.

## E-F2 - locality, found dead

`fleet.prefer` implements holder-first ordering and its own comment calls it
"the single most consequential ordering in the fleet". **It is called from
tests and from nowhere else.** Placement sorts by load and has never been able
to ask who holds anything.

A chain is where that costs. Eight steps of 40 MB, each standing on the last,
across two machines:

```text
4 delegated, 4 here; transfer-bound (86%)
  transfer 19.446s for 167.9 MiB in 4 fetch(es)
  compute 3.014s
```

The chain alternated and shipped a layer at every handoff - 167.9 MiB moved to
do three seconds of work.

**Two attempts, both wrong, and the second is reverted.**

The first asked the executor whether a worker held a layer. Placement happens
*before* anything runs, so the layers do not exist and the stack map is empty;
and on a VM backend the question is an exec into the sandbox, which put I/O on
the placement path and stopped a build with a step stuck for six minutes.

The second asked the schedule instead - where each input will be *produced*,
which is known because the walk is topological and pure, as §4.7.3 requires.
That is the right question. It also needed the price recalibrating: a whole
step sent every child of a shared base onto one machine and two fleet tests
reported nothing crossing the network at all, so loads are doubled and the
price is one half-step, a holder winning only a tie.

And with it in, **the chain hangs on a fleet**: work goes local, the worker
sits idle at 14 MB, and a local step stalls for six minutes with no progress.
Single-machine builds are unaffected - the same chain runs in 6.35s with
locality and 8.18s without - so it is the interaction with delegation and not
the placement itself.

Reverted. A build that does not finish is worse than one that ships a layer it
need not, and the finding is worth more than the patch: **the ordering this
fleet was designed around has never run.**

## The chain hang, traced: a serve and a step contend for one sandbox

Goroutines on a build that had made no progress for six minutes: three stuck in
`fleet.writeFramed`, each writing `0x2828288` bytes - one 40 MB chain layer
apiece.

**`serveBlobStream` discarded its context and set no deadline**, so a write to a
peer that stopped reading blocked for ever. Fixed, twice: the first attempt took
the bound from the serving context, and `fleet.Driver` serves under a cancel
with no deadline, so it set nothing and fixed only a test whose context happened
to have one. The serve carries its own bound now, per blob, five minutes.

The bound fires - `serve e2a6e5cd…: write a message: deadline exceeded` - **and
the build still stalls.** So the unbounded write was a real defect and not this
one's cause.

**Where the evidence points.** The stuck step runs on the driver, in the Apple
VM. The driver is also serving blobs, and with the store inside the VM
(`guestLayers.Get`) serving one means `container exec` into *that same sandbox*,
whose stdio the guest protocol already holds. That is the constraint `PackLayer`
was written around in the first place: "the protocol holds the only stdio pair
`container exec` gives".

**Tested, and wrong.** Packing a 40 MB layer out of a live sandbox takes 0.289s
with it idle and 0.279s while a step is running in it. `container exec` into a
busy sandbox does not contend with the protocol's stdio at all, so the mechanism
this paragraph proposed does not exist.

That is three theories for one hang - an ordering bug, a store contention, and
now this - and the evidence that survives all three is narrow: the driver's
serve blocked writing three 40 MB layers, the worker never reported fetching
anything, and a local step waited. The next thing to collect is the *worker's*
goroutines, which have not been looked at once; every dump so far has been the
driver's, and a mutual wait is invisible from one side.

Locality stays reverted meanwhile. The placement is right and something under it
is not, and shipping the first while hunting the second would mean every chain
build risks a stall.

## Both sides of the stall, at last

The worker's goroutines during the stall, which had never been collected:

```text
fleet.(*runnerCfg).provision -> uplink -> readFragment -> readFramed
```

So the worker is not idle and never was: it is **blocked reading**, holding the
uplink mutex that serialises its transfers, while `replyRunning` beats away
telling the driver it is alive. The driver, at the same moment, is blocked in
`writeFramed` on three 40 MB writes.

**A fragment request answered with a whole blob is the suspect.** `readFragment`
reads a one-byte flag and refuses anything that is not a fragment - correctly,
because answering "here is the whole layer" to "give me these paths" would be
I10's accepted-and-ignored. What it does not do is drain what the sender has
already committed to writing. The driver's `guestLayers` does not implement
`fragmenting` at all, so a driver whose store is inside the VM can only ever
answer a fragment request with a whole layer.

That is a specific, checkable claim and it is not yet checked. What is
established is the shape: **both ends are waiting on the same transfer**, which
no amount of reading one side's stack could have shown.

**Where this leaves the fleet.** The serve is bounded now, so the driver frees
itself after five minutes rather than never - the build still fails, but it
fails. Locality stays reverted. The next step is to give `guestLayers` a
`Fragment`, or to make a whole-blob answer to a fragment request something the
asker can consume, and the choice between those is the interesting part: the
first makes the lazy path work for a VM-backed driver, which is the point of
F4, and the second only stops it hanging.

## Found: a fragment request answered with a whole layer

`serveOneBlob` fell through to the whole-blob path when the store could not
fragment. `readFragment` refuses anything that is not a fragment - correctly,
since accepting "here is the whole layer" in answer to "give me these paths"
would be I10's accepted-and-ignored - and returns after one flag **without
draining what the sender has already committed to writing**.

Enough of those and the connection's flow-control window is gone. The sender
cannot write even the first byte of the *next* answer and the asker waits for it
for ever: both ends blocked on the same transfer, one in `writeFramed` and one
in `readFragment`. That is what the two dumps showed, and what neither showed
alone.

A driver whose store is inside the VM can never fragment, so this was not an
edge case. It was every lazy fetch from a Mac.

**Answered as absent now**, in one byte, which is a word the protocol already
has and is what it means to this asker: try the next source, then the whole-layer
path, which is the fallback I11 asks for.

The chain that hung for ever, with locality restored:

| Arrangement           | Moved     | Wall   |
| --------------------- | --------- | ------ |
| no locality           | 167.9 MiB | 32.27s |
| locality, before this | -         | hung   |
| locality, after this  | 5.7 MiB   | 9.41s  |

**29x less moved and 3.4x quicker**, on the shape a fleet is worst at. E-F2 is
no longer dead code, and `prefer`'s own claim about itself turns out to have
been right all along.

And the fan-out is unharmed, which is the half-step price being calibrated
rather than lucky: the 64-step build still splits `32 delegated, 32 here` and
runs in 51.50s against a 49.58s mean before locality and 96.07s on one machine.
A chain that stays put and a fan-out that still spreads are the two things this
ordering has to do at once, and it does both.

## A real target: this repository's own `+all-binaries`

Five Go cross-compiles from one base - the shape a fleet should be best at.

**It did not build at all, on any machine.** `GOOS=windows go build ./...`
fails: three call sites in `engine/exec` use `unix.Flock` and `syscall.Stat_t`
directly, so `+earthly-windows-amd64` dies and takes `+all-binaries` with it.
Nothing in this repository cross-builds for windows, which is why no test caught
it. Fixed with the platform files the package already uses elsewhere.

With that fixed it builds on the x86 box in 7.5s, and over the fleet:

```text
4 step(s) delegated, 43 here; compute-bound (99%)
```

**Four of forty-seven, and not the ones that matter.** The `go build` at the
heart of every binary carries

```text
--mount type=cache,target=/go/pkg/mod,sharing=shared,id=go-mod
--mount type=cache,target=/root/.cache/go-build,sharing=shared,id=go-build
```

and `ir.Op.OnInvokerOnly` pins any step with such a mount: *"it needs a cache
mount, whose contents live on this machine"*. That is correct - a cache mount
is machine-local state by definition, and an assignment has no way to carry it -
and it means **the expensive half of this repository's own build can never be
delegated.** Thirty-four cache mounts in one Earthfile.

It is also why the numbers are small: the mounts survive `--no-cache`, so the
compiler never actually recompiles and a 131-step "cold" build takes eight
seconds. The workload is not cold and cannot be made cold without discarding a
cache the build is designed around.

**What this says about the fleet.** Every experiment above used steps with no
mounts, and that was not a simplification - it was the only shape a fleet can
take. A fleet helps a build whose parallel work is *self-contained*; it cannot
help one whose parallelism is bought with machine-local caches. Which of those
a real build is, is now a question worth asking of each target rather than
assuming.

## Cache mounts can cross, and now do

A cache mount was the one thing pinning this repository's own build to one
machine. It no longer is.

The argument is one the engine had already made and not followed: a cache is
bound *over* the step's filesystem, so what goes into it is excluded from the
layer by construction, and Κ₁ hashes the mount's declaration and never its
contents. **Every cache hit ever served asserts that what is in there cannot
reach the result.** A worker with its own directory of the same name therefore
produces the same layer, more slowly the first time - or the local cache was
already unsound and had been for every hit.

The declaration crosses and the contents do not, which is the half that makes it
safe rather than permissive: a step run without a mount it declared writes into
its layer what it would have discarded (E433). Three mounts still refuse, each
for its own reason - a secret is not on the wire, a persisted cache is captured
and so *is* the result, a sandbox path names one machine's disk.

End to end, two steps sharing one cache id:

```text
2 step(s) delegated, 1 here; compute-bound (91%)
```

and on the worker, `ef-store/mounts/demo` - a directory it made under the name
the build gave, holding what the step wrote there.

**What it does not yet buy.** `+all-binaries` still runs its `go build` steps on
the invoker: they are eligible now, and placement keeps them anyway because
locality and load say so. On that build it is probably right - the driver's
cache mount is warm and a worker's is empty, and a cold cache is exactly what
the mount exists to avoid. Placement models where a *layer* is and not where a
*cache* is warm, so it cannot yet tell the difference between a worker that has
built with `go-build` before and one that has not.

That is the next piece of the same idea: a warm cache mount is a kind of
locality, and this engine already knows how to weigh one.

## E-F4: is the claim true? Measuring `/go/pkg/mod`

`--portable-except` is an assertion the author makes and the engine cannot
check (§3.3c). That makes the recommended settings in
`docs/caching/sharing-caches.md` the load-bearing part, and they were written
from each tool's documentation. This measures one of them.

**Method.** Populate the same module set twice, into two `GOMODCACHE` roots
chosen to have *different path lengths*, and compare the sha256 of every path
present in both. Different roots are the variable that matters: a file
embedding the directory it lives in is the commonest way a cache turns out not
to be portable, and two runs at one path cannot show it.

**Result.**

```text
paths in A: 95283   in B: 95283   shared: 95283
shared, content differs: 1
shared, mode differs:    0
only in A: 0   only in B: 0
```

A third cache, populated twenty minutes later over a 25-module subset, agreed
on all 3,871 paths it shared.

The one exception is the whole answer:
`cache/download/sumdb/sum.golang.org/lookup/<module>@<version>` carries the
**signed tree head at the time of the lookup** - tree size 63410137 in one,
63410388 in the other, with the signature to match. Path to content is stable
for 95,282 paths and time-varying for one kind.

**The documented glob was wrong in both directions.** It said
`'lock,**/*.lock,**/*.partial'`:

* it matched none of the 337 lookup files, which are the only mutable region;
* `**/*.lock` matched 11 third-party *source* files - `Cargo.lock`,
  `Gemfile.lock`, `Pipfile.lock`, `buf.lock` - inside extracted module trees,
  which are as immutable as the code beside them;
* bare `lock` matched `gvisor.dev/gvisor@.../pkg/sentry/fsimpl/lock`, a
  directory;
* there were no `.partial` files at all.

Four errors in three globs, none of which would have produced a wrong build -
they would have refused to share files that could be shared, and shared the one
that could not. The corrected list anchors every pattern at the mount root:
`'cache/lock,cache/download/**/*.lock,cache/download/**/*.partial,cache/download/sumdb/*/lookup/**'`.

**Two findings that change the design rather than the doc.**

*The mutable region is usually absent.* Go consults the checksum database only
for a module missing from `go.sum`, so a project with a complete `go.sum`
writes no lookup file. The 337 came from `go mod download all` walking the whole
module graph. In the common case `/go/pkg/mod` is immutable with no exceptions
at all.

*The zips are 3.8x smaller than what they become.* `cache/download` is 299 MB
where the extracted trees are 1.1 GiB. A fleet that ships the download cache and
lets each machine extract moves a quarter of the bytes, and pays CPU per step
for it. Which side wins is a measurement this has not made.

**Across architectures, which is the claim the fleet actually needs.** The same
module set filled on `darwin/arm64` and on `linux/amd64` (go1.26.2 both ends,
different root paths, different filesystems):

```text
arm64 paths: 95283  amd64 paths: 94162  shared: 94162
shared, content differs: 0
shared, mode differs:    0
only arm64: 1121   only amd64: 0
```

Zero. Not one of 94,162 paths disagreed, in content or in mode. The module
cache is portable between a Mac and a Linux box, and `--portable-except` on
`/go/pkg/mod` is a true claim rather than a hopeful one.

The 1,121 asymmetric paths were an artefact of the procedure and are worth
recording as a trap. `GOFLAGS=-mod=mod go mod download all` on the Mac **wrote
650 lines to `go.sum`** - the hashes it learned from those 337 checksum-database
lookups - and the run copied that enriched `go.sum` to the second machine, which
therefore needed no lookups at all. 1,120 of the 1,121 are that sumdb region; the
last is `cache/lock`, which the exclusion list already names.

So the sumdb asymmetry measures the harness, not the platform - and it is the
second time in this experiment that the thing being measured turned out to be
the measurement. It also confirms the mechanism from the other side: give Go a
complete `go.sum` and it never touches the checksum database.

## E-F5: Rosetta and native amd64 produce the same build cache

E-F4 measured the Go *module* cache, which holds source. The reviewer's
objection was the right one: two architectures agreeing about source is what
source is for, and the interesting cache is the one holding objects.

`/root/.cache/go-build` was documented as unshareable, on the argument that an
entry is keyed by an ActionID that includes absolute paths, so two machines
never compute the same key. **That argument assumes two machines have different
paths, and inside a container they do not** - same image, same working
directory, same `GOCACHE`. Which is every build this engine runs.

**Method.** `go build std`, `CGO_ENABLED=0`, in one pinned image digest
(`golang@sha256:47ce5636...`), with `GOCACHE` at the same path both ends. On a
native `linux/amd64` box (Ryzen 9 5950X) and on an Apple-silicon Mac running the
same image under `--platform linux/amd64`.

**Result.**

```text
mac 2729 files   box 2729 files   shared: 2729
compiled objects (-d): 1052 of 1052 byte-identical
action entries   (-a): 1419 filenames identical, contents differ
mode differs: 0   present in only one: 0
```

An action entry is `v1 <ActionID> <OutputID> <size> <nanotime>`:

```text
mac: v1 001c536e...c18c1  82c03be3...780191  81  1789543524065701588
box: v1 001c536e...c18c1  82c03be3...780191  81  1789543509046931280
```

The ActionID is the filename, so identical filenames already say the keys
agree. The OutputID and size agree. The differing field is a write time, which
Go keeps for garbage collection and which decides nothing.

So an emulated Intel x86-64 and a native AMD Zen 3 compiled 1,052 objects to
the same bytes. Not luck: Go's code generation is a function of `GOARCH` and
`GOAMD64` and never inspects the host, so the host executing the compiler
cannot reach the output.

**What it costs the flag.** The first reading of this was that the `-a` entries
are rewritten, so the cache is not immutable. **That is wrong**, and Go's source
says so: `markUsed` calls `os.Chtimes` and never rewrites the bytes, at most
once an hour, purely so that trimming has a last-used time
(`cmd/go/internal/cache/cache.go`). An entry's content is written once, at
`putIndexEntry`, and never again.

The cache *is* immutable. Two machines still write different bytes at the same
path, because `putIndexEntry` embeds `time.Now().UnixNano()` in the record it
writes.

So immutability is **neither necessary nor sufficient** for sharing, which is a
worse verdict on the old flag than "it is a lie":

* not sufficient - a file written once and never touched can still hold this
  machine's home directory, and sharing it corrupts the build;
* not necessary - this cache is immutable, is not reproducible, and is
  shareable regardless.

Three properties had been running together, and only the third is the one a
fleet needs:

| property     | means                                         | go-build |
| ------------ | --------------------------------------------- | -------- |
| immutable    | content at a path never changes here          | yes      |
| reproducible | every machine writes the same bytes at a path | no       |
| portable     | any machine's bytes at a path will do for me  | yes      |

`get` validates the entry's id, a non-negative size and a non-negative time, and
nothing else - there is no freshness check - so a borrowed foreign timestamp can
at worst mislead trimming, never a result.

**Still to measure.** That two machines *can* share this cache does not say the
sharing pays. `go build std` filled 169 MB; a real project's is larger, and a
worker that fetches an object instead of compiling it has traded CPU for
network on a link measured at 110 MiB/s. The transport does not exist yet, so
neither does the number.
