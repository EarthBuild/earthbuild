# Plan: speaking the remote execution API

Companion to [the Green Paper](green-paper.md), whose (4.5a) and (4.5b) define 𝜏 and 𝜈 - the
objects this plan puts on a wire. The *why* is that a content-named tree is the thing two machines
that never shared a build graph can agree about, and REAPI is where that agreement is already
standardised.

Durations are working weeks for one developer, and are estimates.

## The order, which is not the numbering

R0, R1, R2 and R2b are done or in flight. After them the dependency order is **R4 then R5**: an
execution service must hand back named outputs and the step's stdout, which is what R4 builds. R3 -
this engine as somebody else's client - is independent and wanted by nothing at present.

## What made this reachable

Three things landed on 2026-09-13/14 and are not part of this plan's cost:

* 𝜏 is a Merkle tree of directories rather than one digest over a flat list (4.5b). A subtree has a
  name independent of where it sits, so two bases holding one directory hold one node.
* Nodes are addressable and self-verifying, and `KindTreeMissing` asks a peer which it lacks.
* The fold is carried across layers, so a step names only the directories it moved. On a 48-step
  ladder over a 20k-entry base: 949ms to 47ms, about 1ms a fold, flat in stack depth.

## Decisions taken (2026-09-14)

* **ℋ stays BLAKE3-256; SHA-256 is selected per store, and the two co-exist.** Different functions
  give different keys, so a store may hold both generations and collection removes whichever stops
  being used. No stamp, no migration, no mixing hazard: the failure mode of getting it wrong is a
  miss, never a false hit (I3).
* **BLAKE3 is negotiable with Bazel and not with Buck2.** REAPI has `BLAKE3 = 9` in
  `DigestFunction.Value`; Bazel has `--digest_function=BLAKE3` since 6.4 and BuildBuddy serves it.
  Buck2's own RFC says "publicly available RE providers use SHA256", declines to make output
  digests configurable, and wants *keyed* BLAKE3 internally - which would not match ours. So the
  switch is needed for Buck2 and unnecessary for a Bazel-family server.
* **Our extra metadata goes in `NodeProperties.properties`, omitted when empty.** Not a second
  digest beside a REAPI one. For every tree either tool would construct the properties are empty,
  protobuf emits nothing for an unset message, and our bytes are theirs. Measured on a real
  repository: a 3,131-file source tree and a 20,000-file output tree each have exactly **two**
  distinct (mode, uid, gid) triples, and 644-vs-755 *is* `is_executable`. The genuine extras are
  uid, gid and hardlinks.
* **A node kind REAPI cannot express is refused, not encoded.** A character device is not a
  `FileNode` with an unusual property - it has no content digest and no node type at all, and
  emitting one would have a conforming consumer materialise an empty regular file where a device
  belongs. Both measured trees held zero such entries; the real sources are rootfs-building steps.
* **Hardlink identity stays in the digest.** Relinking identical-but-unlinked files at
  materialisation would match REAPI and save disk, and is wrong: a later step may write to one and
  not expect the other to change. Preserving an existing link is safe because the step that made it
  expected sharing; creating one is not, because nothing did.
* **Sockets are not layer members** (green paper §3.3, landed). A socket is a live process's
  address and no process crosses a step.
* **The hardlink path costs nothing measurable.** `e.hardlink` is relative to the layer root, so a
  directory holding one has a node digest that depends on a path outside it - which is the
  position-independence subtree sharing rests on. Measured, and it collapses at three removes:

  * **A release build has no hardlinks at all** - 10,638 files, zero. Every hardlink in a Rust
    output tree is rustc's incremental machinery, and cargo disables incremental for release.
    CI builds `--release`, as does `examples/rust-layered`, so the tree this engine caches is
    unaffected entirely.
  * In a *debug* tree, 58 of 8,041 directories hold one, 118 counting the ancestors whose digests
    then depend on them - 98.5% unaffected. All 2,697 inode groups share the common ancestor
    `target/debug`, pairing `deps/*.rcgu.o` with `incremental/<crate>/s-<session>/*.o`.
  * Those `incremental/` directories carry a per-session id in their name, so they could never
    match across two builds whatever the hardlinks did. The only genuine loss is `deps/`, one node,
    whose pointers name those unique paths.

  And position-dependence only bites on *relocation* in any case: two bases holding a directory at
  one path record the same string and share the node normally. No encoding change.

## What is not decided

