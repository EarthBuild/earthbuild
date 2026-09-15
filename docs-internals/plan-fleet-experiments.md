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
