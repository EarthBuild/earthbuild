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

| State                         | Delegated | Ran here | Compute recorded |
| ----------------------------- | --------- | -------- | ---------------- |
| before F1                     | 0         | all      | -                |
| F1, on a full disk            | 4         | 6        | 0s (all refused) |
| F1, second disk               | 6         | 6        | 0s (all refused) |
| declarations movable          | 6         | 0        | 33.256s          |

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

| Reading                    | Time   | Rate         |
| -------------------------- | ------ | ------------ |
| slowest single fetch       | 57.5s  | 17.8 MiB/s   |
| summed across six steps    | 115.0s | 8.9 MiB/s    |
| raw scp, same path         | 22.7s  | 22.1 MiB/s   |

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

| State                             | Transfer reported     | Compute-bound |
| --------------------------------- | --------------------- | ------------- |
| this morning                      | `0s for 0 B`          | 66%           |
| fault-in accounted                | `6.124s for 7.9 MiB`  | 82%           |
| connections opened early          | `417ms for 0 B`       | 92%           |
| priming accounted                 | `433ms for 7.9 MiB`   | 90%           |

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

| Arrangement                       | Wall clock |
| --------------------------------- | ---------- |
| one machine, 16 cores             | **6.48s**  |
| fleet, worker warm                | 33.50s     |
| fleet, worker cold (1 GiB base)   | 95.58s     |

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