| decision                   | what it needs                                                                                                                                                                                                                                                                                                   |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| accepting an RE cache hit  | RE's equivalence is coarser than ours: two trees differing only in uid, xattrs or non-executable mode are one action input to them. Shipping by `re(d)` is safe; *trusting* a hit keyed on it imports their equivalence, and we hash uid because a step can observe it. An I3 question on our side, not theirs. |
| symlinks as their own list | REAPI has three typed lists; 𝜈 has two, with symlinks folded in under a kind byte. Splitting them makes "one name, two kinds" unrepresentable, and costs a cache generation - cheap only while another 𝜈 change is being spent.                                                                                 |
| uid/gid as a tree default  | Both are constant across every tree measured. Hoisting them to a per-tree default makes `extra(d)` empty for a source tree, so 𝜈 becomes a function of the REAPI digest alone.                                                                                                                                  |

## Phase R0 - emit a REAPI `Directory` - **done**

A tree we already hold, serialised as REAPI and digested as REAPI. No protocol, no network.

* `Directory`, `FileNode`, `DirectoryNode`, `SymlinkNode`, `NodeProperties` encoded to bytes.
* Extras into `properties`, the field omitted when there is nothing to say.
* A tree holding a device, fifo or anything else without a node type is refused, naming the path
  and saying the protocol cannot carry it.

**Exit**: met, by `protoc` rather than by Bazel - the reference implementation the ecosystem's
agreement actually rests on, and it was already installed. Twice over: the message encoding against
bytes protoc produced from a transcription of the real field numbers, and every directory the walk
emits decoded back by protoc against that schema. Hand-rolled rather than taken as a dependency,
because the bytes are the contract and a library hides them.

Found by mutation and not by review: files and subdirectories were emitted unsorted. REAPI requires
name order, Go's map iteration is deliberately not, and nothing asserted it.

## Phase R1 - SHA-256 as a store's digest function (2 weeks)

* `EARTH_RE` (or equivalent) selects ℋ at store creation; both generations co-exist.
* `ir.DigestOf`, `NewHasher` and `NewStreamHasher` take it from one place, so nothing can be hashed
  with the wrong function by omission.

**Exit**: one repository built twice under the two functions, both green, sharing a store, and
`earth prune` reclaiming the abandoned generation. Note that the measured cost is *negative* on
hardware with SHA-2 instructions - concurrently over many small files SHA-256 ran 6766 MB/s against
BLAKE3's 532 - and unverified on x86 without SHA-NI, where it may invert.

## Phase R2 - a cache-only REAPI front end (6 weeks)

The first thing another tool can use. Serves, over our store, without executing anything:

* `Capabilities` - advertising the digest function the store was made with
* `ContentAddressableStorage`: `FindMissingBlobs`, `BatchReadBlobs`, `GetTree`
* `ActionCache`: `GetActionResult` only. Writing is Phase R3's question.

**Exit**: a second EarthBuild instance, pointed at the first over this protocol, fetches the
directories it lacks rather than rebuilding them.

**Not**: `bazel build --remote_cache=` getting hits. That was this phase's exit criterion until the
`Action` message was read properly, and it is unreachable - for a reason that has nothing to do
with encodings. `/ac` is keyed on ℋ over an `Action`, which covers `command_digest` as well as
`input_root_digest`, so a client hits only an action it would itself have run. An EarthBuild step's
command is `/bin/sh -c "cargo build --release"` and a Bazel action's is `rustc --crate-name …`;
they are never the same action, whatever their trees digest to.

**What the phase is actually worth**, then, is three things, and they are worth having:

* **A standard wire between our own instances.** The fleet moves layers by a protocol only this
  engine speaks. REAPI is the same job, already specified, already implemented by other people's
  caches - so a store can be served by something that is not us, and read by something that is not
  us.
* **The prerequisite for R3.** Delegating a step means computing an `Action` and asking a cache
  about it; that machinery is this phase's, whoever ends up answering.
* **The claim, tested.** 𝜏 being an input-root digest rather than a translation of one is only a
  claim until two processes agree on one over a wire.

Sharing *file content* with another tool needs a per-file CAS - blobs addressable by content digest
rather than reachable only through the layer holding them - which this engine does not have. That
is where a Bazel client and an EarthBuild store could genuinely meet before R3, and it is not
scoped here.

## Phase R2b - Κₜ *is* the Action digest

The consolidation one level up from 4.5b's, and the same argument: one key rather than two that
must agree. Every part of Κₜ has a home in an `Action`, including the one that looked least likely:

```text
Command { arguments: ω, environment_variables: ε, working_directory, output_paths }
Action  { command_digest: ℋ(Command), input_root_digest: 𝜏(𝑏), platform: π,
          do_not_cache: --no-cache, salt: ζ }
Κₜ ≡ ℋ(Action)
```

`salt` (field 9) exists so an implementation can retire a generation. ζ is not approximated by it;
it *is* it. `input_root_digest` is already 𝜏 exactly.

What it buys: `/ac` becomes implementable and meaningful; two instances of this engine interoperate
over the real protocol rather than a bespoke one; and once a step is decomposed (R3) a Bazel or
Buck2 client hits this cache for real, because by then we are running the same actions.

**Third-party *workers* are not a goal; third-party *clients* are.** The difference decides what
has to be true. Nothing outside this engine will execute our actions, so it does not matter that
`file`, `image` and `local` have no argv, that `Privileged`, `Docker`, `SSH` and `User` have no
REAPI notion, or that `output_paths` is empty and a conforming worker would therefore return
nothing. Our own worker returns the whole delta, as it always has.

What *is* a goal is a client we did not write - buck2 - declaring actions that this engine
executes. That direction is client-to-us, and it requires only that a digest we compute for an
action equals the one it computes: hence protoc-verified encodings (R0) and SHA-256 (R1), both of
which stay essential. A `rustc` invocation is entirely within our reach to run.

Every operation field with no REAPI field goes in `Platform.properties` as `earthbuild.*`, which is
inside the Action digest, so injectivity survives and
`TestEveryOperationFieldReachesTheKey` enforces totality by reflection.

**One constraint is not about third parties and holds regardless: a secret's value must never enter
a `Command`.** An Action is hashed *and cached*, and our own CAS is a cache. Today the key takes a
secret's name and a separate `SecretDigest`; that separation has to survive the move.

Costs a cache generation, which is cheap while nothing depends on the last one.

## Phase R5 - execute, on one machine

**The endgame, and not the hard part of it.** A client this engine did not write - buck2 - sends an
`Action`; this engine runs it and returns the result. Distribution is explicitly out: no scheduling
across machines, no worker pool, no queue, no fairness, no retries.

**And the service is offered only to a target this engine is already running.** buck2 inside an
`earth` target is supported; buck2 outside one is not. That is not a limitation reluctantly
accepted - it is the shape of the thing, and it removes more work than it leaves:

* **No authentication, and no authorisation.** The sandbox boundary is the boundary. Nothing
  reaches the service that this engine did not itself start, so there is no tenant to isolate, no
  token to issue and no identity to check.
* **No exposure.** It listens where a step can reach it and nowhere else - the channel a guest
  already has, or a socket bound into the sandbox - so it is not a port on a machine.
* **It outlives a build, because the machine does.** A sandbox is reused - `Server.Idle` stops one
  that nobody has wanted for a while, and a host that comes back rejoins the machine already
  running rather than paying for a boot. So this is a daemon, and its lifetime is the guest's: warm
  across builds, which is the whole reason the machine is kept.

  That places it. It belongs in `guestd`, not in the host CLI, because the guest is the thing that
  is already long-lived, already owns the store - "a store on the guest's device is not on the
  host's filesystem" - and is already what a sandbox can reach. A service in the host would be a
  second long-lived thing, on the wrong side of the boundary, holding a copy of what the guest has.

  It also means the idle rule has to learn about it: a machine with an action in flight is not idle,
  however long since a host last spoke to it. **Settled: it already can.** `idle` counts work in
  flight with `begin`/`end` and runs its countdown from the end, so a long action buys the grace
  period afterwards exactly as a long step does. The requirement on the service is to take that
  hold, not to invent one - and a service that forgot to would stop its own machine mid-action,
  which is worth a test rather than a comment.

`earth-native -serve-cache` therefore stays a way to *try* this by hand, and is not the product.

**Settled: the opt-in is `WITH RE`, by analogy with `WITH DOCKER`.** A block rather than a flag,
because the thing being asked for is the same thing: something running alongside a step, for
exactly as long as the step lasts, that the step talks to over a socket in its own filesystem. That
analogy is not decoration - `Step.Daemon` already carries a `Socket` field, and already carries the
lesson that the path is *said* by the host rather than derived at both ends, because two
implementations of one rule disagree eventually and present as a client that cannot reach a service
running perfectly well. `WITH RE` inherits that for nothing.

**Settled: an action's base is the step's, and `container-image` is checked against it.** The
service is given a stack by the step it belongs to, which needs no cooperation from the client. An
action naming a `container-image` is checked against the reference that stack was resolved from,
and refused where they differ: its image is part of its Platform, which is part of its Action,
which is its key, so running it in the caller's environment while keying it under the image it
named admits exactly the false hit I3 forbids. There is nothing honest to substitute - the guest
holds layers by digest and has no registry - so a refusal is the answer and not a placeholder.

**And there is no registry to build, which an earlier draft of this asked for.** `FROM` *is* the
resolution: the engine turns a reference into a stack, memoised on (reference, platform), pinned
before it reaches the key (I17), because it must in order to run the step at all. An action naming
an image is naming something already known, so the host says which reference its stack came from -
one field beside the socket - and the guest compares. A table mapping references to stacks would
have been a second copy of what `FROM` already does, kept in the one place that cannot fetch
anything.

The consequence for an author is a line they were going to write anyway: an action wanting a given
toolchain wants the target's `FROM` to name it. Where a build genuinely needs actions in an image
its caller is not in, that is a second target with its own `FROM`, which is how everything else in
an Earthfile expresses the same thing.

**The seam between them is `remote.Runner`.** Which layers an action's environment is, and whether
a client may name one of its own, is settled where the step is started; what reaches the protocol
code is something that can run an action. A service that resolved bases itself would be the second
place that rule is written.

### The recursion, which is the interesting part

A step runs buck2; buck2 asks the engine running that step to execute actions. Those actions are
**not** steps of the Earthfile graph - nothing planned them, nothing named them, and they must not
enter the schedule. They take the narrow path this phase describes: materialise an input root, run
a command, hand back declared outputs.

But they do want the cache, and they get it for nothing, because an inner action's key is an
`Action` digest and so is Κₜ (4.5a). One key space, one store, whether the work came from an
Earthfile or from a client inside one. That is what R2b bought and it is why it was worth a
generation.

**The hazard is parallelism, not correctness.** The step running buck2 holds a scheduler slot while
the actions it spawns want slots of their own.

**Settled: actions get their own bound, because a shared one deadlocks.** A single machine-wide
budget shared by steps and actions can reach a state where every slot is held by a step *waiting*
for an action that cannot start - and a build that waits for itself never finishes. Two pools can
oversubscribe the machine, which is slow. Prefer slow: a deadlock needs a person and a stack dump,
an oversubscription needs patience.

The action pool wants a smaller default than the step pool for that reason, and neither should be
the other's leftovers. `TestALockedCacheDoesNotSpendTheBuildsParallelism` exists for the
neighbouring case and is the shape the test for this one takes.

### What an action turns out to be

The correspondence is tighter than it first appears, because the input root is **not** the whole
filesystem. The practice every client follows is a `container-image` platform property -
`docker://…@sha256:…` - with the action running inside that image and only its own inputs in the
tree. The other reading, input-root-as-filesystem-root, is what BuildStream wants and is *not
standardised*.

| REAPI                               | this engine          |
| ----------------------------------- | -------------------- |
| `container-image` platform property | `FROM …@sha256:…`    |
| input root                          | the `COPY`'d sources |
| `Command.arguments`                 | `RUN`                |
| `output_paths`                      | R4's `RUN --output`  |

So an action is a base image, a small tree over it, and a command - three of which this engine
already does. Materialising the input root is an overlay upper over the base's lowers, which is
what `COPY` does today.

Reproducibility rests on the image being pinned by digest, because under this model the toolchain
reaches the key no other way. `container-image` is in the `Platform`, which is in the `Action`,
which is Κₜ - so a moved tag is a different key. That property already holds and becomes
load-bearing here.

### What is missing

* **Handing back named files as blobs.** An action returns `output_paths`; this engine captures a
  whole delta. This is the one genuine gap, and it is narrower than "a per-file CAS" - the input
  side needs only a small tree, and the output side needs N named files addressable by content
  digest. R4's `RUN --output` wants the same thing.
* **Accepting uploads**, which inverts the cache's current policy. A blob arriving under a name the
  client chose must be verified against that name on receipt, exactly as it is on serve.
* **Decoders** for the request messages. The encoders exist and are protoc-verified; decoding is
  the same shape and `ChildDigests` is the pattern.
* **gRPC**, whose dependencies are already direct. `Capabilities` advertises the one digest
  function; `Execution` returns a `longrunning.Operation`.
* **stdout on the result.** `ActionResult` carries it and a client displays it, so **R4's output
  capture is a dependency of this phase** and not only a convenience. It is not a dependency of the
  cache-only path.

### What is deliberately absent

Scheduling, worker pools, queue metadata, fairness, retries, and `WaitExecution` streaming beyond
what one machine needs. These are the hard part of remote execution and none of them is on the path
to running an action correctly.

**Exit**: an Earthfile target that runs buck2, whose actions execute through the engine running the
target, and whose second build hits without recompiling.

## Phase R3 - delegate a step to an RE service (unscoped)

Independent of R5 and lower priority: R5 makes this engine a service, which is the stated endgame;
R3 makes it a client of somebody else's, which nothing currently wants. Left here because the
machinery overlaps - computing an `Action` and asking a cache about it is R2b's, whoever answers.

The endgame, and the one with a design question rather than a work list. A result EarthBuild did
not capture has no `Capture.ID`, so I8 has nothing to hash and Κ₁ keys on something it was not
built for. That wants settling in the Green Paper before any code.

Not scoped here deliberately: the decomposition that makes it worth doing - one action per compiler
invocation rather than per RUN - is a separate argument, and the reason it pays is that it isolates
nondeterminism to the action that has it rather than poisoning twenty minutes.

## Phase R4 - two things worth stealing, and not remote execution

**R5 depends on both of these**, which was not obvious when they were written down. An
`ActionResult` carries the step's stdout and a client displays it, so the output capture is a
dependency rather than a nicety; and `output_paths` is how an action says what to hand back, which
is `RUN --output` under another name.

Both found by reading the API rather than by doing the work, both **to be done**, and neither
blocked on a peer, a protocol or a digest function. Sequenced after the phases above only because
those are in flight, not because they depend on them.

### `RUN --output`, narrowing a capture to what was asked for

Not a prerequisite for anything. It was briefly recorded as one, on the grounds that a REAPI worker
returns only its declared outputs - true, and irrelevant, because no worker we do not own will run
our steps.

A step's result is the whole overlay delta. `cargo build --release` writes 10,638 files and
gigabytes into `target/`, and the capture walks, hashes and stores all of it when the only thing
anyone consumes is one binary. REAPI's `Command.output_paths` says up front what an action
produces; a step could say the same.

Three benefits, and the second is the one that matters:

* The capture becomes proportional to what is wanted rather than to what the step touched.
* **Incidental nondeterminism stops entering the key.** `Earthfile:202 cargo auditable build`
  produces different bytes on two identical cold builds, and most of that variation is not in the
  binary - it is `.d` files, fingerprint JSON, timestamps in intermediates. A step declaring only
  its binary becomes cache-equal across runs *without fixing the tool*.
* "What does this step produce" becomes answerable before it runs, which is what a scheduler needs
  to decide what can be skipped and a fleet needs to decide what to ship.

The constraint: an intermediate step's real output is the filesystem the *next* step sees, and the
rest cannot be discarded because the next step may read any of it. So this is opt-in and applies
where an author knows what they want - which is the long steps, where it pays. Note the symmetry
with what this engine already has: Bazel *declares* outputs before running, Κ₂ *observes* inputs
after. Two ends of one problem.

### A cache hit that reproduces what the step printed

`ActionResult` carries `stdout_raw`/`stdout_digest` and the stderr pair, so a hit replays a step's
output. This engine has no such field, and `engine/cli/conditions.go` records the cost: a cache hit
reproduces a step's *effects* but not its *observations*, so `LET v=$(ls -d helloworld*)` gave three
files cold and nothing on every build after, silently - an empty string being a value and not an
error. Twelve corpus targets counted their way to "found 0 files" with the files plainly in the
image.

The fix was `NoCache: true` on every command-substitution probe, so they re-run forever. Carrying
the bytes on the entry would let that caching be turned back on.

Two decisions it needs, both about being wrong rather than about being slow:

* **Raw bytes, not a digest**, at least first. REAPI offers both and raw is right for what this
  fixes - a `$( )` value is small - while a digest needs a home in the CAS with liveness against
  cache entries, which is a collection change.
* **Capped, and a capped result stored as nothing.** A step may print without bound, and a
  *truncated* `$( )` value is a wrong value rather than a partial one. Past the cap the entry must
  record that it kept nothing, so the caller re-runs instead of reading half.

Switchable off, because replaying a previous run's output changes what a build log shows.

## Not in this plan

* A transport that ships subtrees. `layer.PackPaths` and `fleet.Blobs.Fragment` already ship a
  subset of a layer by *path*; nodes name content by *digest*. Making those speak one language is
  the prerequisite, and it is a fleet change rather than an RE one.
* `SHA256TREE` (function 8) - chunk-level verification of large blobs. BLAKE3 has this natively via
  Bao, and `lukechampine.com/blake3/bao` is already in the module graph. Only needed if a server
  demands SHA-256 *and* we want verified partial reads.
