# Plan: pluggable build engines (`--engine=buildkit|native`) and a worker fleet

Companion to [rfc-post-buildkit-engine.md](rfc-post-buildkit-engine.md), which argues the
*why*. This is the *how*: the work, in order, with exit criteria.

The formal object model - state, the step transition function, key derivation and the numbered
invariants this plan keeps referring to - lives in [the Green Paper](green-paper.md).

Decisions taken (2026-08-12):

* BuildKit stays a supported engine indefinitely. It is not deprecated by this plan.
* A second, native engine is built alongside it and selected with `--engine=native`. **The flag is
  not wired yet** - the engine is reached by the `earth-native` binary, and a refusal that told an
  author to type the flag sent them to a usage message (E403). This document describes what will be
  true; a message printed to a user must describe what is.

* Fleet transport is [`tmc/go-iroh`](https://github.com/tmc/go-iroh) - pure Go, wire-compatible
  with Rust iroh.

Assumption: one full-time developer. Durations are working weeks and are estimates, not
commitments.

## 0. The seam

Everything hinges on one interface pair. Both engines implement both; nothing else in the
tree imports `moby/buildkit`.

```go
// package engine

// Builder constructs the build graph. It replaces util/llbutil/pllb.
// Implementations must be safe for concurrent use (pllb's global mutex is not a spec,
// it is a symptom - see earthbuild-nits.md).
type Builder interface {
    Scratch() State
    Image(ref string, opts ...ImageOpt) State
    Local(name string, opts ...LocalOpt) State
    Git(remote, ref string, opts ...GitOpt) State
    Merge(states []State, opts ...ConstraintOpt) State
    File(s State, actions []FileAction, opts ...ConstraintOpt) State
    Run(s State, opts ...RunOpt) ExecState
    Diff(lower, upper State) State
}

// Session is one build against one engine.
type Session interface {
    // Realise evaluates a node and returns a handle to its filesystem.
    // Every StateToRef call site in the tree becomes one of these.
    Realise(ctx context.Context, s State, opts ...RealiseOpt) (Ref, error)

    ResolveImageConfig(ctx context.Context, ref string, opt ResolveOpt) (digest.Digest, []byte, error)
    ExportImage(ctx context.Context, r Ref, spec ImageExport) error
    ExportArtifact(ctx context.Context, r Ref, spec ArtifactExport) error
    NewContainer(ctx context.Context, req ContainerRequest) (Container, error) // interactive debugger
    Warn(ctx context.Context, d digest.Digest, msg string, opt WarnOpt)
    Close() error
}

type Ref interface {
    ReadFile(ctx context.Context, req ReadRequest) ([]byte, error)
    ReadDir(ctx context.Context, req ReadDirRequest) ([]*types.Stat, error)
    StatFile(ctx context.Context, req StatRequest) (*types.Stat, error)
}
```

The surface is small because our actual BuildKit usage is small. Measured across the tree
(excluding tests):

* `gwclient` symbols used: `Client`, `Reference`, `ReadRequest`, `ReadDirRequest`, `Result`,
  `ExportRequest`, `SolveRequest`, `ResolveImageConfig`, `NewContainer`, `Warn`. That is the
  whole gateway dependency.

* `llb` symbols used: `State`, `Image`, `Local`, `Git`, `Scratch`, `Merge`, `Copy`, `Mkdir`,
  `Mkfile`, `Run`/`Args`, `AddMount`, `CacheMountLocked`, `AddSecret`, `SSHCommand`/
  `SocketTarget`, `HostBind`, `Platform`, `IgnoreCache`, `ImageMetaResolver`. Roughly fifteen
  node kinds - an entirely tractable IR.

* Session attachables: registry auth, secrets, ssh, host sockets (debugger), build-context
  filesync (`buildcontext/provider`). Five providers, all ours already.

* Exporters in use: `ExporterDocker` (tar), the fork's EarthBuild exporter, and the local
  registry + pull-ping path.

## Phase 0 - measure (2 weeks)

Do not skip. The whole case for the native engine rests on numbers we do not yet have.

Instrument, behind `EARTH_ENGINE_TRACE=1`:

1. Every `Realise`/`StateToRef`: count, wall time, definition byte size, vertex count,
   cache hit/miss, and time spent blocked on `pllb.gmu`.
2. `state.Marshal`: call count and cumulative time (suspected superlinear - the state grows
   monotonically and is re-marshalled per solve).
3. buildkitd: RSS, CPU, and bytes moved across the export path.
4. Incremental-compiler baseline: build a Rust crate, then rebuild with no source change
   across a layer boundary, and count recompiled crate units. Expected to be bad today given
   the second-truncation finding in §2c - record the number so the fix has a before.
5. Apple `container` start-up cost, cold and warm, on `macos-26` (`brew install container`).
   This decides whether §2b can schedule one VM per step or needs a warm pool. It is
   independent of everything else in Phase 0 and can be measured in an afternoon.

Workloads: our own `Earthfile` (`earth +test`), `examples/` (a large one), and a synthetic
Earthfile with N sequential `IF`/`$(...)` commands to isolate solve-point cost as a function
of graph size.

Controls, run as a bisection rather than a guess:

* sweep `--parallelism` / `ConversionParallelism`;
* memoise `Marshal` keyed on state digest;
* run BuildKit **in-process** (it is a Go library; the daemon is a deployment choice) to
  price the gRPC and export boundary separately from the solver.

**Exit criterion: reached, and it fired.** See experiments E2, E2b and E10. Solve overhead is
2.1% of a warm rebuild on Linux and 45.7% on macOS, where the cause is Docker Desktop's TCP
port forwarder rather than anything architectural. Marshalling is under 1% everywhere and the
`pllb` mutex never contends.

So **Phase 2 is re-justified on non-performance grounds**: the dev loop and watch mode,
diagnostics, distribution, nanosecond fidelity, and the ~7,200 lines the process boundary
costs us (RFC §1b). That is a sound case, but it is a different one, and no plan document
should keep quoting the solve-overhead number as motivation.

Two items move *up* the list as a result:

* Measure macOS with the `docker-container://` connection helper, and under Apple's runtime
  (PR #614). If either recovers most of the 45.7%, a large macOS win is available in days
  rather than quarters, and independently of this plan.

* Attack the ~1.4 s fixed per-invocation overhead (E10) directly. It is platform-independent,
  it is what watch mode targets, and it is the honest headline number.

## Phase 1 - introduce the seam (4 weeks)

No behaviour change. `engine/bkengine` is the only implementation and is the default.

1. `engine/`: the interfaces above, plus `State` as an opaque handle.
2. `engine/bkengine`: wraps `llb` + `gwclient`. `pllb` is absorbed here; the global mutex
   stays *inside* this engine where it belongs, and is fixed by memoising `Marshal`.
3. Rewrite the eleven `StateToRef` call sites (`earthfile2llb/converter.go:907,1032,3044,3075`,
   `wait_block.go:179,370`, `with_docker_run_base.go:190`, `buildcontext/git.go:174,337`,
   `builder/builder.go`, `builder/image_solver.go`) to `Session.Realise`.
4. Move `buildkitd/` lifecycle behind `engine/bkengine` - it is an engine's private business.
5. Add `--engine` (values: `buildkit`, `native`), config key, and `EARTH_ENGINE`.
   `native` returns "not implemented" for now.

**Engine selection is a mode, not a strategy.** A project picks one and stays on it; nobody
mixes them within a build, and nothing falls back mid-build. Three consequences worth stating
because they delete work: the two engines need no shared cache format, no compatible layer
digests, and no interop tests. What they *do* need to share is Earthfile semantics - an
`Earthfile` must mean the same thing on either engine, or the choice becomes a trap.

**Exit criterion:** `grep -rl moby/buildkit --include='*.go'` matches only `engine/bkengine`
(and its tests). Full integration suite green. Diff is wide but shallow.

## Phase 2 - native local engine (16-24 weeks)

### How Phase 2 is sequenced: thinnest working thing first

The sub-phases below are written as layers - IR, then executor, then export - and that is the
wrong order to *build* in, even though it is a reasonable order to read in. Three of this plan's
assumptions have already been overturned by an afternoon's measurement each (E1, E4, E13). A
layered build defers integration until every layer exists, which is precisely when a wrong
assumption is most expensive to discover.

So build **vertically**: get one trivial Earthfile all the way through, then widen. Every
milestone is a working build of a strictly larger Earthfile, and each is shippable behind
`--engine=native`.

| M      | Earthfile it can build                                      | First exercises                                                                                        |
| ------ | ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| **M1** | `FROM alpine` + `RUN echo hi > /out` + `SAVE ARTIFACT /out` | registry pull, unpack, snapshot, exec, diff capture, artifact export - the whole spine, nothing deeply |
| M2     | the same build, run twice                                   | cache key, cache lookup; test asserts the second run execs nothing                                     |
| M3     | several `RUN`s and a `COPY` between them                    | layer stacking, file ops, and the E11 flattening rule                                                  |
| M4     | `FROM +other`, `BUILD`, `COPY +other/art`                   | the graph, the scheduler, futures instead of barriers                                                  |
| M5     | `IF`, `ARG x = $(...)`                                      | reading facts back out of a finished step - the old solve points                                       |
| M6     | `SAVE IMAGE`                                                | writing into the local image store                                                                     |
| M7     | `COPY ./src` from the host                                  | build context, `.earthignore`                                                                          |
| M8     | `CACHE`, `--secret`, `--ssh`                                | mounts and their locking - where the bugs will be                                                      |
| M9     | `LOCALLY`                                                   | host execution as a first-class step                                                                   |
| M10    | `WITH DOCKER`                                               | the nested case, last because it is worst                                                              |

**M1 is the milestone that matters.** It is small enough to reach quickly and wide enough that
every subsystem has to exist in some form, so the integration risk is paid on day one rather
than in month four. Everything after it is widening, not discovering.

#### Model first, then make it load-bearing one piece at a time

The walking skeleton says build a thin vertical slice. A stronger version: build the thing as an
**executable model** whose structure is real but whose execution is fake, and then replace the
fake parts with real ones one at a time, with the performance harness running throughout.

Concretely, the model has the real IR, the real scheduler, the real cache-key logic and the real
graph, but a *simulated* executor: a step "runs" by sleeping a nominal duration and producing a
synthetic layer. From that you get three things that are otherwise unavailable until late:

1. **A performance budget before implementation.** If the model schedules 10,000 steps in 50 ms
   and the real engine takes 5 s, the gap is implementation, not design - and you know that on
   the day it appears rather than at the end.
2. **A regression is attributable by construction.** Only one component changed from fake to
   real, so a performance or correctness change belongs to it. This is bisection built into the
   method rather than performed after the fact.
3. **The model is the distributed simulator.** A scheduler written against a simulated executor
   can be tested at 100 workers with induced failures in milliseconds, deterministically, from a
   seed. That is the only affordable way to test a distributed scheduler, and - like
   content-addressing - it constrains how the code is written, so it must be decided before the
   scheduler exists rather than bolted on.

The discipline that makes it work is that the model never becomes a throwaway: it stays in the
tree as the fast test double, and every component has a fake and a real implementation behind
the same interface for the life of the project. The risk is the usual one - a model that drifts
from reality and quietly stops predicting anything - which is why the performance harness runs
against *both* and their divergence is itself a tracked number.

##### The stages

Each stage makes exactly **one** port real (§2.0.2) and leaves the rest simulated. The order is
chosen so that every stage has an exit criterion measurable *without* the stages after it.

| S   | Becomes real          | Still simulated                          | Exit criterion                                                                                                    | Invariants it makes enforceable              |
| --- | --------------------- | ---------------------------------------- | ----------------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| 0   | IR, scheduler, policy | executor, materialiser, blobs, transport | a 10,000-step synthetic graph schedules deterministically; same seed and inventory give a byte-identical schedule | I5 (hints off ⇒ same plan), §4.7.3 stability |
| 1   | key derivation Κ₁     | everything below                         | L1 hit/miss decisions match a recorded reference trace across a corpus of synthetic edits                         | -                                            |
| 2   | blob store 𝔅          | executor, materialiser, transport        | internal CAS property tests pass; the OCI store is adopted unchanged                                              | **I2**, I9                                   |
| 3   | materialiser          | executor, transport                      | `SnapshotterSuite` passes, plus the 500-layer case exercising Φ                                                   | I8 (mtime through capture)                   |
| 4   | executor              | transport                                | **M1**: `FROM alpine` + `RUN` + `SAVE ARTIFACT` end to end; `earth-diff` clean against BuildKit                   | I1 sampled, I10                              |
| 5   | observation Ω         | transport                                | E5b adversarial harness finds no false hit                                                                        | **I3**                                       |
| 6   | transport             | -                                        | n-process local fleet, then two machines; E7 speedup                                                              | I6, I7                                       |

Two rules govern the sequence:

* **The fake never leaves.** Each simulated implementation stays in the tree permanently as the
  fast test double. `MemTransport` outlives the real mesh; the simulated executor is what lets
  the scheduler be tested at a hundred workers in milliseconds, forever.

* **Only one port changes per stage.** A regression is then attributable to the port that
  changed - bisection built into the method rather than performed afterwards.

##### The number that keeps the model honest

A model that drifts from reality stops predicting and nobody notices, because it keeps producing
plausible answers. So **divergence is measured, not assumed**: after each stage, run the same
workload through the model and through the partly-real engine and record

```text
divergence = |predicted makespan - measured makespan| / measured makespan
```

per stage, tracked over time. A rising divergence at stage N means the model's remaining fakes
have stopped resembling what they stand for - which is a finding about the fakes, and is
actionable, rather than a vague sense that the simulator is "getting stale".

**As stated, that measurement is circular, and the fix is not optional.** The simulator draws its
durations and sizes from build records; if divergence is then measured against those same records
the model is being asked to predict data it was seeded from, and it will score well while
predicting nothing. Hold data out:

* seed the simulator from builds 1 … N-1;
* predict build N;
* compare against build N's measured makespan.

Only the held-out figure counts. The in-sample figure is worth recording too, because the *gap
between them* is the interesting quantity: in-sample low and held-out high means the simulator has
memorised rather than generalised - it is fitting per-step noise instead of learning per-class
cost, and its L1-L3 class hierarchy is drawn too finely.

Two divergences are worth separating, because they have different causes and different fixes:

| Divergence           | Means                                                                                        |
| -------------------- | -------------------------------------------------------------------------------------------- |
| per-step duration    | the cost model is wrong - fixable by better class priors                                     |
| whole-build makespan | the *scheduler* is wrong, or contention the model omits is real - a far more serious finding |

A model can predict every step's duration accurately and still get makespan badly wrong, which is
precisely the case worth catching: it means the schedule, not the estimate, is where the error
lives.

**Stages 0-1 need no containers at all**, so they run anywhere, in milliseconds, including on
macOS without a VM. That is the practical argument for this ordering: the scheduler, the cache
policy and the stability guarantee - the parts hardest to get right and most expensive to fix
late - are exercised before any of the infrastructure exists.

##### What the simulated executor must reproduce

"Sleeps and emits a synthetic layer" is not a specification, and S0 is not actionable without
one. The simulator has to be faithful in the dimensions the scheduler reads and free to be
useless in every other.

| Dimension            | Simulated how                                                                                         | Why the scheduler needs it                                      |
| -------------------- | ----------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| duration             | from build records where a matching step class exists, otherwise from a distribution seeded per class | `rank_u` and every placement decision                           |
| output size          | likewise, per class                                                                                   | communication cost - the term that decides placement on a fleet |
| input closure size   | derived from the graph, exactly as reality would                                                      | prefetch and locality scoring                                   |
| exit code            | scripted per node, so failure paths are reachable                                                     | retry, WAIT/END, error propagation                              |
| observation set      | scripted per node                                                                                     | L2 lookups, mask precision, E5b shapes                          |
| layer *contents*     | **not simulated** - a synthetic digest, no bytes                                                      | the scheduler never reads content                               |
| filesystem semantics | **not simulated**                                                                                     | that is what S3 is for                                          |

The rule: **simulate what the component under test consumes, and refuse to simulate the rest.**
A simulator that grows fidelity it is not asked for turns into a second implementation, and then
into a second source of bugs.

**Determinism is a property of the simulator, not a nicety.** Duration and size come from a
generator seeded by (step class, run seed), so the same seed reproduces the same run exactly, and
a different seed explores a different world. That is what makes a failing distributed test
replayable from a seed, and it is why `Clock` and `Rand` are injected ports rather than package
functions.

##### The stages are not a second plan

S0-S6 and M1-M10 are the same work seen from two directions. Milestones say *what an Earthfile
can do*; stages say *which port is real*. They meet at one point:

```text
    S4  ==  M1     the executor becomes real, and the first Earthfile builds end to end
```

Everything before S4 is preparation that M1 depends on; everything after is widening. When the
two disagree about priority, the milestone wins - a working build is worth more than a
better-tested simulator.

##### What the model cannot tell us

Stated so nobody mistakes a green simulation for a working engine. The model cannot find: real
filesystem semantics, mtime fidelity (I8), container escape or isolation defects, actual cache
hit rates on real inputs, or anything about bytes it never had. It answers questions about
*scheduling, ordering, stability and policy* - and those only.

**A partial engine must fail loudly.** Until M10, the native engine cannot build most real
Earthfiles. It therefore carries an explicit capability list, and the interpreter refuses
anything absent from it with a message naming the command, the milestone that will add it, and
the `--engine=buildkit` fallback. Silently doing approximately the right thing is far worse than
refusing, and this is the failure mode a partially-built engine invites.

**The progress metric is the existing test suite, ratcheted.** Not weeks elapsed: the count of
`tests/` passing under `--engine=native`, which starts near zero and only ever goes up. Wire
that into CI at M1 so the number is visible from the beginning, and treat any regression as a
build break. It also means the engine is being judged against tests written for the old engine,
by people who were not trying to make the new one look good.

#### BuildKit is the oracle: differential testing from M1

We are in the unusually good position of having a reference implementation to hand. Build the
same Earthfile under both engines and compare the results mechanically; "is the new engine
correct" becomes "does it differ from BuildKit, and is the difference on the known list".

Build `earth-diff <target>` at M1, not later, so it grows alongside the engine. It builds under
both engines, exports both, normalises, and reports the *first* difference as a path plus a
field rather than a digest mismatch.

**Compared:** artifact bytes; the unpacked layer tree - paths, modes, uid/gid, symlink targets,
file contents, xattrs; image config - env, entrypoint, cmd, workdir, labels, exposed ports; exit
codes, including for builds expected to fail; and whether a step re-executed, which is how cache
behaviour is compared without comparing cache keys.

**Deliberately not compared, and each needs a stated reason:**

| Excluded                                 | Why                                                                                                              |
| ---------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| sub-second mtimes                        | our known, intentional divergence (§2c) - compare truncated to whole seconds                                     |
| layer and image digests                  | different tar writer and different timestamps make these differ by construction; compare *contents*, not digests |
| `created` timestamps in the image config | wall-clock                                                                                                       |
| tar entry order                          | normalise by sorting before comparison                                                                           |
| `/etc/hosts`, `/etc/resolv.conf`         | injected by the runtime during `RUN` and can leak into a layer; the runtimes differ                              |

**Filter the corpus for determinism first.** A build containing `RUN date` or an unpinned
network fetch differs from itself, so it cannot serve as an oracle case. Run every candidate
twice *under BuildKit* and drop any that fails to reproduce itself. Doing this first also
produces something independently useful: a measured list of which of our own targets are
non-deterministic.

**Where the oracle does not apply.** BuildKit is the reference, not the definition of correct.
E11 found a build BuildKit *cannot* do at all - the 500-layer wall - and there the goal is to be
better, not equivalent. Any deliberate divergence gets an entry in the exclusion table above
with a reason, and the table is reviewed as a whole rather than grown one exception at a time,
because that table is where "equivalent" quietly turns into "similar".

**Ordering: macOS first. Decided 2026-08-13.** §2b's argument carries - the constrained backend
first is what keeps `engine/exec.Backend` from quietly assuming runc, overlayfs, cgroups and CNI.

The cost is accepted rather than wished away: macOS needs `earth-guestd` (E1b) before any step
executes, so **M1 is further out than it would be with a Linux-first order**. What makes that
affordable is the staging below - stages S0-S3 exercise the IR, the scheduler, the cache policy
and the stability guarantee with *no executor at all*, so the schedule is not idle while the
guest agent is built. Linux follows as the second implementation, which is where the interface
gets its honesty check.

### Where the ports actually are

A stage is done when its port is *real* - not simulated, not stubbed, and exercised by the same
conformance suite the simulator passes. Recorded here rather than inferred from the code, so that
a stage cannot quietly count itself finished.

| Stage | Port                         | State                                                                                                                                                                                                       |
| ----- | ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| S0    | IR, hashing, key derivation  | **real** - Κ₁, Κ₂, Φ, Λ, records, first-divergence reporting                                                                                                                                                |
| S1    | scheduler over a fake worker | **real** - placement and eligibility exercised with several workers (E130)                                                                                                                                  |
| S2    | blob store                   | **real** - content-addressed, verified on read                                                                                                                                                              |
| S3    | materialiser                 | **real on Linux** (overlayfs, root or rootless, `userxattr`); depth no longer bounded by the store's path length (E163); simulated elsewhere                                                                |
| S4    | executor                     | **real on macOS and Linux** - one case table answered by both backends                                                                                                                                      |
| S5    | observation source           | **real for COPY and RUN** - a command reused over a base it never ran on (E217)                                                                                                                             |
| S6    | fleet transport              | **end to end** - both protocols cross a real wire (E247, E248), `earth-worker` joins from a machine that is told only where the driver is (E254), and a build finds its fleet through `fleet.Driver` (E255) |

**The corpus reports no unimplemented construct.** 491 targets across 193 Earthfiles: none blocked
on something this engine has not built. What remains is 474 withheld by a plan-only caller - a probe
to run, a repository to fetch, an argument, a secret, a terminal - 45 invalid Earthfiles from 36
causes, and 5 refused by decision from 2.

The number moved twice for the same construct. A `RUN` carrying mounts inside a `FROM DOCKERFILE`
blocked 371 targets - every target of the buildkit sibling - and was first *reclassified* rather than
built: a Dockerfile's `bind` was filed under the decision taken about an Earthfile's
`bind-experimental`, which is a different thing wearing the same word. One takes a host path and is
written through; the other is a read-only view of the build context or of an earlier stage, which is
content this build already digests. §3.3d and I20 say so, and the engine now builds it - both kinds
of view, keyed by what they hold.

**The engine builds this repository.** Not a stage - a stage is a port, and this is what the ports
add up to - but the milestone the staging was for, and the one that cannot be claimed by a
conformance suite:

* every target in the repository's own Earthfiles plans, `TestTheRepositorysOwnTargetsPlan`;
* the repository builds itself with the native engine, `TestTheRepositoryBuildsItself`, producing
  a Linux binary for the machine's own architecture;

* corpus targets build for real under `EARTH_TEST_BUILD`, six of six on the last sweep.

Three defects stood between the ports being real and this being true, and none of them was a
missing port: a layer stack whose depth depended on where the store happened to live (E163), two
assertions about the machine they were written on (E163a), and a shell pipeline in `+lint` that
worked only while one `go.mod` was in the image (E164). The gap between "each part works" and "the
whole thing runs" was made of accidents, which is the argument for building the thing rather than
grading the parts.

### Decisions taken, 2026-08-17

Four questions had been accumulating, each blocking work rather than a conclusion. Recorded here
because a decision that lives in a conversation is a decision nobody can find.

| question                                                       | decision                                                                                                                                                                     |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `goconst` on 276 test fixture strings, 36% of the lint backlog | **fix them**; the configuration stays maximal and untouched                                                                                                                  |
| `RUN --interactive`, the last unimplemented construct          | **implement it**, restricted to a driver and workers on one host                                                                                                             |
| S5's `RUN` tracer: seccomp-unotify (needs `unsafe`) or FUSE    | **consider seccomp-unotify**; the specific `unsafe` block, the reason no safe route works and its SAFETY invariants come back for per-use consent before anything is written |
| S6 fleet transport                                             | **start it**; go-iroh goes in                                                                                                                                                |

The `--interactive` restriction is the interesting one. A prompt needs a terminal attached to a
running step, and relaying one across a network is a different problem from relaying one across a
pipe - so the construct is accepted when the driver and every worker are on this machine and
refused, by name and with the reason, when they are not. That is a **fourth** shape of refusal, and
it fits none of I10's three: not a gap, not absent from the language, not a decision, but a
capability this *arrangement* cannot provide. I10 will need it.

S4 now runs real processes end to end - scheduler, guest protocol, exec - through a `Local`
sandbox that provides no isolation. Two pieces remain, and they are the two that make results
*trustworthy* rather than merely produced:

The VM backend landed on macOS. Measured on this machine: **~650ms to boot, ~60ms to exec**,
against experiment E1b's predicted ~690ms and ~65ms - close enough that the one-VM-per-run
decision rests on measurement rather than on the estimate. A step now runs inside a VM, cannot
see outside its chroot, and produces a **captured, cacheable** result. That is the first point in
the engine where anything reaches 𝔄.

**The differential caught a real divergence, 2026-08-13.** Twelve construct cases now run through
both backends. One passed on this machine and failed in a sandbox: `WORKDIR`.

`isolate` chroots, and sets `cmd.Dir` itself - **after** the working directory had been set,
silently discarding it. Inside a chroot the directory has to be named from the new root anyway, so
the value set before isolation was wrong twice over. A step with `WORKDIR sub` therefore ran in the
root and wrote its output one directory from where every later command looked, **while reporting
success**.

The host path has no chroot, so it could never show this. That is the entire argument for running
the same cases through both: neither suite alone can tell you that two implementations of one
construct disagree.

Three of the twelve are marked sandbox-only and skipped on the host with that reason rather than
quietly dropped, because `COPY` has no meaning on a host target - there is no image to copy into,
and writing into the developer's own directory instead is a surprise nobody wants from a build
tool. The coverage difference stays visible instead of being hidden by a passing run.

**Host steps execute, 2026-08-13.** A `LOCALLY` target now runs: on this machine, in the project
directory, with output streamed and both steps recorded **uncaptured**.

Only ε reaches it, as for any other step. A host step inheriting the engine's whole environment
would observe ambient state that never entered its identity - I3 violated by omission - and would
do it on a machine full of the developer's own variables.

**Which exposed a defect the feature made obvious: a local-only build required a sandbox.** It
booted a VM, used it for nothing, tore it down - and on a machine with no container runtime it
failed outright. A `LOCALLY` target is precisely the thing someone without a runtime can run, so
demanding one to run it is backwards. The front end now inspects the plan: if every step is a host
step, no sandbox is started. Measured at **0.43s with no guest binary available at all**, where
before it could not run.

The executor refuses a sandboxed step rather than improvising one, so "needs no sandbox" stays a
decision the caller made rather than a guess this type silently corrects.

**LOCALLY, 2026-08-13.** Corpus: **346 to 374 targets planned**, 241 causes down to 225.

`LOCALLY` makes the steps after it run on the invoking machine. The specification has had a name
for this since the beginning - `host` - and distinguishes it throughout: unsandboxed,
non-cacheable, never retried (I7). Those are not three policies but one fact stated three ways:
nothing bounds what a host step observed, so A3 does not hold, ε is not a bound, and any key
derived from it is a claim about a step that could have read anything.

**The rule is enforced in the scheduler, not left to an executor to declare.** A host step neither
writes an entry nor reads one - reading would run nothing and report that the machine had been
changed, and the entry could come from a shared cache someone else wrote. An executor that simply
forgot to mark its result uncaptured would otherwise produce exactly the wrong answer, silently.
The test uses an executor that *claims* the result is captured, because that is the case the rule
has to survive.

Two refusals fall out of the model rather than being chosen: `COPY` inside a `LOCALLY` target has
no image to copy into, and would write to the developer's own disk; a `FROM` after a `LOCALLY` is
two targets wearing one name, with different cache rules on either side of the line.

`WITH DOCKER` remains the largest gap at 46 causes, and is not incremental: it needs a container
runtime inside the sandbox. It is named as such rather than attempted in the margins of an
iteration.

**Platform, push and build-args, 2026-08-13.** Corpus: **302 to 346 targets planned**, and 265
distinct causes down to 241 - the first iteration measured against the honest denominator.

`BUILD --platform=linux/arm64 +target` now builds that target for that platform. The platform
travels with the resolution, reaches every step, and is part of the memo key, so the same target on
two architectures is two builds rather than one. It is parsed rather than accepted:
`--platform=nonsense/` would otherwise become a platform with an empty architecture and fail much
later with a message about a manifest.

**And the executor honours it.** Recording a platform in the plan without pulling for it would have
produced a right plan and a wrong result - two builds, one image - which is the most expensive
shape of bug available here. The node's platform decides the pull; the executor's is the fallback.

`SAVE IMAGE --push` was refused on the grounds that silently not pushing is a release that looks
done and is not. That was the wrong reading: the flag declares an image *should* be published, and
publishing happens when the invocation asks. It is now recorded on the image, which is not the same
as ignoring it - a build that pushes nothing has been told to push nothing.

**LET, SET and `+base`, 2026-08-13.** Corpus: **288 to 302 targets planned**.

`LET` introduces a variable, `SET` updates one. `SET` on something never declared is refused, and
that distinction is the whole reason the language has both: treating `SET` as a declaration makes a
typo silently create a second variable while the original keeps its old value and the author
believes it changed. A value computed by running something - `LET x=$(cat version.txt)` - is
refused rather than guessed, on the same rule as an undecidable `IF`.

**`+base` names the base recipe**, the commands before the first target. The name is reserved - the
parser refuses a target called `base` - so a reference to it can only mean the implicit one.
Searching the named targets alone reported `no target named "base"` **137 times**: true of the list
it searched, and useless to the reader.

**A note on the gap ranking.** One line - a remote `FROM` in `buildkitd/Earthfile` - now accounts
for 182 refusals, because every target in `tests/` inherits it through a chain of four files. The
ranking counts blast radius, not causes. That is the right thing for choosing work (fixing one line
unblocks 182 targets) and the wrong thing for judging how much is left, and the two readings are
easy to confuse.

**The reference parser, and a lesson about which layer does what, 2026-08-13.**

`image.ParseRef` was hand-rolled and wrong: a digest-only reference got both a digest *and* a
`latest` tag, so a pull pinned to a digest carried a tag contradicting it. Replaced with
`distribution/reference`, already a dependency and already used by the BuildKit path here.

It also knows a rule that would have been written the other way round: a path component must be
lowercase, so an uppercase first component **can only be a domain** - `MYHOST/name` is a host and a
repository, not a malformed reference. The obvious hand-rolled rule rejects a legal reference.

**Then the swap caused a silent wrong-output bug**, which is worth recording because it is the
sharpest lesson of the day. Running every command's arguments through the shell lexer removed
quoting, and:

```text
RUN /bin/busybox sh -c "echo compiled-output > /binary"
  -> /bin/sh -c "/bin/busybox sh -c echo compiled-output > /binary"
```

The redirect now belongs to the *outer* shell; the inner one receives only `echo`. The build
succeeded, every test passed, and the artifact was empty.

The distinction the lexer cannot make for you: **quote removal is right for a value the engine
consumes** - a path, an argument default, a label - **and wrong for text a shell will parse again**.
Expansion applies to both; unquoting only to the first. Reusing a shared component is right, and
reusing it for a job it was not doing is how a correct component produces a wrong answer.

**Use what is already here (Giles, 2026-08-13).** The interpreter had grown its own option parsing,
quote handling and variable expansion. All three already exist in this repository and are used by
the BuildKit path:

| hand-rolled              | already here                                |
| ------------------------ | ------------------------------------------- |
| flag loops per command   | `cmdopts.Copy` / `.Build` / `.Do` / `.From` |
| parenthesised regrouping | `stringutil.ProcessParamsAndQuotes`         |
| quote and escape parsing | the same, plus the shell lexer              |
| variable expansion       | `dfShell.NewLex` + `ProcessWordWithMap`     |

Deleted and replaced with `flagutil.ParseArgsCleaned` and the shell lexer. Two implementations of a
language's substitution rules is two sets of corner cases that drift, and the second one is always
the one nobody tests against real files.

**The corpus count fell from 550 to 288, and that is the fix working.** `FROM` had ignored its
options entirely, so `FROM --pass-args +base` took `--pass-args` as an *image name* and planned
successfully - an image called `--pass-args`, which no registry has. Those targets counted as
planned. Parsing the options correctly resolves the reference instead, follows the chain, and
reaches genuine walls: remote repositories, mostly.

A metric that counts accepted nonsense is worse than no metric, because it rewards exactly the
change that makes it worse. The number is now smaller and means something.

**Read the grammar (Giles, 2026-08-13).** `internal/earthfile/earthfile.abnf` defines the language
this engine interprets, and consulting it moved the corpus from **358 to 550 targets planned** in
one iteration - more than any feature has.

Two things it settled that had been inferred from samples:

* **Quotes are syntax.** `path` excludes quote characters unquoted and "quoted paths permit
  QUOTED-STRING", and `escaped-char = "\" %x21-7E`. The interpreter passed quoted tokens through as
  values, so `COPY "wildcard-copy.earth" /dst` reported `"wildcard-copy.earth" is not in the build
  context`: a file nobody has, presented as the author's mistake, **226 times**. Unquoting happens
  after expansion, because a variable inside quotes is part of the value.

* **`function-ref = target-ref`** - a function is written exactly like a target, differing only by a
  FUNCTION as its first command. The corpus harness scanned every `name:` line and asked the
  interpreter to build functions as targets, collecting twenty-one refusals that said nothing about
  the engine. A defect in the measuring instrument, not the thing measured.

Recorded as assumption **A6** in the specification: this document says what an engine does with an
Earthfile, the grammar says what an Earthfile is, and where they disagree about syntax the grammar
governs. An interpreter that infers syntax from examples is correct about everything except what
the author wrote.

**IMPORT, and negated conditions, 2026-08-13.** Corpus: **335 to 358 targets planned**.

`IMPORT ./lib AS mylib` gives another Earthfile a name; without `AS` the alias is the last element
of the path. Imports are **per-file**, because an import is a declaration *in* a file about how
that file's own references read: a name imported in one Earthfile means nothing in another, and
sharing them would let a reference resolve differently depending on which file was parsed first.

A bare name that was never imported is named as such rather than read as a directory. `tests+build`
with no IMPORT would otherwise report "no Earthfile in ./tests" - true, and unhelpful, when the
real answer is that a line is missing. Remote imports are refused **where they are written**, not
where they are used: it is a decision about that file, and reporting it at the first reference
sends the reader to the wrong line.

`IF [ ! -z "$x" ]` - negation - was 36 refusals and is now decided. An operand that expands to
nothing disappears from the token list entirely, so `[ -z ]` with one token is the case `-z` exists
for and is true rather than malformed.

**A nil map panicked on the first IMPORT in any file**, because the build's first unit was built
from a struct literal and every other one through `load`, so a field added to one was missing from
the other. Replaced with a constructor. Two construction sites for one type is the defect; the nil
map was only how it surfaced.

**Cross-file references, 2026-08-13.** `./lib+build`, `..+root`, `FROM ./lib+base`,
`COPY ./lib+build/out` - a build now spans several Earthfiles. Corpus: **326 to 335 targets
planned**.

The refactor is a **unit per Earthfile** rather than one tree per build, and the reason is a trap
rather than tidiness: everything in an Earthfile is relative to *its own* directory. A COPY in
`lib/Earthfile` names a file beside that file, and resolving it against the calling Earthfile
would silently copy something else - or report a file missing that is sitting exactly where its
own Earthfile says it is. Each unit therefore carries its own base recipe, functions, resolution
memo and build context.

Cycle detection is now across files: `./lib+build` depending on `..+main` is a cycle even though
neither Earthfile contains one. The site is the directory *and* the name, so the same target name
in two files is two sites.

Remote references - `github.com/org/repo+target` - are refused rather than guessed at. A local path
begins with `.`, `..` or `/`; anything else before the `+` is a host name, and building something
other than what was named is the failure this engine is arranged against.

**`COPY --dir` and BUILD arguments, 2026-08-13.** Corpus: **297 to 326 targets planned**.

`--dir` copies the directory itself rather than its contents - `cp -r src dst` against
`cp -r src/. dst` - and getting it wrong puts a project's files one level from where every later
command looks. It is the commonest COPY flag in this repository by a factor of five.

It **desugars**: a destination ending in a separator already means "place the source inside this",
so the flag becomes a destination the author could have written. No new field, no protocol change,
and identity and the key cover it because it is in Args where they already look. A flag that can be
expressed in what the engine already has is a flag that needs nothing new.

`BUILD +target --NAME=value` passes an argument to the target, as DO does for a function. Which
surfaced a real defect: **targets were memoised by name alone**, so `BUILD +image --tag=one`
followed by `--tag=two` produced *one* image and silently discarded the second. Memoisation now
keys on the name and the arguments together - a target built with different arguments is a
different build - while two calls with the same arguments still resolve to one subgraph.

**Image configuration, 2026-08-13.** `ENTRYPOINT`, `CMD`, `EXPOSE`, `VOLUME`, `LABEL` and `USER`.
Corpus: **249 to 297 targets planned**, the largest single jump since the base recipe.

They divide cleanly, and the division is the whole design:

* **What an image says about itself** - entrypoint, command, ports, volumes, labels - adds nothing
  to the graph. It produces no layer, so it is not a step. It is collected as configuration and
  attached to the image where `SAVE IMAGE` appears, *not* at the end of the recipe: a command after
  the save belongs to whatever is saved next, and taking end-of-recipe state would let a later line
  silently change an image already declared.

* **`USER` is the exception**, and lives on the operation instead. It changes what a step *does*,
  not only what the image declares - running as root and running as nobody can produce different
  filesystems - so it belongs to ω and reaches Κ₁.

The reflective key-coverage guard picked up `Op.User` with no wiring, as it did `Op.Dir`. That is
twice now that a new field affecting a result was protected by a test written before the field
existed, which is the entire argument for writing that kind of guard rather than a comment.

Exec form and shell form are both accepted: `ENTRYPOINT ["/usr/bin/tool"]` runs the binary,
`ENTRYPOINT /usr/bin/tool --serve` runs it through a shell. Treating the second as an argv produces
an image that fails to start with "no such file or directory" naming the entire command line.

**`--pass-args` and `SAVE IMAGE`, 2026-08-13.** Corpus: **231 to 249 targets planned**.

The iteration began by auditing the remaining `BUILD` and `FROM` refusals for more misdiagnoses,
on the theory that the last one paid better than a feature. It did not find any - the remaining
refusals are honest - which is itself the useful answer, and it took ten minutes rather than the
hour a feature would have.

What it did find was `--pass-args`, 26 times on BUILD and as many again on DO. **It is the explicit
form of something this engine refuses to do implicitly.** A function does not see its caller's
arguments, because one that did would behave differently depending on where it was called from;
`--pass-args` is the author writing down that this call means exactly that. Refusing it would be
refusing a decision someone had already made and recorded. An explicit argument on the call still
beats an inherited one - the nearer statement wins.

`SAVE IMAGE` collects like `SAVE ARTIFACT`: a declaration of output rather than a step. `--push` is
refused rather than ignored, because silently not pushing an image a release target says to push is
a release that looks done and is not.

**Artifact copies, and a misdiagnosis worth more than the feature, 2026-08-13.**

The corpus's largest category was "missing context file" - 173 of them. Almost none were missing
files:

* `COPY --dir src dest` (40) - COPY takes flags, and reading one as a path reported
  `--dir is not in the build context`. A diagnosis of entirely the wrong thing, forty times over.
  The same bug had been fixed for BUILD and not generalised.

* `COPY +compile/binary /usr/bin/` - an **artifact reference** to another target's output, reported
  as a missing file, sending the reader after something that was never meant to exist.

* `../../libs/hello+artifact/*` - a cross-file artifact reference, likewise.

A wrong diagnosis is worse than a refusal: a refusal tells you the engine cannot do something, a
misdiagnosis tells you *you* did something wrong. Fixing the three moved the corpus from 225 to
231 planned targets, but the real change is 173 confident wrong answers becoming correct ones.

**`ir.Node.Sources` came out of it**, and is the better design regardless. A step has things it
*stands on* (Inputs, whose layers form its base) and things it *reads* (Sources, whose layers never
stack but do reach the key). That was previously inferred from an input's kind - the scheduler
special-cased `OpLocal` - which worked while a build context was the only kind of source and would
have silently stacked an entire image the first time an artifact was copied.

The executor was passed source **node identities** where it needed **result layers**. The two
coincide for a staged context, whose layer is named after its node, and diverge for an artifact,
whose layer is whatever the producing target made. It worked until the first artifact copy and then
looked for a layer that had never existed.

**IF, 2026-08-13.** Conditions over build arguments are decided when the plan is made, and only
the selected branch enters the graph. Corpus: **210 to 225 targets planned**.

The design came from counting what real Earthfiles actually write, not from reasoning about what
`IF` could contain. Nearly all of them are string comparisons of arguments -
`[ "$mode" = "release" ]`, `[ -z "$x" ]` - which are decidable once arguments are expanded. A
minority genuinely need a filesystem or a process, and those are refused by name rather than
guessed: guessing a branch builds something the Earthfile does not describe and reports success.

**Chained conditions, 2026-08-13.** `&&` and `||` between tests are still a function of ε, so they
are decided the same way - left-associative, equal precedence, short-circuiting. This was not a
refinement anyone asked for: nine of the eleven conditions the corpus was refusing were chains of
argument comparisons that needed no process at all, and the engine was turning them away because it
read a chain as one indivisible unsupported condition. Corpus: **374 to 382 targets planned**,
distinct causes **225 to 217**, `IF` refusals **11 to 2**.

Short-circuiting does real work here rather than saving time: `[ "$v" = "no" ] && command -v
unbuffer` is fully decidable, because the operand that needs a sandbox is never reached. A
condition that is not evaluated needs no decision.

**Evaluating the rest needs the prefix to have run - and LOCALLY is the hard case (2026-08-13).**
`interp.Conditions` is the seam: a caller-supplied evaluator, given the condition, the node whose
filesystem it must run against, and where it is. Without one, an undecidable condition is refused
by name, which is what `earthbuild plan` and the corpus need - neither should start a sandbox
behind the caller's back to produce a graph.

Wiring an evaluator that runs the condition on this machine was tried and reverted, and the
reason turned out to be the shape of the whole remaining problem. `IF [ -f flag.txt ]` after a step that writes
`flag.txt` is evaluated **while the plan is being made**, before any step has run: the file is not
there yet, the condition answers false, and the build takes the wrong branch and reports success.
Evaluation is not a function that can be called during interpretation - it needs the target's
prefix to have been *executed*.

That inverts the obvious order of work:

| target    | what evaluating a condition costs                                                    |
| --------- | ------------------------------------------------------------------------------------ |
| sandboxed | the prefix runs once and is cached, so the main build hits that cache. Nearly free.  |
| LOCALLY   | host steps are never cached (I7), so running the prefix to decide runs it **twice**. |

A LOCALLY prefix executed twice means `RUN rm -rf build` executed twice, which is not a cost but a
defect. So the sandboxed case is the tractable one and LOCALLY needs interleaved execution -
interpret to the IF, execute, resume - rather than a prefix re-run.

**Sandboxed conditions, done 2026-08-13.** The condition is run on the filesystem the recipe has
built up to that line, and its exit status is the answer, as it is in a shell. Measured on the
end-to-end suite: the first condition costs 2.3s including the image pull, and each subsequent one
1.0s, because the prefix it stands on is keyed and cached and the build proper then hits the same
entries. "Nearly free" is the measurement, not the hope.

Two things already in the engine made this small enough to be worth doing now rather than after
prediction:

* the scheduler already reports a non-zero exit as a `StepError` rather than an executor failure,
  so "it ran and said no" and "it could not be run" were already distinct - and answering false to
  the second would take a branch the Earthfile did not select while reporting success;

* a failed step is deliberately not cached, so a condition is never held to a previous answer.

The sandbox is built lazily and shared between deciding the conditions and running the build. Not
thrift: a second sandbox has its own layer store, so every step run to answer a condition would be
a cache miss in the build that follows, and the "nearly free" property would be a fiction.

LOCALLY remained refused at the time of that note, and does not now: it plans in every shape tried,
runs on the machine, and agrees with the reference engine in a differential case (E42). The
milestone table still lists it under M9, which is where it was planned rather than where it landed.
Prediction (below) is still unbuilt, and is now purely a latency optimisation rather than the thing
that makes conditions work at all.

**Remote target references, 2026-08-13.** `FROM github.com/org/repo:rev+target` resolves: the
repository is checked out and its Earthfile interpreted like any other. Parsing, revision handling
and fetch memoisation sit in the interpreter behind an `interp.Remotes` seam; the git work sits in
the CLI. Without a fetcher a remote reference is still refused by name, which is what a plan-only
caller needs - producing a graph must not clone a repository.

Three decisions are load-bearing and none is obvious:

* **A pinned revision is cached; an unpinned one is not.** `repo:abc123+t` is immutable, so its
  checkout is reused forever. `repo+t` means whatever that branch holds *now*, and caching it by
  name would pin the reference to whatever this machine happened to see first - reproducible by
  accident and wrong on purpose.

* **Fetch is memoised per (repository, revision), not per repository.** A file naming a dependency
  three times must not clone it three times; two revisions of one repository must not collapse to
  one checkout, which would build one while reporting the other.

* **`git fetch`, not `git clone --branch`.** A revision may be a tag, a branch or a commit and only
  fetch takes all three - `clone --branch` refuses a hash. The revision is named explicitly so a
  server that cannot supply it fails loudly rather than handing over its default branch.

**An Earthfile is untrusted input, and this is where that stops being theoretical.** The repository
and revision are used to build a path that is then `RemoveAll`ed and recreated, and are passed to
git as arguments. `github.com/../../etc+t` and `repo:../../../etc+t` both walked out of the cache;
`repo:--upload-pack=id+t` made git run a command of the Earthfile's choosing on this machine. Both
are rejected now, at the interpreter (a repository path element and a revision are names, never
paths or options) and again at the fetcher (the computed path is checked to be inside the cache
before anything is removed). The second check does not depend on the first being correct, because
the layer that would do the damage is the fetcher.

This is not a hypothetical threat model. A remote reference means this build clones a repository
and interprets the Earthfile it finds - so the *next* reference in the chain is text an attacker
supplied, and the engine had better not have trusted it. Found by review rather than by a test,
which is the argument for the review.

**Why a wedged subprocess could not be bounded, 2026-08-14.** The differential oracle hung the
suite for 380 seconds, and neither `context.WithTimeout` nor `Cmd.WaitDelay` stopped it. The reason
is worth recording because it is not obvious and it has now bitten twice in this session.

`CombinedOutput` and `Output` read through **pipes**, and `Wait` does not return until those pipes
reach EOF. The reference engine leaves child processes holding its stdout - it talks to a daemon
through helpers - so killing the process on a deadline closed nothing, and the read blocked forever.
The process being waited for was not the one holding the pipe. The same shape hung a shell command
earlier today, where a stray `cat` held stdin.

Writing to a **file** instead fixes it: the descriptor is passed straight to the child, no copying
goroutine exists, and `Wait` returns when the process this test started is gone. The deadline then
works as written.

The test now asks once whether the reference engine can make progress - a trivial build, not
`--version`, because the failure is a daemon that has stopped responding and a version string is
printed without ever talking to it. A broken dependency costs one 90-second skip that names the
remedy, rather than five timeouts that look like work.

**The image cache is per machine, not per build cache (2026-08-14).** `EARTH_IMAGE_CACHE_DIR`
separates the two, because they answer different questions: a layer store belongs to a build cache
and dies with it, while an image is content-addressed by reference and platform and is *identical
for every project on the machine*. Fetching alpine once per project is bandwidth spent on nothing.

This was found the expensive way. The sandbox suite gave every case a fresh cache directory - right
for layers, wrong for images - so alpine was fetched again for each one, on every run. A day of that
earned a 429 from Docker Hub, and the tests then reported the quota as a skip: **the suite went
green while its coverage quietly emptied out**. Nineteen differential cases skipping is
indistinguishable from nineteen passing if only the exit code is read.

With one shared image cache: rate-limit skips **19 to 0**, 119 passing, and the suite is faster.

One test had to be exempted, and the reason is worth keeping: `TestAnImageNamedTwiceIsFetchedOnce`
*counts* the entries in the cache, and every other test in the suite puts things in the shared one.
A test that measures a cache cannot share it.

**The differential oracle is behind `EARTH_TEST_ORACLE=1`.** It drives the engine that ships, which
drives a daemon in a container, and when that daemon is unhappy the invocation does not fail - it
stops making progress, in a way that neither `context.WithTimeout` nor `Cmd.WaitDelay` interrupts.
Three hundred and eighty seconds of silence reads as a slow build rather than a stuck one. The rest
of the suite should not be held hostage by a dependency that is not the thing under test; the
differential is the most valuable test here and is worth running deliberately.

**CACHE --persist, 2026-08-14.** `CACHE --persist /state` keeps the cache *and* puts its contents
in the image. Corpus: **407 to 415 targets planned**.

The difference from a plain CACHE is which side of the layer the contents land on, and it decides
the implementation rather than decorating it. An ordinary cache is **bound over** the step's
filesystem, so what goes into it never reaches the overlay's upper layer - that is what keeps it
out of the image, and it is structural rather than filtered. `--persist` asks for the opposite, so
it cannot be a bind at all: the contents are copied in before the step, where the capture will find
them, and copied out afterwards so the next build has them.

In the key, because the two produce different images from the same command. Keying them alike would
let a build hit the other's entry and ship an image with or without a cache in it.

The copy uses the guest's existing `copyTree`, which preserves mtimes because they are part of a
layer's identity (I8). The duplicate written here first would have reset them, producing a layer
whose digest did not match the one just computed - the kind of bug that appears as an unexplained
cache miss much later.

**COPY --pass-args, 2026-08-14.** `COPY --pass-args +target/artifact dest` hands this target's
arguments to the one the artifact comes from. FROM and BUILD already did it; COPY refusing it was an
inconsistency rather than a missing feature, and the flag exists because a target that produces an
artifact usually needs the same arguments as the one consuming it - repeating them at every call
site is how they drift apart.

Explicit `--build-arg` overrides win over the passed-through scope, because writing one is saying
what it should be regardless of what happens to be in scope.

Unimplemented causes **90 to 88**, with targets unchanged at 407: those builds now reach something
further along. The remaining refusals in this family are all real - `CACHE --persist` puts the cache
into the image, `COPY --platform` changes which architecture's artifact is taken, `RUN --entrypoint`
runs the image's own entrypoint - and each changes what is produced rather than how fast it is
produced.

**`paralleltest` is a decision, and here is the evidence for making it, 2026-08-15.** 653 issues,
and the obvious objection - that many tests call `t.Setenv`, which makes `t.Parallel` panic - turns
out to be small: **six files**. 631 test functions could take it.

So it was tried, on `engine/interp`, which is pure planning with no sandbox and no shared cache: 317
functions parallelised, with the race detector as the check. Two tests failed immediately with
`open ../../Earthfile: no such file or directory`.

**The cause is one test and it is not fixable by exempting it.** `TestRelativeContextsAreResolved`
verifies that a relative build context resolves, which it can only do by changing the *process*
working directory - and a process has one. While it runs, every concurrent test that names a
relative path is looking somewhere else. Marking that one test serial does not help, because the
damage is done to whatever runs beside it.

Three ways out, and each costs something:

| approach                 | cost                                                     |
| ------------------------ | -------------------------------------------------------- |
| drop the test            | loses the coverage that found a real path-resolution bug |
| run it in a subprocess   | a test harness inside a test, for one assertion          |
| leave the package serial | `paralleltest` stays at 653                              |

Reverted, and left serial. The point of writing this down is that the objection everybody reaches
for - `t.Setenv` - is not the blocker, and the actual blocker is a single test whose subject *is* the
working directory. That is a decision about what the suite is for, not a lint sweep.

**Then the other packages were done, and the lint turned out to be load-bearing, 2026-08-15.** The
blocker above is confined to one file: `grep -rln 'os.Chdir|t.Chdir' engine/ --include='*_test.go'`
returns `engine/interp/copy_test.go` and nothing else. So the packages that neither chdir nor
`t.Setenv` - core, image, guest, layer, blob, cache, sim, ir - were parallelised: 197 top-level
tests, plus 15 subtest sets that `tparallel` then quite rightly complained were half-done.

**The suite went red, and the bug was in production code.** 15-17 failures a run, all
`race detected`, none reproducible in isolation - because the race needs two goroutines on one
`*ir.Node`, which is what two tests over one package-level fixture are. `ir.(*Node).ID` memoised
into a plain field. The scheduler was safe only by accident: `Run` calls `g.Nodes()` before it fans
out, and `Nodes` walks Inputs, Sources *and* After, so every memo is filled while single-threaded.
Nothing wrote that ordering down and nothing enforced it. Fixed with `atomic.Pointer[NodeID]` -
racing callers compute the same digest, so the store is idempotent and the atomic is there to make
it legal rather than correct. Full write-up in E24.

`paralleltest` 653 to 457, lint 1331 to 1110, and the pure-package suite 2419ms to 2084ms. The
wall clock is the least interesting number of the three: the reason to obey a tedious lint is that
occasionally it is pointing at something. `cli` and `exec` stay serial on purpose - they share VMs,
the image cache and the layer store, and eight concurrent sandbox VMs measure the machine rather
than the engine.

**`govet` splits into a real finding and a declined one, 2026-08-15.** 106 issues: 34 `shadow` and
72 `fieldalignment`.

The shadows are almost all `err` in an inner scope, which is the idiom the language encourages and
not worth touching. **One was not `err`**: `engine/core/schedule.go` had two variables called `stack`
in scope in the function that decides what filesystem a step sees - the step's own stack, and the
stack a copy reads out of. The code was correct; the naming was a trap in the one place a trap is
most expensive. Renamed to `srcStack`, with a line saying why the two are different things.

**`fieldalignment` is declined, and here is the measurement rather than the opinion.** The case for
it is real where a struct is allocated per file, and exactly one struct here is: `layer.entry`, one
per path in a captured tree.

| Struct        | as written | widest-first | saved |
| ------------- | ---------- | ------------ | ----- |
| `layer.entry` | 152 B      | 144 B        | 5.3%  |

At 300,000 files - a Next.js `node_modules` with its npm cache - that is 45.6 MB against 43.2 MB.
2.4 MB, in the best case the codebase offers, bought by ordering fields by width instead of by
meaning. Declined: the reordering costs readability everywhere and buys nothing measurable, and the
`//nolint` noise of exempting 72 sites individually would cost more than either.

The number worth keeping from that table is the other one. **45.6 MB of entries for one captured
tree**, held while the copy into the store runs after it - which is the shape of the ENOMEM in E25,
and remains true now the VM is larger. `layer.Take` is O(files) in memory because entries are sorted
by path before hashing and `filepath.WalkDir` does not visit in that order. Two ways out - hash in
walk order (streaming, but every existing digest changes and §3.3 would need amending) or collect
paths only, sort, then stat in a second pass (same digests, ~5x less memory, one extra stat per
file). Neither is scheduled; both are cheaper than the third option, which is telling people to buy
more memory.

**`goconst` is two lints wearing one name, 2026-08-15.** 216 issues, and where they are decides
what to do about them. *(Superseded 2026-08-17: the verdict below was declined by the decision on
line 422 - fix them all. The reasoning is kept because the burn-down proved half of it right: a
fixture named badly does hide the detail a reader needs, so each name has to say what the value is
**for**. It also proved a third case the table has no row for - a literal a guard reads out of the
source, where naming it blinds the guard and the lint rule loses. See E200.)*

| Where       | count | verdict                                              |
| ----------- | ----- | ---------------------------------------------------- |
| `*_test.go` | 194   | declined - a fixture reads better inline             |
| production  | 22    | mixed, and two of them were worth the whole exercise |

A test that says `alpine:3.22` says what it means; the same test saying `baseImage` has hidden the
one detail a reader of that test needs. Same for `Earthfile:2` and `/bin/sh` in a table of expected
diagnostics. Declined, and the ratio is the argument: 90% of this lint's output is asking for the
tests to be made harder to read.

The production ones are not all alike either. `arm64` appearing three times is a coincidence of
spelling. But `EARTH_CACHE_DIR` and `EARTH_IMAGE_CACHE_DIR` each appeared in two places - where the
variable is *read*, and in the note that tells someone to *set* it - and those two must be the same
string or the remedy silently does nothing. They had already diverged once (E27): the note said
`EARTH_CACHE_DIR` while warning about the image cache, so following it moved a directory that was
not at fault. Now they are constants in `store.go`, and
`TestTheNoteNamesTheVariableTheEngineReads` sets each one and checks the engine's own resolver
follows it - so the two can no longer disagree without the suite noticing.

`/bin/sh` was not a repeated string but a repeated *construction*:
`[]string{"/bin/sh", "-c", cmd}` in six places. That is now `shell(cmd)`, which also gives the
image-that-ships-no-/bin/sh case one place to be dealt with when it arrives. One site in
`engine/cli` still spells it out, because exporting the helper across a package boundary for a
single caller costs more than it saves.

**The hoist orphaned nine `//nolint` comments, and the linter found them, 2026-08-15.** Hoisting
`if err := f(); err != nil` moves the call up and leaves the comment behind on the `if`, where it
annotates nothing - so nine suppressions silently stopped covering the calls they were written for,
and the findings they had been hiding came back.

That is the sort of damage a mechanical rewrite does quietly: the code compiles, the tests pass, and
a deliberate exemption has become a comment about a condition. It was visible only because the
suppressed findings reappeared in the count - which is an argument for running the linter *after* a
sweep as well as before, and for reading the categories rather than the total.

Comments now travel with the statement they are about. `gosec` fell by two on the way, from a defer
in the blob store that closed a file and removed it while discarding both errors: expected to fail on
the happy path, since the file has been closed and renamed away, and now ignored *explicitly* so a
reader knows it was decided rather than forgotten.

**`noinlineerr` 383 to 16, and a claim of mine that was wrong, 2026-08-15.** I said the remaining
49 were all `} else if err := ...`, where the init belongs to the else branch and hoisting would
change control flow. They were not. They were `if _, err := os.Stat(x); err != nil` on one line -
a multi-value left side my pattern required to begin with `err :=` - and three more classes behind
that: assignments with `=` rather than `:=`, lines carrying a trailing `//nolint` comment after the
brace, and multi-value calls spanning lines.

Each was a small widening of the same error-only rule, and each was checked by the whole suite. An
`=` hoist is the safest of them: it changes no scope at all, because it already assigns variables
that exist.

The total ticked *up* by four while this category fell by ten, because hoisting adds a line and other
linters count lines. That is worth stating rather than quietly reporting the good number.

Sixteen remain, and they are genuinely awkward rather than uniform. The category is at 4% of where it
started and the next ten would cost more than they are worth tonight.

**Fixing the leak, and over-reaching while doing it, 2026-08-15.** `noinlineerr` **122 to 49** -
the shapes are now handled in three passes: the plain `if err := x; err != nil`, the compound
condition, and the multi-line call whose terminator is `}); err != nil {`. The last is found by
walking *back* from the terminator rather than parsing forward, which is what the hand-rolled parser
that hung last week was trying to do.

**Then I widened the pattern to every inline assignment and it was wrong.** It hoisted 281 - most of
them `if got := f(); got != want` in tests, which is not what the linter is about - and the fixpoint
that converts a redeclaration to `=` turned collisions between same-named variables of *different
types* into assignments. The compiler refused it, which is the only reason it was caught in the
minute rather than the month.

Reverted whole, and redone with the error-only patterns. That cost the iteration's earlier work and
was still the right move: the alternative was reviewing 281 unreviewed edits to test code at one in
the morning, where a wrong `=` silently assigns to an outer variable instead of shadowing it.

The rule the linter encodes is about *error handling*. A pattern that matches more than the rule is
not a stricter version of it - it is a different change wearing its name.

**The mechanical part of the lint gate, done by the tool, 2026-08-15.** `golangci-lint --fix` and
`golangci-lint fmt` clear the categories that have one right answer: **modernize 50 to 1, perfsprint
8 to 0, gofumpt 8 to 0, whitespace to 0** - about sixty-five issues, none of them a judgement.

**It broke the build, and that is the point of running the whole suite behind it.** The formatter
removed `errors` imports from files that still used them, in three packages. The compiler said so
immediately and the fix was mechanical, but an autofix trusted without a build - or worse, without
the *cross-platform* vet, since one of the three only appears under `GOOS=linux` - would have been a
commit that did not compile on somebody else's machine.

Checked deliberately afterwards: **zero comment lines were removed**. In this codebase that mattered
more than the line count, and it was worth the one command it took to be sure rather than assume.

**And a leak worth naming.** `noinlineerr` went 53 to 122 across this session, because every test
written since used `if err := ...; err != nil` - the idiom this repository does not use and I had
already spent an iteration removing. The burn-down was real and the habit was not fixed, which is the
difference between cleaning something and stopping doing it. The rule for anything written here from
now on: assign, then check.

**A step's filesystem has two case sensitivities, 2026-08-15.** The hypothesis recorded last night
was half right and worth testing rather than believing. It is now demonstrated, and it is stranger
than the guess.

Inside a step, `ls /BIN/SH` **succeeds** and a file the step writes as `Foo` does *not* answer to
`foo`. A step's filesystem is an overlay: its lower layers are image layers read from the store,
which on a stock Mac is case-insensitive, and its upper layer lives inside the sandbox on a
case-sensitive one. So paths a build was *given* answer to any case and paths it *makes* do not, in
the same directory tree.

Most builds never notice. The ones that do are the ones that ask, and `examples/next-js` panics
inside a TypeScript compiler probing exactly that - `failed to stat "/APP/NODE_MODULES/..."`, upper-
cased, because it was checking. That is what the earlier `stale file handle` was about.

The engine now says so once, at the start, naming the store and what the difference is - a warning
rather than a refusal, because nearly everything works and refusing to build on a stock Mac would
refuse the common case to prevent an uncommon one. It explains failures that arrive much later and
look like something else entirely.

**And an image whose own paths collide is refused.** `Foo` and `foo` in one layer cannot both exist
on such a store: one wins and holds the other's contents under its own name, which is a wrong image
produced in silence. Node and TypeScript packages collide this way often enough that it is not a
curiosity. Asked of the filesystem - `os.SameFile` on the two resolved paths - rather than assumed
from the platform, because a Mac may have a case-sensitive volume and a Linux machine may not.

**A test of mine was wrong in the way that matters**, and is worth recording: it skipped when
`Unpack` returned no error, which it did on *both* kinds of filesystem, because the detection under
test did not exist yet. A skip that fires precisely when the feature is missing tests nothing. It now
asks the filesystem what it is and asserts accordingly.

**Two kinds of failure, counted apart, 2026-08-15.** The corpus measurement now reports **30 built,
3 the engine could not do, 7 this machine cannot** - and separating the last two is the point. An
image that provides no manifest for the sandbox's architecture cannot be run here by anything;
counting it as an engine failure makes the number stop moving for a reason nobody can act on.

`exec format error` is now explained where it surfaces rather than passed on. It is what the kernel
says about a binary for another architecture and it names neither platform. The commonest route is an
image cached *before* this engine checked architectures: the step asks for the sandbox's own
platform, so the up-front comparison sees nothing wrong, and the first command fails with six words.
Explaining it at the point it appears catches every route, including the ones nobody has thought of -
and anything that is not that error is passed through untouched, because a command exiting 1 is not a
platform problem and dressing it as one sends the reader away from the cause.

Of the three the engine could not do, one is `examples/go`'s own case-sensitivity bug, already filed.

**One was genuinely unexplained and is now explained - it was two faults, 2026-08-15.**
`examples/next-js` failed inside `npm run build` with `vfs: failed to stat
"/APP/NODE_MODULES/.../TSC": stale file handle` - ESTALE, and a path that has been upper-cased. The
hypothesis recorded here was that APFS's case-insensitivity meets a case-sensitive guest through
virtiofs, and a tool probing for case behaviour finds the seam. That hypothesis was right, and it
was the *second* fault; the first was hiding it.

The first was the sandbox VM taking `container run`'s 1 GiB default. `next-js+deps` never reached
the build step: it finished `npm install` and then failed to copy the result into the layer store
with `mkdir ...: cannot allocate memory`. It reproduced only in the corpus suite, because ten
earlier builds in the same VM had left 724 MB of 1034 MB in page cache. Fixed with `-m 8G`,
overridable, and hashed into the VM's name so raising it takes effect without removing every
sandbox by hand.

With that fixed the ESTALE panic appeared, and the engine's own advice - use a case-sensitive volume
for the build cache - was **tested rather than trusted**: a case-sensitive APFS sparse image as
`EARTH_CACHE_DIR`, and `next-js+build` builds end to end. E25 has the table.

The store still has to be case-sensitive and the engine still only warns about it. Creating a
case-sensitive volume for its own cache is the real repair, and it is not scheduled.

**That judgement was wrong, and the whole corpus said so a day later, 2026-08-15.** Building all 130
targets rather than the first twelve (E26): 96 built, 26 failed - and **19 of those 26 were the
store's filesystem**. Not a corner that catches one Next.js project: `python:3` cannot be unpacked
onto a case-insensitive volume at all, because it ships `usr/share/man/man7/PAM.7.gz` beside
`pam.7.gz`, and `earthbuild/dind` cannot either. On a stock Mac that is the single largest cause of
build failure in the corpus, by a factor of four over everything else combined.

So it moves from "warned about, not scheduled" to the front of the macOS work, and the shape is
already proven: `caseVolumeRecipe` makes a volume that works, and its test runs the commands. What
is missing is the engine doing it for its own cache rather than printing it - which needs a decision
about consent, since it means creating and mounting a filesystem on someone's machine, and a note
was explicitly *not* consent when that recipe was written.

The genuine engine failures in that sweep are five, in three shapes, and they are the other half of
the work list: `COPY /dist: nothing in that target has it`, `the sandbox has no
/usr/local/bin/docker` after WITH DOCKER, and a `go build` with no diagnosis yet.

**Advice that does not work is worse than none, 2026-08-15.** The architecture refusal written an
hour earlier ended with "build for linux/amd64 with `--platform`". Following my own advice pulled
the amd64 image and then failed with `exec format error` at the first RUN, because an arm64 machine
cannot execute amd64 binaries and nothing here emulates them. The message moved the failure and
called it a remedy.

Two changes came out of it. The refusal no longer suggests it: on a machine that cannot execute the
image, building for its platform only moves the failure. And a step about to run is now checked
against what the sandbox can execute, which is where the question actually belongs - **cross-building
is legitimate**, and a target that only copies files for another architecture works perfectly well.
Refusing at the pull would have refused that too.

`Earthfile:4 is for linux/amd64 and this sandbox runs linux/arm64, so it cannot be executed here` -
naming the line, both platforms, and what to do instead.

The lesson is the one worth keeping: a diagnosis that suggests a remedy has made a claim, and a claim
in an error message deserves the same test as a claim in code. This one was written, shipped and
followed within the hour, which is about as fast as a wrong suggestion can travel.

**So the case-sensitivity note was made to earn its advice, 2026-08-15.** It ended with "a
case-sensitive volume for the build cache removes the difference" and left the reader to work out
how - true, unhelpful, and untestable. It now prints the commands, and `caseVolumeRecipe` is the one
place they are written:

```text
  to make one:
    hdiutil create -size 50g -fs "Case-sensitive APFS" -volname EarthBuild -type SPARSE "<path>"
    hdiutil attach "<path>.sparseimage" -mountpoint "/Volumes/EarthBuild"
    export EARTH_CACHE_DIR=/Volumes/EarthBuild/store
```

`TestTheCaseSensitiveVolumeRecipeWorks` runs those commands - through a shell, so the quoting in the
printed line is the quoting under test - and probes the volume they produce. The advice cannot rot
into something that no longer works without the suite going red, which is the whole point of writing
it down as code rather than prose. It builds a disk image, so it is skipped in short mode; it reaches
no network.

Deliberately printed and not run. Creating and mounting a filesystem on someone's machine is their
decision, and a note is not consent. A sparse image because it takes the space it uses rather than
the space it is told, and needs neither a disk to repartition nor an administrator.

Away from macOS the recipe is empty and the note stops after the diagnosis: a case-insensitive store
on Linux is a mount somebody chose, and no fixed set of commands can speak to that.

**Where a thing lands, said once more, 2026-08-15.** A wider sample - 40 targets rather than 20 -
brought back the same rule in two more places, and one failure that turned out not to be ours.

**`SAVE ARTIFACT x y AS LOCAL ./`** wrote the artifact *as* `./` and failed with "is a directory". A
destination that ends in a separator, or is already one, names somewhere to *put* a thing rather than
the thing's new name. That is the third construct needing it - COPY's destination, COPY's `--dir`,
and now an export - and each was written before the rule was clear enough to state.

**An image built for another machine is refused, saying which.** A multi-architecture image is an
index and the right manifest is chosen from it; a *single*-manifest image has nothing to choose from,
so nothing checked it. The failure was `fork/exec /bin/sh: exec format error` from inside the
sandbox - a message with nothing in it to connect to an image, an Earthfile or a platform. The
configuration says what the image is and is fetched now, so the mismatch is named where it happens.
An image that says nothing about itself is still trusted: that is old or unusual rather than wrong.

**And the last failure in the narrow sample was not the engine's.** `examples/go/Earthfile` runs
`go test github.com/earthbuild/earthbuild/examples/go` while its own go.mod declares
`github.com/EarthBuild/...`. Go import paths are case-sensitive, so it fails on any engine and
reproduces on main. Filed to the nits file rather than fixed here: it is one line, and this branch is
not the place for it.

**An artifact lives in the target's stack, not its last layer, 2026-08-15.** Corpus builds **18 to
19 of 20**, and the clojure example - `lein uberjar`, a version extracted from the jar's filename, a
`SAVE IMAGE` tagged with it - builds end to end.

A copy read the producing node's **own layer**. That works whenever the artifact is made by the
target's last step, which is most of the time and was every case until now. Clojure's build makes the
jar, then reads a version out of it, then saves the jar - so the jar is two layers down, and the copy
said the pattern matched nothing. True of that layer, false of the target.

The port now passes source **stacks** rather than single layers, and the guest searches newest first,
because a later layer replacing a file is the later file. The *key* still uses each source's result
layer: a source's final layer is its whole content, so identity is unchanged and only what a copy can
reach is wider. Keeping those two apart was the whole of the change - the first attempt used stacks
for both and would have altered every key in the cache to fix a lookup.

**And `SAVE ARTIFACT <path> <name>`.** The version in a filename is decided by the build -
`app-*-standalone.jar` - and the name is decided by the author, which is what the ENTRYPOINT two
lines later uses. `COPY +build/*` now lands each artifact under the name it was given.

`Artifact.Name` was refused by the seam test at first, on the grounds that the CLI does not read it.
It is consumed inside the interpreter instead, and the entry now says so - which is the difference
between a field with no consumer and a field whose consumer is somewhere the test does not look.

**A build context belongs to its own Earthfile, 2026-08-14.** Corpus builds **13 to 18 of 20**, and
the monorepo example - three Earthfiles, cross-directory artifact globs, a SAVE IMAGE - builds end to
end.

A build has one `-dir`, and an Earthfile referred to across directories has its own: `../js+build`
copies index.js from beside *that* Earthfile. The executor joined every context path to the
invocation's directory, so a referenced target read files out of the caller's tree.

**The plan was right and the execution was wrong**, which is why the earlier cross-directory tests
passed: they checked that the *interpreter* resolves a neighbour's files against the neighbour's
directory, and it does. Nothing carried that answer to the executor, and the failure arrived one
layer down from the tests that were looking for it.

The directory travels in `Meta` rather than in the operation, because identity is the file's
**content**: two identical files in different directories are the same layer and should stay one.
That is the same reasoning that keeps `After` and `OnFailure` out of the key, arriving from a
different direction.

**A probe runs where the build is, and a pattern is matched where the files are, 2026-08-14.**

`WORKDIR /var/app` then `SAVE IMAGE app:$(cat version)` reads a file the line above put in
/var/app. The probe ran at `/`, looked for a file the Earthfile never mentions, and reported the
command as failing - which reads as a broken Earthfile rather than a working directory nobody
carried. A probe observes the build state, and *where* it observes from is part of that state, so
the working directory now travels with it.

The directory comes from the **interpreter**, not from the last step: WORKDIR changes the state
without producing a step, so a step's own Dir is whatever it happened to be and not where the build
now is. A test asserting the old behaviour was updated rather than deleted, because the property it
was checking - that a probe inherits the build's context - is still the right one; only the source of
that context changed.

**And a pattern is matched against the layer that has the files.** `SAVE ARTIFACT
target/uberjar/*-standalone.jar` names a file whose version the build decides, so it cannot be
resolved when the plan is made. It is matched in the guest, against the filesystem that has it, and
one match is required: a copy has one destination, and choosing between several is the author's
business rather than this engine's guess. A pattern matching nothing names the pattern, so the
message is about that rather than about a file with a star in its name.

**Artifact globs, and the monorepo that came with them, 2026-08-14.** Corpus builds **9 to 13 of
16**.

`COPY +target/*` names everything that target saved. It is not a path: passing the `*` to the guest
asked it to stat a file literally called `*`, which no layer contains. It is expanded when the plan
is made rather than in the guest, so each artifact is its own copy and **the key covers exactly what
was taken** - a producer that starts saving a second artifact is a different build and should look
like one. A glob over a target that saves nothing is refused, because copying nothing would produce
an image quietly missing whatever the author meant.

The monorepo example came back with it: `COPY ../html+html/* ./` is the same glob, reached across
directories. A pair of tests went in for the cross-directory question anyway - a target in another
directory must read *its own* directory, by reference and by BUILD - and both passed, which is worth
having written down: that part was already right, and the failure was the glob wearing a monorepo's
clothes.

**Three more from building the corpus, 2026-08-14.** The `cutoff-optimization` example - compile in
one target, link in a second, run in a third, passing artifacts between them - now builds end to end.
Three bugs stood between it and that, and none of them could have been found by planning.

**A layer may name its own root.** A tar built with `tar -C rootfs .` begins with an entry called
`./`, busybox's included. Resolving it gives the unpack root, whose *parent* is outside the layer -
which is what the escape check looks at, so the check refused the one entry that cannot possibly
escape. `busybox:1.38.0` could not be pulled at all, and said the layer wrote through a symlink out of
itself.

**A relative artifact follows the working directory.** `WORKDIR /code` then `SAVE ARTIFACT main.o`
means /code/main.o, as it does for a RUN and for a COPY destination. Taking it from the filesystem
root produced "no such file" against a path the Earthfile never wrote.

**And a consumer names the artifact, rather than giving a path.** `COPY +build/main.o .` names what
that target saved; where it saved it is that target's business. Reading the name as a path looked for
/main.o, and - this is the part worth keeping - the failure surfaced in the *consuming* target, two
steps and one target away from the line that decided it. A consumer now asks the producer where its
artifact went.

**Whiteouts, and `COPY src .` - a regression of my own, 2026-08-14.** Corpus builds **5 to 7 of 10**.

**Whiteouts are deletions here, not overlay markers.** An image's layers are unpacked into one
directory, so `.wh.X11` means "remove etc/X11" - and the engine was writing the *overlayfs* form
instead: a character device 0:0, which describes a layer that stays separate and is stacked later. In
a tree already flattened it is meaningless at best and a stray device file at worst. It also needed
CAP_MKNOD and CAP_SYS_ADMIN, so it worked only as root on Linux and refused everywhere else -
`clojure:temurin-8-lein` could not be pulled at all, and the diagnosis said "needs overlayfs" to
somebody who had not asked for one. It now runs: `Leiningen 2.12.0 on Java 1.8.0_492`.

Build layers are a different model and still stack; `engine/mat/overlay` is where that lives and
nothing here touches it.

**And then `COPY src .`, which was a regression I introduced three days ago.** Making `.` keep its
trailing separator fixed the *file* case - `COPY x .` under a WORKDIR - and broke the *directory*
case, because the separator was being asked to carry two opposite meanings:

| source      | `--dir` | what it means                   |
| ----------- | ------- | ------------------------------- |
| a file      | -       | place it inside the destination |
| a directory | no      | contribute its **contents**     |
| a directory | yes     | place the **directory** inside  |

`COPY src .` put the tree at `./src`, one level from where `gcc -c main.cpp` on the next line looks
for it - which is why that example failed three times in one measurement. `--dir` is now a flag on
the step and in the key, rather than a separator on the destination, and the guest decides from the
source's own kind.

**A note on what this cost to find.** The wrong result was cached under a *correct* key: the key
covered `--dir` before the guest honoured it, so the first fix appeared to do nothing and three runs
looked identical. A key describes what a step is asked to do, not whether the implementation did it -
which is obvious in retrospect and was not at the time.

**A step could not resolve a name, so no build could fetch anything, 2026-08-14.** The corpus-build
measurement went **0 built to 5 of 6** in one sitting, and the largest single reason is this: a step
had no `/etc/resolv.conf`.

An image ships none, because the runtime is expected to provide one. Nothing did. Every build that
fetches anything resolves a name first, so maven, npm, pip, apt and cargo all failed - each with its
own unrelated-looking error, none of them mentioning DNS. It is the third member of the family that
began with `/dev` and continued with `/proc`: things an image assumes and a runtime must supply.

Bound from the sandbox rather than written, because what the resolver should be is the machine's
business and inventing a nameserver is guessing at somebody's network. It is ambient state, and worth
saying so plainly - but so is the network itself, which is why `RUN` is what it is. Nothing about
what is cacheable changes; a step that was going to fetch can now do so.

**And `/etc/gshadow`, which Debian ships with mode 0000.** Not readable by anyone, root included,
because root ignores modes and nobody else has business with it. On Linux this engine runs as root
and never notices; on a developer's machine it is an ordinary user, and `SAVE IMAGE` failed with
"permission denied" on a file the image legitimately contains. Relaxed, read, and put back - the same
pattern the unpacker uses, safe for the same reason: this process owns the tree.

**What is left of that run is one failure and it is a design question.** `clojure:temurin-8-lein`
carries `etc/.wh.X11`, a whiteout, and whiteouts are refused because this engine unpacks an image's
layers into a single directory where a deletion cannot be expressed. That is a real limit of
flattening rather than a bug, and the next thing to decide.

**An attempt that failed, recorded because the lesson is the useful part.** Finishing `noinlineerr`
meant handling multi-line and compound shapes, and the transformer written for it was a text parser
for Go syntax - which is exactly where a text transform stops being the right tool. It hung on a
loop, was killed, and the eight files it had touched were restored from the index, which is why the
verified state was staged in the first place. The remaining 53 are hand work. The AST is the correct
tool and reprinting from it risks the comments, which in this codebase are the point.

**`noinlineerr`: 383 to 53, and it was not a decision after all, 2026-08-14.** Flagging it twice as
needing a judgement was itself the mistake. The repository has already decided: `.golangci.yaml`
enables the linter, the Earthfile pins the version, and `util/` complies. Conforming to a project's
checked-in policy is not a call to escalate - it is the work.

Done as a burn-down rather than a sweep, smallest packages first, so the *procedure* could be proved
on 26 sites before being pointed at 450. The transformation is mechanical only in its first half:
hoisting `if err := f(); err != nil` out of the `if` changes the scope of `err`, so wherever one is
already in scope the `:=` becomes `=` - and that is a real semantic change, from shadowing an outer
variable to assigning it.

**The compiler is what makes it safe, and the fixpoint is the method:** transform, build, let the
compiler name every redeclaration, convert those, repeat until clean. 269 hoists, 103 conversions,
seven rounds. Then the same again for the multi-value shape - `if got, err := f(); ...` - which needs
both names hoisted and turned up a genuine collision: a test had an `out` from a command's output and
a second `out` for the build's buffer, invisible while one was scoped to an `if`.

Three places the fixpoint could not see, each caught by something else in the pipeline: the
linux-only files, which only `GOOS=linux go vet` compiles; the linux-only *test* files, which report
in a different format; and a program under `testdata/`, which nothing vets and the sandbox suite
builds at run time. The cross-platform vet in `verify-engine.sh` earned its place three times in one
afternoon.

Verified by the whole suite including the sandbox one, which actually runs builds - the only check
that would notice a hoist having changed what a function does.

**Where the gate stands now: 1622 issues to 1110.** What is left is dominated by `paralleltest`
(457), which is now a decision that has been *made* rather than deferred: the pure packages are
parallel, `interp` is serial because one test's subject is the working directory, and `cli` and
`exec` are serial because they share a VM, an image cache and a store. The rest of the count is
those three packages, and it stays.

The sentence that stood here - "adding `t.Parallel()` to them would not be conformance, it would be
a race" - was half right and worth keeping as a caution: it *was* a race, in `ir.(*Node).ID`, and
the race was ours and predated the tests by months. See E24.

**The repository's own linter is a merge gate this branch does not pass, 2026-08-14.**
`.golangci.yaml` is `default: all` minus a disable list, and the Earthfile pins golangci-lint
**2.12.2** - the version this was run with, so the numbers are the ones CI will produce. `util/`
reports 2 issues. `engine/` reports **1622**.

Two categories are most of it and both need a decision rather than a sweep:

| category     | count | why it is a decision                                               |
| ------------ | ----- | ------------------------------------------------------------------ |
| paralleltest | 609   | many of these tests use `t.Setenv`, which *forbids* `t.Parallel`   |
| noinlineerr  | 383   | `util/` has 1 inline-err and `engine/` has 334 - a real divergence |

The second is worth stating plainly: this engine was written with `if err := f(); err != nil`
throughout, and the repository it is going into does not use that idiom anywhere. "Match local
style" was followed at the scale of the surrounding lines and missed at the scale of the repository.

**What was done rather than deferred: directory permissions.** `G301` flagged fourteen sites, and
they are not one question. A directory this engine *owns* - the action cache, the layer store, the
image cache, an unpack root - is now `0750`: the cache holds what a machine has built and the keys
those results are filed under, which is a record of what somebody works on. A directory that becomes
part of an **image**, or that a user asked an artifact to be written into, keeps `0755` and says why:
a directory a non-root user in that image cannot traverse is a build that works here and fails
wherever the image is run.

There is a test asserting the cache is not world-readable, which is the property rather than the
number.

**And an honest note on the count:** it went from 1622 to 1633 during this work, because the new
tests written for the bugs above add to the two structural categories. The debt is structural, not
accumulating through carelessness, and it will move when those two questions are answered.

**A step had no /proc, so no JDK could run, 2026-08-14.** `maven:3.8.5-openjdk-17` failed with
`libjli.so: cannot open shared object file` naming a library that was **present, readable, the right
size, a valid ELF, and resolvable by `ldd`**. Every obvious explanation was wrong, and the file being
demonstrably there is what made it worth chasing rather than filing.

The loader computes `$ORIGIN` from `/proc/self/exe`. A step had no /proc, so an rpath using
`$ORIGIN` - which is every JDK and a great many toolchains - resolved to nothing, and the failure named the
library rather than the reason. `ldd` works because it resolves without needing it, which is exactly
what made the symptom so misleading.

Every step now gets a proc filesystem. A *fresh* one rather than a bind of the sandbox's, so a step
sees its own processes and not the guest's - which would be ambient state a step can observe and no
key describes (I3).

`RUN mvn --version` now prints `Apache Maven 3.8.5`.

**And a fourth place for the mode rule.** Capturing a step's result copies its delta, and
`maven`'s image has `/root` at 0700 with a step writing `/root/.m2` inside it - so the copy created
the directory with its declared mode and could not then put anything in it. `copyTree` in the guest
now applies directory modes at the end, deepest first, exactly as the unpacker and `linkTree` do.

Four places, one rule: **a restrictive directory mode is applied last**. Each was found by running a
real image rather than by reading the code, and none of them could have been found by planning.

**Building the corpus rather than planning it, and three bugs in the first four minutes, 2026-08-14.**
Every construct in the corpus plans. Nothing said whether any of it *runs*, and this engine has
spent the week proving those are different questions: `COPY x .`, `WORKDIR` + `COPY`, a step with no
`/dev`, and ENV taking PATH with it all produced flawless plans.

`TestCorpusTargetsActuallyBuild` runs them instead. It reports rather than fails, because a corpus of
other people's Earthfiles needs networks and credentials this machine has not got and a suite that
went red for those is one nobody reads.

**The first version of the harness was itself the lesson.** No deadline, no cap, and a single test
whose output Go buffers to the end: it ran for half an hour against hundreds of targets and printed
nothing. That is the same defect as a check whose log is overwritten before anyone reads it. It now
has a deadline, a cap, and a subtest per target - which is what makes results arrive as they happen
rather than all at once or never.

**Its first real run failed on the first image it touched**, and behind that were three bugs, all one
rule: *a restrictive directory mode must be applied last*.

`maven:3.8.5-openjdk-17` ships `usr/bin` at 0555, and the files inside it come after it in the
archive. Applying the mode when the directory was created made every one of them fail with
"permission denied", so the whole image was unpullable. A directory's mode describes the image, not
the unpacking of it, and it is now applied at the end of a layer, deepest first.

That fixed one layer and exposed the next: layer 1 adds more binaries to the `usr/bin` layer 0 left
read-only, and by then the mode is real on disk. Such a directory is now made writable for the write
and put back afterwards - a different case from a mode deferred within one layer, and it needs its
own answer.

And then `linkTree`, which copies a cache entry into a build's layer store, failed identically for
identical reasons. Three places, one rule, found by pulling one image that nothing in the test suite
had ever pulled.

A fourth followed from it: `os.RemoveAll` cannot delete a tree containing a directory that denies
writing, because removing a file needs write permission on the directory holding it. A half-pulled
image could not be cleared and its staging directory stayed for ever. `image.RemoveAll` makes
directories writable on the way down, which is safe precisely because the tree is being deleted.

**Where it stands:** maven now unpacks and its step runs, and fails further along with
`libjli.so: cannot open shared object file`. That is the next thread and it is a real one - the file
is present in the cache entry - but it is a different problem from the three above, and worth
starting from a clean measurement rather than chased at the end of a long day.

**One construct left, and it is a decision rather than work, 2026-08-14.** The corpus is down to
**485 targets planned and 5 blocked by a single unimplemented construct: `RUN --privileged`.**

A target's output can now be a Dockerfile's build context - `FROM DOCKERFILE -f ./Dockerfile
+context/*`, the most-written form of the construct in the corpus. The Dockerfile itself still comes
from beside the Earthfile, because the context is what the *build* reads and the Dockerfile is what
says how to read it: looking for it in the target's output would need that target built before
anything could be parsed.

`--allow-privileged` is now accepted and grants nothing. It permits a referenced target to use
`RUN --privileged`, which this engine refuses wherever it appears - so the permission has nothing to
act on, and the only way accepting it can be wrong is by refusing a build the shipping engine would
run. That is the safe direction, and refusing the flag did the unsafe-looking thing of failing a
build over a permission nobody could have used. There is a test asserting a privileged step is still
refused *through* the flag, because that is what makes accepting it safe rather than convenient.

**What is left is a question this document should ask rather than a task it should list.**
`RUN --privileged` cannot be confined, so its result cannot honestly be cached (A3, I7). It is
therefore not "unimplemented" in the way `WITH DOCKER` was: it could be run *unconfined and
uncacheable*, exactly as `LOCALLY` already is, and that is a decision about what this engine is
willing to do rather than a feature nobody has got to yet. Recorded as such, and deliberately not
counted as remaining work.

The three corpus causes it accounts for are one fixture family: two targets whose build context is a
privileged target, and the privileged step itself.

**Multi-stage Dockerfiles, 2026-08-14.** The number that did not move last time moved a long way:
targets **blocked** fall from **376 to 5**, because the case behind them - `buildkit/Earthfile:10` -
was one stage building on another, and 374 targets were waiting behind that single line.

Two shapes, and this IR already had both. A stage standing on another is an **input**: its node is
the base the next stage begins from. `COPY --from=<stage>` is a **source**: read and never stacked,
which is the difference between carrying one file out of a builder and carrying the whole builder -
and is exactly what `COPY +target/artifact` already rests on. There is a test asserting the builder
does *not* end up in the image it was copied from, because that is the failure that would otherwise
pass unnoticed: the file is there either way.

`COPY --from` is the one instruction that cannot be said as an Earthfile command - it names a node,
and an Earthfile has no way to say "stand on that" - so it is built directly while everything else
still goes through `p.command`. That keeps the exception to one instruction instead of forking the
translation.

Stages are built **on demand**, not in order: only what the selected stage depends on runs. Building
the rest would do work the Earthfile never asked for, and on a file with a `test` stage that is
precisely the work somebody excluded on purpose. A loop between stages is a diagnosis naming the
cycle rather than a stack overflow, and each stage gets its own environment and working directory -
a later stage inherits nothing from an earlier one but the files it is handed.

Corpus: **480 to 485 targets planned**, and blocked targets **376 to 5**. What is left is two `FROM`
causes and one `RUN` flag: five targets in total.

**`FROM DOCKERFILE --target` and `--build-arg`, and a corpus number that did not move, 2026-08-14.**
A multi-stage Dockerfile with no `--target` builds its last stage, which is Docker's own rule and was
previously grounds for refusing the whole file - a property that does not affect the answer. `--target`
names one instead, and a target that is not there is refused listing the stages that are. `--build-arg`
supplies values for the Dockerfile's own ARGs exactly as a build argument does for a target, and is
restored afterwards because it belongs to that Dockerfile and not to the rest of the Earthfile.

**The corpus is unchanged at 480 targets, and that is worth stating rather than hiding.** The case
this was aimed at - `buildkit/Earthfile:10`, 3 causes and 374 targets - uses both flags and is still
refused, for a different reason found only by trying it: its `buildkit-linux` stage builds *on
another stage*.

That is the next piece and it is a real one. A stage standing on another needs that one built first,
and `COPY --from=<stage>` needs its filesystem as a source. Both are shapes this IR already has -
Inputs for a base, Sources for something read and never stacked - but neither can be expressed by
translating to `earthfile.Command`, because an Earthfile has no way to say "stand on that node". The
translation will have to build stage chains as nodes directly, keeping a map from stage name to the
node it ended at, and hand those to the same COPY machinery `+target/artifact` already uses.

Refused rather than resolved, meanwhile, and deliberately: an unrecognised `FROM builder` would
otherwise be taken for an image reference and *pulled from a registry*, which is a build that fails
confusingly at best and succeeds against a stranger's image at worst.

**`FROM DOCKERFILE`, translated rather than delegated, 2026-08-14.** The last large construct, and
the decision recorded two days ago went the way it was expected to. A Dockerfile is now *parsed and
translated into the commands this interpreter already runs* - its FROM, RUN, COPY, ENV and WORKDIR
mean what the Earthfile spellings mean, so they become the same steps.

That is the whole argument for it. The Dockerfile's contents decide the keys, its steps land in the
same layer store, and changing one line of it re-runs one step. Handing it to `docker build` would
have been less code and would have put the result outside every guarantee this engine makes: a
daemon's cache is keyed by nothing here, so the result could not be cached, shared or reproduced.

Cheaper than expected, twice over. The Dockerfile parser is already a dependency of this repository -
`docker2earth` uses it - so nothing had to be written to read the file. And translating to
`earthfile.Command` rather than to `ir.Node` means the whole thing runs through `p.block`: every rule
about quoting, keys, mounts and working directories applies without being restated, including the
ones fixed this week.

Supported: single-stage, with `-f`. Refused by name: more than one stage, `--target`, `--build-arg`,
`COPY --from`, a target as the context, and every instruction outside the set above. An instruction
silently dropped produces an image that is not what the Dockerfile describes, and nothing downstream
can tell.

**Two bugs it surfaced, both older than it.**

`WORKDIR /app` then `COPY x .` created `/app` as a *regular file*. Resolving `.` against the working
directory produced `/app`, and a destination with no trailing separator names a file - so the copy
renamed rather than placed, and the failure arrived two steps later as `mkdir /app: not a directory`.
The trailing separator is not decoration and `.` carries its meaning without carrying the character.
This was wrong for Earthfiles too and had a test asserting the wrong answer, written by me three days
ago.

`RUN --entrypoint` with nothing after it was refused as needing a command. It is complete without
one - an image whose entrypoint is a whole program needs no arguments - and the corpus writes exactly
that.

Corpus: **478 to 480 targets planned**, unimplemented causes **6 to 4**. What is left is
`FROM DOCKERFILE` with a target as its context (24 corpus lines, and the largest single form), with
`--target`/`--build-arg` (5), and one `RUN` flag.

**`RUN --entrypoint`, 2026-08-14.** `namely/protoc-all` is an image whose entrypoint *is* protoc, and
`RUN --entrypoint -- -f api.proto -l go` means "run that, with these flags". Without it such an image
can only be used by knowing what its entrypoint happens to be and writing it out by hand, which is
the thing the image exists to avoid. Verified end to end against `node:20-alpine`, whose
`docker-entrypoint.sh` now runs.

The entrypoint is read at execution rather than planned, because only the fetched image knows it -
and it is in the step's key already, through the image the step stands on. `Op.Entrypoint` is keyed
because running an image's entrypoint with some words and running those words as a command are
different operations. The arguments are exec form: they go to a program, not a shell, and a shell
would re-split them.

Three things had to be fixed on the way, each a place where a design decision had a consequence
nobody had followed through:

**The configuration was stored per node and had to be stored per image.** A second target naming the
same image links the tree from the shared cache and never pulls - so the file existed only for
whichever node happened to fetch it, and every other one saw an image that declared nothing. It now
lives beside the shared cache entry and follows the tree to each node that links it.

**Written before the directory existed.** The copy to the node's layer ran before `linkTree`, which
is what creates the parent - so it failed with ENOENT into a dropped error, and an image with an
entrypoint was reported as having none. Moved after the link.

**A bare command name resolved against the wrong PATH.** Go resolves argv[0] when the command is
built, using this process's environment, which is the guest's and not the step's -
`docker-entrypoint.sh` was reported as not found while sitting in the image's own `/usr/local/bin`.
It has never come up because every other step runs `/bin/sh`, and an absolute path needs no
resolving. Resolution now happens against the step's PATH inside the step's filesystem, and an
unresolvable name is left alone so the failure is the kernel's and names what was asked for.

Two tests had to learn about the sidecar file: one counted raw directory entries in the image cache
and saw one image as two, and one pulled into `sb.Store` - the override field, empty until something
resolves it - rather than `sb.StoreDir()`.

That second one did more than fail. Pulling into `""` joined to a *relative* path, so an entire
alpine root filesystem was unpacked into the working directory, which for a test is the package under
test: `engine/exec/layers/` appeared in the repository and went into the staged set, four hundred
files of it, noticed only because the staged count jumped from 244 to 667. It is the third time the
`Store`/`StoreDir` distinction has bitten, so there is now a test asserting the store resolves to an
absolute path - the property whose absence is what turns a wrong path into a mess rather than an
error.

Corpus: **474 to 478 targets planned**, unimplemented causes **8 to 6**. What is left is
`FROM DOCKERFILE` and one `RUN` flag family.

**An image declares things, and the engine now reads them, 2026-08-14.** `Pull` fetched manifests
and layers and never the configuration blob, so `FROM node:20-alpine` gave `NODE_VERSION=[]`. An
image is a filesystem *and* a declaration about how to run it - ENTRYPOINT, ENV, WORKDIR, USER - and
half of that was being thrown away.

The configuration is verified like any other blob, because it decides what a container runs: a
substituted one chooses the command. It is written *beside* the layer rather than inside it, since
what an image declares is not part of the filesystem it ships - putting it in the rootfs would add a
file to every image built on this one.

The step's environment is now three layers, weakest first: a **floor** this engine guarantees, then
what the **base image** declared, then **ε**. Each wins over the one before, because each is more
specific about this step. The floor stays because an image may declare nothing at all, and a step
with no PATH falls back to whatever the shell compiled in - which omits `/usr/local/bin`. Last
iteration that floor was the whole answer, which was a stopgap and is now a floor.

Not a violation of I3, and worth saying why: the base image is an *input* to the step and already in
its key, so its declarations are not ambient state - they are part of what the step was told to
stand on.

Still to come from the same blob: `RUN --entrypoint`, which now has something to prepend, and
`SAVE IMAGE` inheriting what its base declared.

**A security review of the unpacking change, and what it was right about, 2026-08-14.** An automated
review flagged the new replace-then-write path as arbitrary file overwrite through a planted symlink:
layer one writes `config -> /etc/passwd`, layer two writes a regular file called `config`.

Two thirds of it was already covered, and by accident rather than by design in one case. `safePath`
resolves an entry's *parent* and refuses anything landing outside the layer, which stops escapes
through an ancestor; and the leaf case is stopped because `os.Remove` does not follow a symlink, so
the link is unlinked rather than written through.

The part worth acting on is that the safety had become a property of `replacing()` having run
correctly rather than of the write itself. There is now a test that plants exactly that symlink and
asserts the file outside is untouched. Recorded rather than dismissed, because "it happens to be safe
two functions away" is how the next edit reintroduces it.

**The engine could only pull single-layer images, 2026-08-14.** `FROM node:20-alpine` failed with
`create "etc/apk/world": file exists`. Unpacking used `O_EXCL` against the filesystem, and that flag
cannot tell apart the two things it was being asked to distinguish: **one layer naming a path twice**,
which is a malformed archive, and **a later layer replacing an earlier one's file**, which is the
whole of what layering means.

So every image with more than one layer failed, and almost every real base image has more than one.
alpine has exactly one - which is why the corpus, the sandbox suite and four days of end-to-end
builds all passed without touching it.

The fix keeps the distinction the flag was reaching for, by tracking what *this* layer has written
rather than what is on disk. It applies to every entry kind, not only regular files: the second
attempt got past the file case and failed on `symlink "usr/bin/strings": file exists`. A directory is
the one thing that may legitimately already be there, since two layers both containing `/usr/bin` are
not in conflict.

Found by going looking for something else entirely - `RUN --entrypoint`, which needs the base image's
configuration - and picking an image with more than one layer for the first time.

**And the thing that was actually being looked for is still missing.** `Pull` fetches manifests and
layers and never the config blob, so this engine does not know any base image's ENTRYPOINT, ENV,
WORKDIR or USER. `FROM node:20-alpine` gives `NODE_VERSION=[]`. Three things follow from it:
`RUN --entrypoint` cannot work, `SAVE IMAGE` of a derived image loses what it inherited, and the
PATH baseline added to the guest last iteration is a hardcoded stand-in for the image's own. That
baseline should stay as a floor and stop being the whole answer.

**A correction: the `FROM` row was never propagation, 2026-08-14.** Three entries in this document
said the corpus's `FROM` row - 5 causes, 376 targets, the largest number on the board - was targets
inherited from something `WITH` blocked. That was asserted from the shape of the number and never
checked. It is wrong.

It is **`FROM DOCKERFILE`**: building a Dockerfile as a stage. `buildkit/Earthfile:10` is the first
of them. It is a real construct, it is the largest remaining piece of work by a wide margin, and it
had been written off three times.

The report is why, and the report is now fixed: it named constructs and never a line, so "FROM, 5
causes" invited exactly the guess that was made. Each construct now prints one example location,
sorted so it is the same one every run. Naming a place to go and look is the difference between a
work list and a rumour.

Two shapes are open for it, and neither is an increment. A Dockerfile *frontend* translating to this
IR keeps the whole caching story - our keys, our layers - and is a large piece of work with a long
tail of syntax. `docker build` inside the sandbox is much smaller now that a daemon is there, and
inherits the daemon's unbounded state, which is the thing already forcing `WITH DOCKER` blocks to be
uncacheable. The first is almost certainly right and is a project to start deliberately.

**PROJECT and the last WITH DOCKER options, 2026-08-14.** `PROJECT org/project` names who a build
belongs to, which the hosted service resolves secrets against; this engine resolves secrets from the
invocation and nowhere else, so it is validated and otherwise does nothing. It was going to be
*recorded* on the plan - and `TestEveryPlanOutputIsConsumed` refused it, on the grounds that a plan
field nothing reads is a feature built ahead of its consumer. That is the criticism this document has
made of other people's code twice this week, and the codebase caught me at it. The field arrives when
the code that reads it does.

`--build-arg`, `--pass-args` and `--platform` now carry into a loaded target exactly as they do for
FROM, BUILD and COPY - a construct that spelled them differently is one people have to learn twice.
`--build-arg NAME=VALUE` needed its own parsing: `overrides` reads the `--NAME=VALUE` form used
inside a parenthesised reference, so it matched nothing here and the argument silently never arrived.
`--cache-id` and `--allow-privileged` remain refused; both change what the daemon *is* rather than
what is in it.

Corpus: **471 to 474 targets planned**, unimplemented causes **13 to 8**. The work list is now
`FROM DOCKERFILE` and three `RUN` flags. Nothing else.

**`WITH DOCKER --compose`, and two bugs it uncovered that matter far more, 2026-08-14.** Compose
brings services up before the block's commands and takes them down after. Both halves are the
feature: the daemon outlives the build, so a service left running is there for the next build and
every one after it. `--wait` is not optional either - `up -d` returns when containers have started
rather than when they are ready, and the first line of such a block is usually something that
connects to one, so without it the failure is a connection refused that succeeds on a retry.

Compose needs a project name, which it takes from the working directory's basename - and a step
whose WORKDIR is the image root has none, reported as "project name must not be empty". It is now
derived from the compose files, which also makes `down` find what `up` started without anything
passed between them, and put in ε as `COMPOSE_PROJECT_NAME` so the body's own `docker compose ps`
means what its author obviously intended.

**Two bugs came out of this that have nothing to do with compose, and both were general.**

**A step had no devices.** `/dev` held `null` and nothing else - no `urandom`, no `zero`, no `random`.
That is most language runtimes, every TLS handshake and a great deal of package management, all
failing on a filesystem that looks fine. An image ships an empty /dev and expects the runtime to
populate it; nothing did. The standard set is now bound in from the sandbox for **every** step -
bound rather than created with mknod, because a bind needs no privileges this does not have and the
alternative is a list of major and minor numbers to get wrong.

The symptom that led there was much smaller and much stranger: `docker compose` reported itself as an
unknown command, because docker's plugin loader opens /dev/null while collecting metadata and every
plugin failed to load.

**And ENV took PATH with it.** `cmd.Env = req.Env` inherits the parent environment when the slice is
nil and replaces it entirely when it is not - so an Earthfile with no ENV got a PATH by accident, and
one with a single ENV lost it and fell back to the shell's own default, which omits `/usr/local/bin`
where pip, npm, cargo and docker all put things. The symptom is `sh: docker: not found` on a line
whose only crime is following an ENV.

A *declared* baseline now sits under ε - PATH and HOME, written down in the guest, the same on every
machine. That is not the ambient state I3 forbids: it is a constant, not an observation. An Earthfile
that sets PATH still wins, because an Earthfile that sets PATH means it.

Both were found by following a failure that looked like it belonged to the feature being built, and
neither would have been found by the corpus, which never runs anything.

Corpus: **454 to 471 targets planned**; WITH falls from 14 causes to **4**, and unimplemented
constructs from 23 to **13**. The work list is now `FROM` (propagation), `WITH --cache-id` and
`--platform`, three `RUN` flags, and `PROJECT`.

**`WITH DOCKER --load`, 2026-08-14.** 480 of the corpus's 892 WITH DOCKER lines, and the one that
makes the construct worth having: it is how a build tests the image it has just made. A build now
builds a target, packs it as an OCI image, loads it into the daemon and runs a container from it -
verified end to end by a container printing a file an earlier step of the same build wrote.

**Two steps, because two things happen in two places.** `OpPackImage` writes the layout on the
machine running the build, from layer directories and a configuration it holds; an ordinary
`Docker: true` exec loads it inside the sandbox. A single step would have to be half host and half
guest, which is the one thing this engine's step model does not express.

Four bugs, and every one of them is a distinction this engine already had:

**Input against Source.** The load step took the packed image as an *input*, which merged the
target's whole layer stack into it - and since both share a base, the stack then named one layer
twice, which overlayfs refuses outright. A source is read and keyed and never stacked, which is
precisely the difference between standing on something and reading it. The scheduling sweep caught
this, not a unit test.

**Visible is not reachable.** The archive is written into the store that host and guest share, and
`docker load -i /var/lib/earthbuild/store/...` reported it missing. It was there - but a step runs
chrooted into its own overlay, and the store is outside it. `ir.Mount` gained a sandbox path so the
archive is mounted into the step that reads it, the same way the docker socket already was.

**A platform is not optional.** The packed image declared none, so the load succeeded and the very
next `docker run` reported the image as not present locally and tried to fetch it from a registry.
The node's platform when it has one, the executor's otherwise.

**And the name.** With only `org.opencontainers.image.ref.name`, the image was listed by
`docker images` *twice*, denied by `docker image inspect`, and sought in a registry by `docker run`.
It ran perfectly by ID, which is what proved the image was right and only its name was wrong:
containerd's image store keys on `io.containerd.image.name` and wants a fully-qualified reference.
`image.FullReference` now expands one, by docker's own rules - a first component with a dot or colon
is a registry, anything else a Docker Hub namespace, a bare name is `library`, an absent tag is
`latest`.

Corpus: **421 to 454 targets planned**; WITH falls from 41 causes to **14**, and unimplemented
constructs overall from 49 to 23. The remaining WITH causes are `--compose`, which brings up
services and is a different kind of thing from putting an image in a daemon.

Two classifications had to be made rather than inherited. Packing is `SpeculateRetryable` - it writes
a content-named file into the cache, and a wrong guess costs what a cache miss costs. A step with a
daemon is `SpeculateNever`, because it puts things into one that outlives the build, which the next
block sees whether or not the branch that asked for it was taken. That is the same state that makes
these steps uncacheable, arriving in a second place.

**`WITH DOCKER --pull`, 2026-08-14.** 86 of the corpus's uses, and the whole of it is one idea:
a pull is a **step**, not a property of the block.

That is not a shortcut, it is what the construct means. Fetching an image is work; it can fail; it
has to happen before anything that uses the image; and what was pulled has to be part of what the
body is. All four come free from being an ordinary node - ordering from Inputs, failure from the
scheduler, identity from the key - where a flag on the block would have needed each one arranged by
hand.

It writes nothing to the step's own filesystem, because the image goes into the daemon, which is
outside it. So its layer is empty and standing on it costs nothing, which is what makes the whole
approach work rather than merely tidy.

The body's key already covers what was pulled, and that matters before it is load-bearing rather
than afterwards: the block is uncacheable today, so nothing reads those keys - but a key that was
wrong while nothing read it is a cache poisoned the moment something does. There is a test for it.

Two smaller things, recorded because both cost a run. The synthesised node first put the whole
command in `Args[0]`, which is not how a RUN is encoded - `argv` produces `/bin/sh -c <command>`,
and execve given a sentence reports the sentence as a missing executable. And the refusal table in
`withdocker_test.go` had to lose `--pull`, which is the third time this session a test has correctly
failed because the thing it asserted was unsupported stopped being so.

Corpus: **417 to 421 targets planned**, WITH down from 45 causes to 41. Next is `--load` at 480 uses,
which is the last big one and needs an OCI image built mid-graph rather than at export.

**A WITH DOCKER step is not cached, and finding that out was the point of shipping it, 2026-08-14.**
The hazard recorded before any of this was built turned out to be live in the first version of it.
The daemon outlives the build that used it, so `docker run alpine ...` leaves an image behind and the
next build's `docker images` observes it - state no key describes. The step was being published to
the cache, which makes it the worst failure this engine can produce: a build that passes because an
earlier build left something behind, and fails on a machine that never ran it.

I7 already says what to do. A key that cannot bound what a step observed must not become a cache
entry, so `Op.Docker` joins `OpHost` and `--no-cache` in the scheduler's uncacheable set - neither
looked up nor published. The cost is that a WITH DOCKER block re-runs on every build, which is not a
regression but the honest price of a daemon whose contents are not in the key.

This is a stopgap with a known ending, and worth stating so it is not mistaken for a design. When
`--load` and `--pull` land, what enters the daemon is declared in the command and therefore keyable,
and a per-block data-root bounds the rest; the rule then narrows to what is genuinely unbounded
rather than covering the whole block.

Two things had to learn the new reason. The corpus's cache-hit invariant counts the steps that must
run again, and would otherwise have reported a correctly-uncached step as a cache failure. And the
report's outcome column was eight characters wide, which is two short of `uncaptured` - so the
description went out of line on exactly the steps whose outcome most needs reading.

**WITH DOCKER runs, 2026-08-14.** A bare `WITH DOCKER ... END` now works end to end: `RUN docker
images` returns a listing and `RUN docker run --rm alpine echo ...` runs a container inside a build.
That is 96 of the corpus's 892 uses, and the options remain refused by name.

**The load-bearing assumption was tested before anything was built on it.** Nested docker inside an
Apple `container` VM works out of the box with `docker:27-dind` - no privileged flag, no special
configuration. Had it not, the whole design would have been different, and finding out after
building the interpreter half would have been the expensive way to learn it.

Three things fell out that are worth keeping:

**A step is given a daemon by mounts, not by anything new.** The client and its socket belong to the
machine and outlive the step, which is precisely what a mount is for and what a layer is not - so
`ir.Mount` gained a sandbox path and the existing machinery carried the rest. `Op.Docker` is in the
key, because `RUN docker images` with a daemon and the same line without one are different requests:
the first lists images, the second fails to find the command.

**The VM naming built for reuse separated the two machines for free.** A sandbox is named after its
image, so a project with a WITH DOCKER block gets its own VM and a project without one keeps the
small image - no daemon to boot, no process left running.

**The bug worth recording.** The first end-to-end attempt waited ninety seconds for a socket that
was never going to appear. The VM boots with `sleep 86400` as its command, which for the dind image
overrides the entrypoint - and that entrypoint *is* dockerd. The result was a VM with a docker
client, a socket path, and nothing listening. An image that provides a daemon now runs its own
entrypoint; only the plain image, which has none worth running, is held open with a sleep.

The wait itself stays, and is the right behaviour rather than a workaround: a daemon takes several
seconds to create its socket after a boot, and failing because it has not arrived yet would make a
build succeed or fail on how long the machine took to start. Only the first build after a boot waits
at all - the VM outlives a build, so every later one finds the daemon already up.

Corpus: **417 targets planned**, WITH down from 49 causes to 45. The eight targets that moved are
ones that now run rather than ones that merely parse, which is the distinction this engine keeps
being tempted to blur.

**Still to do, in the order the corpus asks for it:** `--load` (480 uses), `--compose` (98),
`--pull` (86). And the hazard below is unchanged and unaddressed - the daemon currently persists with
the VM, so its state is shared between builds.

**WITH DOCKER stages and the determinism hazard, 2026-08-14.** With the
withheld-capability family extended to cover GIT CLONE and remote checkouts, the work list reads:
`WITH` 49 causes, `FROM` 5 (propagation from targets `WITH` blocks), `RUN` 3 (flags that each change
what runs: `--privileged`, `--ssh`, `--aws`, `--oidc`, `--network`, `--entrypoint`, `--interactive`).
Nothing else.

**Scoped by what the corpus actually writes**, not by the option list. Across 892 `WITH DOCKER` lines:

| option or shape   | lines |
| ----------------- | ----- |
| `--load`          | 480   |
| `--compose`       | 98    |
| `--pull`          | 86    |
| no options at all | 96    |
| `--platform`      | 24    |
| `--cache-id`      | 12    |

The bodies run `docker run` (192), `docker inspect` (108) and `docker images` (108). **There is no
useful slice of this without a daemon** - even a bare `WITH DOCKER` wrapping `RUN docker images`
needs one - so the staging cannot start anywhere else, and any increment that plans the construct
without running it would only turn a clean refusal into a corpus number that means nothing.

What makes it affordable now is the persistent VM (E20). A daemon inside a VM that is booted per
build would cost its start-up on every build; inside one that persists, it starts once and stays.
The sandbox VM is already named after its image, so a project that needs docker gets its own VM
without disturbing one that does not - the naming scheme built for reuse turns out to carry this
for free.

Stages, each ending somewhere honest:

1. A sandbox image carrying dockerd, selected when the plan contains WITH DOCKER - the same
   inspection `needsSandbox` already does on the plan before choosing an executor.
2. dockerd started inside the VM on first use, persisting with it.
3. `--load`: build the referenced target, write it as an OCI image - `engine/image` already does
   this - and load it. The reference is an ordinary graph edge, so the key covers it.
4. `--pull`: fetch through the existing image cache and load.
5. `--compose`: needs compose in the image; the least-used and last.

**The hazard to design against, stated before any of it is built.** A daemon that outlives a build
carries state no key describes: images loaded by an earlier build, containers it left running. A
step that observes one of those has observed something outside its key, which is exactly what I3
forbids, and the failure is the worst kind available here - a build that passes because a previous
build left something behind, and fails on a machine that never ran it.

`--load` and `--pull` are declared in the command, so what a block *asks for* is keyable. What a
previous block *left* is not. The answer is therefore a per-block data-root rather than pruning
between blocks - pruning is a promise to remember every kind of state docker can hold, and the list
grows with docker. That reloads images per block, which is the cost, and it is precisely what
EarthBuild's own `--cache-id` exists to buy back: persistence of that layer data, named by the
author, and therefore in the key.

**COPY was wrong in the two most common ways of writing it, 2026-08-14.** Found by measuring the
inner loop (E19) rather than by reading the corpus, which is the point worth keeping: the corpus
proves an Earthfile *plans*, and both of these produced a perfectly good plan and then failed - or
worse, succeeded - at execution.

`COPY src.txt .` failed outright, because the guest tested for a trailing separator to decide whether
the destination was a directory to place the file inside. True of `/app/`, false of `.`, so it tried
to create a file *as* the overlay's merged root. `WORKDIR /app` then `COPY . .` silently put the
files at the filesystem root, and reported it two steps later as a RUN that could not find a file
which had definitely been copied.

The second is the more instructive. The destination is now resolved against the working directory
**when the plan is made**, not inside the guest - where a file lands is a static fact about the step,
so it belongs in the step's identity. Two COPYs of one file into two working directories are
different operations and must not share a key. That property already held, because `Op.Dir` is
keyed; it now has a test saying so, which it did not before.

**The sandbox boots beside interpretation, 2026-08-14.** The second thing a build waits for that it
need not. A sandbox was built lazily at the first probe, or after interpretation for the build
proper; on macOS that is a VM boot, and it sat squarely on the critical path with nothing overlapping
it. A project that has ever run a condition needed a sandbox to do it and will almost certainly need
one again, so `shouldWarm` reads that off the prediction store - no new persisted state - and the
boot now overlaps parsing, digesting the build context and everything else interpretation does.
Being wrong costs one unused VM boot, the same shape of cost as a wasted image pull.

Warming broke an implication the code was leaning on, and the fix is the more interesting half.
`executorFor` read **"a sandbox exists"** as **"a probe needed one"**, which was true only while a
probe was the sole way one came to exist. Left alone, a build of nothing but LOCALLY steps would
silently switch executors because something warmed a VM in the background - a hint changing a
result, which is exactly what a hint may not do (I5). `started` and `used` are now separate, and
`executorFor` decides on the plan and on `used`, never on the field being non-nil.

**And a data race that no test could have caught.** `sandboxed` fills `g.ex` inside a `sync.Once`,
and a warm-up runs that Once on a background goroutine; `executorFor` and `close` read the field
directly, having never called `Do`. The race detector cannot see it without a real VM to boot, so it
is asserted structurally instead: a test greps `conditions.go` for reads of `g.ex` outside the
accessor. Every path to the sandbox now goes through `sandboxed()`, including the one that only
wants to shut it down - a shutdown racing the boot would otherwise either miss the sandbox it meant
to close or read a half-written pointer.

A second field fell out of it. `ex` was doing duty as both "the sandbox" and "the executor this build
uses", so a host-only build overwrote the warm sandbox and nothing closed it; `host` is now its own
field and `close` shuts down both.

Verified against the sandbox suite as well as the unit tests, because the lifecycle is the part unit
tests cannot reach.

**The prefetch was in front of the build, not beside it, 2026-08-14.** The freely-speculable tier
had a defect that its own comment described the opposite of. `prefetch` ended in `wg.Wait()` and was
called before `interp.Build`, so every build with a confident prediction stalled at startup for the
full duration of the predicted pulls, serialised, before interpreting a line. The comment claimed it
"takes the transfer off the critical path entirely"; it put the transfer at the *head* of that path,
which is strictly worse than not prefetching at all - a wrong prediction was paid for in full before
the build had started.

It now returns a waiter instead, started at the top of the build and waited for on the way out:
`defer prefetch(...)()`, whose arguments evaluate at defer time, so the pulls begin immediately and
only the wait is deferred. Concurrency with the build is safe without further work because the image
cache already stages a pull to one side and renames it into place, and losing that rename race is
handled - a property that was there for two builds racing and turns out to cover a build racing
itself.

The waiter is not bookkeeping. A build that has returned must not leave pulls running against a
cache directory it has stopped using, so it is waited for rather than abandoned.

Two tests hold the shape: one asserts control returns while a pull is still in flight, the other
that the waiter does not return until every pull has. Neither could have been written against the
old signature, which is the tell - a function whose only observable behaviour is "it has finished"
cannot be asked whether it started.

**Still no consumer for tiers 2 and 3.** `core.MaySpeculate` is built, measured and exercised by the
corpus sweep, and nothing in the engine calls it: speculative *execution* needs the plan for a branch
that interpretation has not reached, so it waits on the learned tree points rather than on the
classifier. Recorded here so the classifier is not mistaken for the feature.

**CATCH, 2026-08-14.** The last construct outside the `WITH DOCKER` family. `TRY` and `FINALLY`
already worked; `CATCH` was refused because it runs commands *because* the try failed, and treating
its body as ordinary steps would run recovery over a build that went perfectly well - the opposite
of what was written.

It needed one new thing in the scheduler: a step that runs conditionally on another having failed.
`ir.Node.OnFailure` names the guarded step, and sits with `After` on the far side of identity. The
reason is sharper than After's: it decides *whether* a step runs, never what it computes, so a
handler must key identically to the same command written outside a TRY or it misses a cache entry
it is entitled to. There is a test asserting exactly that.

Skipping is transitive through inputs, which is what makes a handler of more than one command work
without a second mechanism: only the first command names the guarded step, and the rest are skipped
by standing on one that was. The alternative - running the second command against whatever it could
still reach - executes half a recovery over a build that never went wrong.

The handler stands on the failed step, because a failure is only worth inspecting where it left
things, and that is the same reason FINALLY does. It is a side branch: the build after END carries
on from the TRY, and gets a root of its own in `Also` so it is scheduled despite nothing depending
on it. Threading the rest of the build through the handler would make every later step wait on
commands that usually do not run at all.

Corpus: **408 to 409 targets planned**, 61 unimplemented causes to 60.

**A condition may name something no ARG declared, 2026-08-14.** `IF [ "$CARGO_HOME" = "" ]` in
`lib/rust/Earthfile` was refused as testing an argument that was never declared. The refusal was
false. CARGO_HOME is set by the rust image, and the Earthfile is under no obligation to declare a
variable it did not invent - the engine had simply not looked in the one place the name lives.

`decide` now consults the environment this build state carries (ε) before giving up, so a name set
by ENV is decided in the plan for nothing, and a name from neither ARG nor ENV makes the condition
*undecidable here* rather than wrong: it goes to a probe, whose shell sees exactly what the step
would. Substitution within a token is all-or-nothing, because a token half-substituted is compared
as though the rest were empty, which is how a condition takes the wrong branch with nothing looking
amiss.

The cost is deliberate and worth stating: a mistyped argument name in a condition used to be caught
by that refusal and now becomes a probe that quietly compares against the empty string, exactly as
the shipping engine does. Correctness of the answer beat the diagnostic - a probe is never wrong,
only slower - but this is the one place in the interpreter where a typo lost its diagnosis.

Corpus: two causes move out of unimplemented and into "needs a probe" - **61 unimplemented causes**,
33 probe causes. Targets planned unchanged at 408, which is the expected shape: these targets were
never buildable without a sandbox and still are not, they are just no longer accused of a mistake
they did not make.

**Probes are not unimplemented, 2026-08-14.** Fifteen separate rows at the top of the corpus's
"this is the work" list read like fifteen missing commands - `SAVE IMAGE ... has to be run to know
its value`, `LET ...`, `FOR ...`, `BUILD ...`. They are one mechanism, `expandCommands`, general to
every command except RUN, ENTRYPOINT and CMD - the three that are handed to a shell whose job this
already is. It is implemented and the CLI wires a runner to it. The corpus plans without one, so it
was counting finished work as work to do.

`ErrNoRunner` now makes the distinction by type rather than by reading the message, and the corpus
reports a third bucket. Unimplemented causes fall **86 to 63**, with 31 causes and 36 targets moving
to "blocked only for want of somewhere to run a probe". Nothing was built to achieve that; the list
was simply wrong about where the work is, which is worse than it being long.

Writing the test that was meant to prove the family already worked found the one member that did
not. **`ARG v = $(cat version)` was not expanded** - only `LET` was - because ARG returns from
`command` before either expansion, and stored the literal text `$(cat version)` as the value. It
carries a subtlety LET has not: a default is used only when the caller supplied nothing, so the
probe runs on the default path and nowhere earlier. `ARG v = $(git describe --tags)` in a target
whose caller always passes `v` would otherwise run a command whose answer is discarded - and in the
build where this matters that command does not work at all, the default existing precisely because
the tool is absent. A discarded value is cheap; a discarded failure stops the build.

Corpus: **419 to 408 targets planned**. The drop is the fix. Those eleven targets were "planning"
with a variable whose value was the four-character string `$(ca` onwards - the command itself,
carried into an image tag or an artifact path. They are now refused, and say what they need.

**`COPY --platform` and the word `native`, 2026-08-14.** `COPY --platform=linux/amd64
+producer/binary .` builds the referenced target for that platform and takes the artifact from it.
FROM and BUILD already carried a platform into a referenced target; COPY refusing it was the same
inconsistency `--pass-args` was, and the flag exists because a build often needs one artifact from
an architecture other than the one it runs on - a cross-compiled binary being the ordinary case.

Parsing the flag immediately surfaced a second construct behind it: `--platform=native`, which the
corpus uses and which is not a malformed platform but a word meaning *the machine this build runs
on*. It resolves to a concrete platform rather than to "unset", and the difference is the point -
unset means inherit, and the whole use of the word is to escape an inherited foreign platform.
`FROM --platform=linux/amd64` followed by `COPY --platform=native` that quietly returned amd64
would cross-compile while reading as if it had not.

Corpus: **415 to 419 targets planned**; the two refusals that named no construct - the honesty
check the corpus test enforces - are gone, and refusals fall from 91 to 89.

Also settled, and worth recording because it removes a candidate: the `FROM` row in the corpus
report shows 5 causes blocking 376 targets, which looked like the largest lever after `WITH`. It is
not a lever at all. Those are targets whose base is a target blocked by something else, `WITH`
chiefly - propagation, counted once at the top of each chain. `WITH DOCKER` is genuinely the only
large blocker left.

**COPY inside LOCALLY, 2026-08-14.** `COPY +target/artifact <path>` in a LOCALLY target puts a
built artifact on this machine. Corpus: **401 to 407 targets planned**.

The refusal it replaces said there was no image to copy into, which was true and beside the point:
the author did not ask for an image, they asked for a directory on the machine the target already
runs on. Every COPY inside a LOCALLY target in this repository is that shape. Copying the *context*
is still refused, and now says why - the file is already there, at the path the line names.

Recorded as an artifact export rather than a step, because that is what it is, and it means one
implementation of "put this where the user asked" rather than two.

**A destination outside the project is allowed here**, unlike `SAVE ARTIFACT AS LOCAL`. That rule
exists because an Earthfile - possibly fetched from elsewhere - must not choose where to write on
someone's machine. A LOCALLY target is already running arbitrary commands there: refusing the copy
while allowing `RUN cp x /etc/passwd` would be theatre. The real hazard, remote code with a LOCALLY
target, is older and larger than this line and is not made worse by it.

**The corpus invariant caught a bug in this within minutes of it being written.** A target
exporting several artifacts from different producers had only the first producer in the graph; the
rest named steps nobody would ever schedule. "Every artifact is produced by the graph" was written
two days ago as a property that passed on everything - and its value turned out to be catching the
first thing that broke it.

**GIT CLONE, 2026-08-14.** `GIT CLONE [--branch ref] <url> <dest>` puts a repository into the
image. The checkout becomes an ordinary copy source, so it is content-addressed like every other:
digested at graph construction, so a build whose dependency moved gets a different key. Keyed on
the URL instead would leave the graph unchanged when the branch advanced, and the build would hit
the cache and reproduce the previous checkout - the most damaging false hit available, because it
looks like a fast build.

A seam of its own rather than the one Earthfile references use. That takes a repository path this
engine builds a URL from; GIT CLONE is handed a URL as written, `ssh://git@github.com/x.git` among
them, and reusing the other would mean guessing which half of the string the caller meant.

The checkout is keyed on url *and* ref, and an unpinned clone is fetched afresh each time - for the
reason an unpinned image reference is: "whatever that repository holds now" cannot be answered from
a directory written last week.

The corpus number does not move, because the corpus has no cloner, which is the plan-only guarantee
holding: producing a graph must not reach the network.

**Secrets as environment, 2026-08-14.** `RUN --secret TOKEN` and `RUN --secret NAME=SOURCE` give a
step a credential as an environment variable - the other route, with a different trap. `Op.Env` is
hashed, so a value placed there would be in the cache key: written to disk, shared between machines,
and impossible to retract. The node records the *names*, which are keyed because asking for a
different secret is a different step; the values are added at execution and exist nowhere in the
graph.

Refused by the name it would come from rather than the name it arrives as, because `SOURCE` is what
the caller has to supply and naming `NAME` would send them to look for the wrong thing.

The same walk-the-whole-cache test, because the mechanism is different even though the property is
the same: a value that reached `Env` would be in a key rather than in a layer, and no test of the
mounted route would have noticed.

**Secrets, 2026-08-14.** `RUN --mount=type=secret,id=TOKEN,target=/run/token` gives a step a
credential. The riskiest thing built here, and the design is arranged so the dangerous outcome is
impossible rather than avoided.

**The value is not in the graph.** The IR carries a secret's *id* and nothing else; the interpreter
is told only which names the invocation supplied, so it can refuse a step asking for one that does
not exist. The executor is the single place a value is read. A design that carried the value and
excluded it from the key would work until someone added a hasher, and that failure is a credential
in a cache key.

**The value is not in the layer.** A credential written into the step's own filesystem is captured
with everything else the step wrote and ends up in the image - shipped, pushed, public. The guest
writes it to a private file *outside* the step's root and binds that in, the same mechanism a cache
uses and for the same reason, then removes it when the step is done. Read-only, because a step that
could write through the mount would be writing into wherever the invocation keeps its credentials.

**A step given a secret is not cached**, for the reason a cache-mounted step is not: its output may
depend on something no key bounds (I3).

The test is the point of the feature. A step measures the secret it was given - proving it could
read it - and the artifact carries the length, never the value. Then the *entire build cache* is
walked for the string, and the build's own output too. Nothing about "we mount it from outside"
would be worth believing without that.

`--secret` on a RUN is still refused, as is a secret nobody supplied: running with an empty file
would fail somewhere far from the line that asked, usually with a message about authentication that
sends the reader to the wrong system.

**RUN --mount, 2026-08-14.** `RUN --mount=type=cache,target=/x` mounts for that step and no other,
which is the difference from `CACHE` and the reason both exist: CACHE declares something about the
rest of the target, a mount on a RUN is about that command. A step inheriting another's mount would
see a directory its author never asked for. Corpus: **380 to 401 targets planned**.

Only `type=cache` is provided. A `secret` hands a credential to a step and a `tmpfs` gives it
memory that disappears; neither is a cache, and providing a cache instead would run the step with
something other than what it asked for. A silently absent secret is the worst of the three, because
the command that needed it fails somewhere else entirely - so the refusal names the *type* rather
than the flag.

The corpus writes five of these and three are `type=secret`, which is the next thing this plumbing
makes reachable: a secret is a mount whose source is not a cache.

**CACHE works, 2026-08-14.** `CACHE /root/.m2` mounts a directory that outlives the build into
every step after the line, and a sandboxed build proves it: the first build writes into the cache,
the second appends to what the first left. Corpus: **368 to 380 targets planned**.

**A step carrying a cache mount is not cached**, and this is a deliberate divergence from the
engine that ships. What such a step produces may depend on what was in the mount, which no key can
bound (I3), so there is no honest key for its result - the same reasoning I7 applies to a host step.
The mount is what makes it fast; the action cache cannot also claim it. A false hit is worse than a
slow build, and the mount removes most of the cost anyway.

`--persist` and `--sharing` other than `locked` are refused rather than ignored: the first puts the
cache's contents into the image, and the others describe how concurrent users interleave. Accepting
either while doing something else is the silent-wrong failure this engine is arranged against.

**The mount carries an id, not a path, and finding out why cost two failed runs.** The host and the
guest are different machines - a VM on macOS - and the store the host sees at one path is mounted
elsewhere inside the guest. Sending a host path had the guest create that path in its *own*
filesystem, so the first build's cache was written somewhere that vanished with the VM. Then
`filepath.Dir(LayerDir)` walked one level too far up, because LayerDir is the store root rather
than the layers directory, and landed outside the shared mount for the same result.

Both failures looked identical from outside - "the second build saw 1 line" - and neither was
visible to any unit test, because both are about which machine a path means something on. The e2e
exists for exactly this.

**Mounts reach the IR, 2026-08-14.** `ir.Op.Mounts` carries them, the executor turns each into a
directory beside the layer store named by the id the Earthfile gave, and the guest binds it. Still
nothing in the interpreter produces one - `CACHE` is next - so no target plans that cannot run.

**The paths reach the key; the contents cannot.** That is the whole difficulty with a cache mount
and the reason a step carrying one is not soundly cacheable: what it produces may depend on what
was in the mount, which no key can bound (I3). Mounting somewhere else is a different step, so the
paths belong in the key; trusting a result that depended on the contents would be the false hit
this engine exists to prevent.

The key-coverage guard refused the new field rather than passing it - it had never met a struct
slice and said so instead of claiming cover. Taught generally rather than by case, so the next
struct field in `ir.Op` is covered without the guard being edited, which is the property a
hand-written guard loses first.

Changing the hash then broke a test three packages away, and the break was worth having.
`TestCopyExpandsAPattern` asserted on the order of `Graph.Nodes()`, which sorts by node identity -
so it was asserting on a property of a hash. It compares a set now; the order that *is* meaningful,
which source wins when two write the same path, is the COPY chain and has its own test.

**The mount needs no filter, 2026-08-14.** `guest.Step` now carries mounts to the sandbox, and
writing the layer that follows produced a better answer than the one planned. A capture takes the
overlay's *upper* directory, and a bind mount bypasses the overlay for that subtree entirely - so
what a step writes into a cache goes to the bound source and never reaches the upper.

**The exclusion is structural, and the filter written for it was deleted.** That is the same shape
as `Sources` being keyed but never stacked: a rule that holds because of how the thing is built
cannot be forgotten by whoever adds the next call site, and a filter can. The mount *point* still
appears in the layer as an empty directory, which is correct - the path existed in the step's
filesystem.

`ExecIn` grew a `Step` struct rather than a seventh parameter. Six was already too many to read at
a call site, and the next two would have been positional booleans.

**Mounts, from the bottom (2026-08-14).** The guest protocol carries mounts: a directory on the
machine running a step, bound into that step's filesystem before the chroot. Built bottom-up
deliberately - nothing in the interpreter accepts `RUN --mount` or `CACHE` yet, so no target plans
that cannot run. The corpus number is unchanged and that is correct.

**A mount is not a layer, and the difference is the whole feature.** A layer is stacked and becomes
part of what the step produces; a mount is a hole in that filesystem onto something that outlives
it. `CACHE /root/.m2` wants the second: a compiler's cache that survives to the next build and is
*not* in the image. So `mountedPaths` exists to tell a capture what to leave out - including it
would put an entire compiler cache into an image and make the step's identity depend on it.

Three kernel details, each one a way this goes quietly wrong:

* bound **before** the chroot, because the source is a path only the guest can name and afterwards
  there is no way to reach it;

* read-only needs a **second** `mount` call - the flag is ignored on the bind itself, and assuming
  otherwise silently produces a writable mount;

* unmounted in reverse order, since a mount inside another has to go first.

**The protocol version went to 3.** An older guest would ignore an unknown field and run the step
*without* its mount - a step that cannot see its cache, reporting success. That is exactly the
failure the version exists to prevent, and the reason bumping it is not optional bookkeeping.

The test asserting the version refusal now takes the number from the constant. It failed on this
bump, which was right, and a test that must be edited on every bump invites editing the assertion
instead of thinking about the change.

**The audit one level down, 2026-08-14.** The seam guard asks whether every output a `Plan`
declares is consumed. Each of those outputs carries fields of its own, so the gap simply moves
inward - and `Image.Push` is recorded by the interpreter and read by nothing.

That one is **right**, and the difference is the point. Pushing happens when the *invocation* asks
for it, which is how the tool that ships behaves, and this engine has no invocation flag to ask
with - so recording the declaration and not acting on it is correct. What is not acceptable is that
being indistinguishable from an oversight, which is exactly how it looked. The guard now covers
`Image` and `Artifact` fields too: a field listed says someone decided, a field missing says nobody
has. Mutation-proven by adding an unaccounted field.

The audit did find one real gap, and it is about what the build *says* rather than what it does. An
image declared `--push` was written with no mention of the push, so someone who wrote `--push` and
watched a build succeed had every reason to believe it was published. The line now reads
`app:latest -> /path (declared --push; not pushed - this engine writes images, it does not publish
them)`.

Thirty-five `SAVE IMAGE --push` lines in this repository were being answered with silence.

**The loop closes, 2026-08-14.** A condition that must be run is run; which way it went is recorded
against **where it is written**; what the build needed is attributed to it; and a later build with
that history pulls those images into the shared cache **before interpreting anything**. Proved end
to end on a sandboxed conditional: the history names the line, the condition and `alpine:3.22`, and
three consecutive builds take the same branch.

Nothing here changes what is built. The condition is still evaluated and still decides (I5) - only
when the bytes move. That is the whole claim, and it is why every part of this can fail silently:
a prefetch that does not happen costs a pull later, and one that fetches the wrong image costs
bandwidth.

The wiring only became honest once the image cache existed. Two iterations ago the same mechanism
would have pulled into a directory nothing would ever look in; the note then said so rather than
claiming a speedup, and the note is why this one is real.

Attribution is deliberately coarse - every image the build used, against every site it decided.
Exact attribution would need the interpreter to track which nodes came from which subtree, which it
has no other reason to do, and the error is in the direction that costs bandwidth rather than
correctness.

**The image cache, 2026-08-14.** Images are now kept under a key of **reference and platform**,
beside the layer store rather than inside it. The layer store is keyed by node identity, which is
right for a step's output and wrong for a base image: two targets that both begin
`FROM alpine:3.22` have different identities for the same bytes and were fetching them twice.
Proved end to end - two targets, one entry in the cache.

Three details each answer a way this goes wrong:

* **Linked, not copied.** A layer is read-only to a step (§3.3b), so two names for one file is
  exactly what is wanted: no bytes move, and no step can write through one name to disturb the
  other. A copy falls back only across filesystems.

* **Pulled aside and moved into place.** A half-written entry is worse than none - the next build
  would find a directory, believe the image was there, and build on a fragment.

* **Platform is in the key.** The same name on two architectures is two sets of bytes, and serving
  one for the other is a container that will not start. That failure is worse than the pull it
  would have saved.

This is also the reservoir the previous iteration said was missing. `exec.Prefetch` now has
somewhere to put bytes, so the mechanism built then is no longer pointing at nothing - though the
call that would use it during a build is still to come, and saying that remains more useful than
wiring it and hoping.

**Prefetch is built and deliberately not wired (2026-08-14).** `prefetch` fetches what
confidently-predicted branches needed last time, and `Predictions.Needed` records it. Both are
tested: a confident site fetches its branch's images and not the other's, an unconfident or
alternating site fetches nothing, and a failed fetch cannot fail a build - a prefetch is a hint,
and one that could fail a build would make a hint load-bearing.

**It is not called from anything, and that is the honest state rather than an oversight.** Wiring
it would spend bandwidth and save nothing: `image.Pull` writes into a directory named by *node
identity* and keeps no blob cache keyed by digest, so an image fetched before the graph exists has
nowhere to be found. The later pull would download it again.

Saying so is the point. Twice today a lower layer declared something the layer above never read,
and both times the build reported success for work that did not happen; connecting these two with a
puller that warms nothing would have been the same defect, dressed as a feature and harder to spot
because the tests would pass.

**What it needs is an image cache keyed by reference and platform** rather than by node identity -
at which point the prefetch has somewhere to put the bytes and `materialiseImage` has somewhere to
look before reaching for the network. That is the next piece, and it is worth having on its own:
today two targets that both start `FROM alpine:3.22` pull it twice if their node identities differ.

**The tiers, built and measured (2026-08-14).** `core.MaySpeculate` answers what may be done about
a step before the branch that needs it is known, in the three tiers below. Measured across the
corpus - 367 graphs, 2,311 steps:

| tier      | steps | share | what a wrong guess costs                       |
| --------- | ----- | ----- | ---------------------------------------------- |
| freely    | 870   | 37%   | bandwidth                                      |
| retryable | 1308  | 56%   | a layer nobody uses - the cost of a cache miss |
| never     | 133   | 5%    | a side effect that cannot be taken back        |

**93% of a real build could be started before the answer is known**, which is what makes a
speculator worth building rather than an idea worth admiring.

Two properties do the work. It is **transitive**: an ordinary `RUN` looks retryable on its own, and
if it stands on a `LOCALLY` step then speculating on it means running that step first, so the
weakest tier of everything involved wins. And **ordering edges are deliberately not followed** -
`WAIT` is about when work lands, not what it costs to guess, so treating it as a barrier would
suppress speculation after every WAIT block: a performance cliff at exactly the construct people
reach for when they care about correctness.

The corpus sweep asserts the property that makes the tiers trustworthy rather than merely
plausible: a graph containing nothing that touches the machine must be speculable throughout. A
"never" in such a graph would be the classification refusing work for a reason nobody could name.

An unrecognised operation is `never`. Refusing to speculate on a kind added later costs a little
speed; guessing that a new kind is retryable could cost a side effect.

**Speculation, shaped (Giles, 2026-08-14).** The blocking round trip below has an answer that does
not need the graph to be known first, and it is more useful than waiting: **these tree points have
been reached before, so cache them and let the prediction start down the path pre-emptively.**

What may be done speculatively divides cleanly, and the division is by reversibility:

* **Preloading layers is always safe.** It moves bytes and changes nothing. Wrong, it costs
  bandwidth; right, it removes the transfer from the critical path entirely. This is the floor -
  worth doing on every prediction, however weak.

* **A retryable step may be run.** Its result is a layer keyed by content: if the prediction was
  wrong the layer is simply never used, and if it was right the work is already done. That is the
  same shape as a cache miss, which is the cost model this engine keeps arriving back at.

* **`LOCALLY`, `--no-cache` and anything that pushes must wait for certainty.** They are not
  functions of their inputs - they touch the machine, the clock or a registry - so running one
  speculatively is not wasted work but a *side effect that should not have happened*. There is no
  layer to discard afterwards.

That gives the predictor three tiers rather than one switch, and the engine already distinguishes
all three: `Op.NoCache` and `OpHost` are exactly the "wait for certainty" set, and `Image.Push` is
the third. The classification a speculator needs is already in the IR because it was needed for
caching, which is a good sign the model is the right shape.

**What the evaluator costs when the work is not here (2026-08-14).** A condition that must be run,
and a `$(...)` that must be expanded, are answered by executing a probe on the filesystem the
recipe has built so far. On one machine that is a sandbox exec: measured at 2.3s for the first and
about 1.0s after, because the prefix it stands on is cached.

Distributed, the same call is a different shape and this was not considered when it was written.
Interpretation **blocks** on it, so the round trip is not overlapped with anything: the prefix must
be materialised somewhere, a worker must be chosen, the probe must run, and the answer must come
back before the *graph can be extended at all*. Every step after the condition is unknown until it
returns, so there is nothing else to schedule in the meantime. On a fleet with a warm layer store
that is a network round trip; on a cold one it is a layer transfer first.

The engine's other properties survive distribution well, and it is worth being clear about which:

* keys are content-derived and **proved deterministic** across runs, so an entry written by one
  machine is found by another - the whole premise of a shared cache;

* layers are content-addressed, so moving one is a copy rather than a rebuild;
* the ordering edge added for WAIT is a dependency like any other, and costs nothing extra;
* a `--no-cache` or host step is uncacheable everywhere, not just here.

The mitigation is the one already specified: **prediction** (green paper §3.4a). A predicted branch
lets the graph be extended before the probe returns, which converts a blocking round trip into a
speculative one, and I5 keeps it honest - the answer still decides. It was filed as a latency
optimisation for single-machine builds and is worth more than that: on a fleet it is what stops a
condition serialising the whole plan. `core.Predictions` exists and is still unwired.

**The differential oracle, 2026-08-14.** The engine that ships and the one being built are asked
the same question, and their answers are compared. Five constructs so far - a command's output, an
argument, a condition, a loop, and quoting that reaches the shell - and they agree on all five.

This is the test plan's first milestone at its smallest, and it is a different kind of test from
everything else here. Every other check in this repository compares the native engine against
someone's idea of what it should do, and that someone is mostly me. This one compares it against
the implementation people are already using, which is the only definition of correct a replacement
is answerable to.

Artifacts rather than images, deliberately. An artifact is bytes: a difference is a difference and
needs no interpretation. Images carry timestamps and digests that legitimately differ between
engines, and comparing those needs the exclusions table - which is real work and should not be
smuggled in under a test that could be trusted without it.

Mutation-proven, and with a bug this engine actually had: making RUN use the value lexer instead of
the word lexer - which strips the quoting a shell needs, and shipped that way once - is caught by
the oracle immediately. The test costs about 17 seconds and needs `earth`, a docker daemon and
the sandbox, so it skips wherever any of those is missing.

**The image runs, 2026-08-14.** An Earthfile is interpreted, its steps run in a sandbox, their
layers are packed, an OCI layout is written, `skopeo` loads it into the local daemon, and
`docker run` prints what the build wrote. That is the whole path, checked by three tools that have
no stake in whether this engine is right.

It is worth being precise about why this test exists when skopeo already read the layout. Reading
proves the layout **parses**; running proves it **works**, and the gap between those is where the
interesting failures live - a missing executable bit, layers stacked in the wrong order, a diff id
that disagrees with the manifest. Each of those produces an image that inspects perfectly and will
not start.

The first run failed, and the failure was worth having: skopeo wanted a signature-trust policy file
that a developer machine has no reason to have. Not a defect in the image, and the fix is
`--insecure-policy` - the question here is whether the image runs, not who signed it. A test that
had reported "the image would not load" without the message underneath would have sent someone
looking for a bug in the manifest.

The image is loaded under a distinctive name and removed afterwards, so the test leaves nothing on
the machine that ran it.

**SAVE IMAGE writes an image, 2026-08-14.** The refusal added two iterations ago is now a written
image: an Earthfile says `SAVE IMAGE`, the steps run in a sandbox, their layers are packed, and an
OCI layout lands under the build cache. The build prints where, because an image written somewhere
nobody is told about has not really been produced.

A layout on disk rather than a load into a running daemon, which is what this engine can honestly
do today. The layout is the interchange format, so `docker load --input` and `skopeo copy oci:...`
take it from there.

The environment is **sorted** on the way into the config, and that is not tidiness. An image's
identity is the digest of its config, a Go map has no order, so an unsorted environment would make
the same build produce a different image every run - the defect this engine spent the day hunting
in its own key derivation, one layer further out.

The mapping from what an Earthfile declared to what the format needs is kept apart from the writing
and tested without a layer store, because they are different kinds of work: one is about what
`SAVE IMAGE` meant, the other about what the specification requires.

End to end, and checked by **skopeo** rather than by this engine's own reader: the image a real
sandboxed build wrote has the base layer plus the step that added to it, and an independent tool
agrees. The seam guard was updated in the same commit, which is what it is for - `Images` now says
`writeImages` rather than `checkImages: refused`.

**The OCI layout, 2026-08-14.** `image.WriteLayout` writes packed layers, a config and a manifest
as an OCI image layout - the interchange format rather than one option among several, since
`docker load`, `skopeo copy`, `crane push` and every registry client start there. The types come
from `image-spec`, so the structure is whatever the specification says rather than whatever this
engine happens to believe.

Two decisions are worth their comments. **Layers are uncompressed**, which makes a layer's digest
and its diff id the same value - removing a whole class of mismatch - and avoids gzip, whose header
carries a modification time: compressing would put a clock back into an image built to be
reproducible. And **the config has no `created` timestamp**, which is the one field the format
invites that would make two builds of one input produce different images.

The test that matters is not either of the ones checking structure. Those verify the layout with
the same types that wrote it, which proves self-consistency and nothing else. **`skopeo inspect`
reads it** - an independent implementation with no interest in what this engine believes, and the
only evidence that the format is right rather than merely internally agreed. It skips where skopeo
is not installed, so it costs nothing where it cannot run.

**Packing a layer, 2026-08-14.** `image.Pack` writes a directory as a tar and reports the SHA-256
digest and size an OCI descriptor has to state. The first piece of writing an image, and the piece
everything else rests on: a manifest is a list of layer digests, so nothing above this can be
correct until the bytes below it are settled.

**Byte-reproducible, and that is the requirement rather than a nicety.** An image's identity is the
digest of its layers, so a tar that varies between runs is an image that varies between runs - two
builds of one input producing two different images, and a registry storing both. Three things cause
it and all three are normalised: directory order (sorted byte-wise, because a listing promises no
order and a collation-aware sort would make a layer depend on the machine's language), modification
times, and ownership. Mutation-proven: leaving `ModTime` as the filesystem gave it fails the test.

The timestamp is `time.Unix(1, 0)` rather than the epoch itself, because some tools read a zero
time as "unset" and substitute the current one - putting the clock back into the archive by the
very mechanism meant to keep it out.

SHA-256 here and BLAKE3 everywhere else in the engine, which is the green paper's rule holding:
this digest is written into a manifest and read by registries, so the format dictates the hash.

Round-tripped through this package's own `Unpack` - the reader every pulled image already goes
through, so a tar it refuses is not a tar - and checked for the two things an image is not allowed
to lose: an executable bit, without which a container will not start, and a symlink, which
flattened into a copy quietly doubles a base image.

**The seam gets a guard, 2026-08-14.** Two bugs in two iterations were the same shape: a lower
layer declares an output and the layer above never reads it. The artifact a `FINALLY` saved, and
then `SAVE IMAGE` - planned, recorded on the `Plan`, and ignored by the only code that could act on
it, so a build reported success having produced no image.

Neither was visible to a unit test, and the reason generalises: **a unit test is written per
component, and a seam belongs to nobody**. Both components were correct. What was missing was
anything asserting they were connected.

So the seam has a guard, in the shape the key-coverage test already established. A field added to
`interp.Plan` and not listed in `consumedBy` fails, and listing it is a statement about what acts
on it; a listed name that no longer exists fails too, because a stale note misleads the next reader.
Mutation-proven: adding an unread field fails it. The compiler cannot notice an ignored field.

`SAVE IMAGE` is now a refusal naming the image and the line, rather than silence. A refusal rather
than a warning because a warning printed among a build's output is a success as far as anything
reading the exit code is concerned, and CI reads the exit code. Writing images is the feature this
asks for and does not provide - the honest position until it exists.

**TRY, proved where it counts (2026-08-14).** The simulated executor could not vouch for the part
that matters - whether a *failed* step's filesystem survives to be exported - because it returns
`Captured: true` because it was told to. The sandbox says it does: the real executor captures a
layer regardless of exit status, so the failed step's files are there.

Writing that test found the defect the unit tests could not. The CLI returned as soon as the
scheduler reported a failure, **before exporting anything**, so a build with a TRY failed correctly
and threw away the artifact the FINALLY had just declared - the one thing the construct exists to
keep. `core.ToleratedFailure` is a separate error type for exactly this reason: the caller has to
treat it differently, and the difference is the feature. Everything downstream has already run, so
the export happens and *then* the build fails.

That is the second time in two iterations that thinking about the whole path found something a
green unit test was hiding, and both were at a seam: one between the interpreter and the scheduler,
one between the scheduler and the CLI.

**TRY and FINALLY, 2026-08-14.** `ir.Op.Tolerate` says a non-zero exit is a result rather than the
end of the build. The step still failed and the build still fails - at the end, once everything
that had to run has run. Corpus: **361 to 368 targets planned**.

Both halves are load-bearing and each is wrong without the other. Stopping at the failure means
FINALLY never runs, which is the entire reason TRY exists; not failing afterwards means a red test
suite reports a green build, which is worse than either.

The failed step's **layer is kept**, and that is the feature rather than a detail. Every TRY in
this repository is `RUN test > report && false` followed by `SAVE ARTIFACT report`, and none of it
means anything if the filesystem the failed step left behind is discarded. FINALLY then stands on
it in the ordinary way, as the next step, because that is where the file it names was written. The
layer is kept and still never cached: a failed step is not published, tolerated or not.

`Tolerate` is in the key, and the reason is not the obvious one. The *command* is identical either
way, but the outcomes differ where it matters: a tolerated failure yields a filesystem later steps
use, an untolerated one yields nothing. Two requests with different results are different requests.

`CATCH` is refused rather than approximated. Its commands run *because* the try failed, so treating
them as ordinary steps would run them when nothing went wrong - the opposite of what was written.

**WAIT, and the ordering edge it needs, 2026-08-14.** `ir.Node.After` says a step must wait for
another without using its result. Neither existing edge could: an input stacks a layer and a source
puts one in the key, while what a WAIT block contains is usually a *side effect* - an image pushed,
a file written on this machine - with no layer to take. Expressing it as an input would stack a
filesystem nobody asked for. Corpus: **357 to 361 targets planned**.

It is deliberately **absent from the identity**. Ordering changes when work happens, not what it
produces, so two builds differing only in a WAIT do the same work and must share cache entries.
Keying on it would make a WAIT invalidate everything after it - a cache that punishes the one
construct people reach for when they need correctness.

The first implementation attached the edges to the block's own exit, which is wrong in a way worth
recording. A block containing only `BUILD +dep` produces no new node, so the edges landed on the
step *before* the block - making the image pull wait for a target that stands on that same image.
That is a cycle, not an ordering. The edges are now left pending and attached to whatever is built
next, which is what "everything after this block waits" actually means.

The existing sweeps validated the new construct without being touched: all 361 graphs still
schedule soundly, every step still runs exactly once and after what it stands on, and the second
build still hits. An ordering edge that dropped work or deadlocked would have shown up there rather
than in a WAIT test written by the person who had just written WAIT.

**The second build runs nothing, 2026-08-14.** 350 corpus graphs are now built twice through the
real scheduler and the real action cache - 2,265 steps - and the second build must execute only the
steps that are never cached. Determinism said the same input yields the same key; it said nothing
about whether that key is written down, found again, or trusted when it is. The second run opens a
*second* cache over the same directory, so the entries have to have survived being serialised and
read back by what is, as far as the cache is concerned, a stranger.

A step that re-runs here is a cache miss nobody would notice: the build still produces the right
answer, only slowly. That is the failure a build tool can least afford and least easily see.

The first version reported eight graphs re-running steps, and every one was correct behaviour -
`RUN --no-cache` and `LOCALLY`, which are *meant* to run again. The assertion now counts those
rather than skipping the graphs that contain them, so a file with four uncached steps still checks
its other two. That correction was verified against the Earthfiles rather than assumed:
`release/apt-repo/test` has six steps in `test-ubuntu` and exactly four are `RUN --no-cache`. A
test that goes green the moment it is loosened deserves that check, because the loosening is
indistinguishable from the fix.

Mutation-proven: pointing the second build at a fresh cache directory fails 341 graphs.

The sweeps have grown expensive enough to need managing: six of them walk the whole repository, and
under race instrumentation the interp package went from 40 seconds to 373. They now skip under
`-short`, so `go test -short -race ./engine/...` is 12 seconds and still covers every unit test,
while an ordinary pass runs the sweeps in full.

**Determinism, checked rather than asserted, 2026-08-14.** Every corpus target is now planned
three times and the whole traversal compared - node identities, chain keys, arguments, artifacts,
images. Go randomises map iteration deliberately, so anything that walks a map on the way to a
key produces a different one each run. The damage is not a failed build: it is a cache that never
hits, on a tool whose entire argument is that it does, appearing intermittently.

Both hashers are covered, and that was the point of extending it. `ir` and `core` hash overlapping
fields through separate code, so a map sorted in one and walked in the other is a bug the obvious
version of this test - comparing node identities - could not see. It is the **key** that decides
whether the cache hits.

The guard is mutation-proven in both: removing `sort.Strings` from `ir.Op`'s environment fails
seven comparisons, and removing it from the chain key fails seventeen. A test that has never failed
is a hypothesis.

The first attempt at the second mutation did not compile, so the run reported zero failures and
appeared to prove the opposite. That is the third time today a zero has meant "nothing ran" - after
the silent no-op edit and the `grep -c FAIL` on a package that would not build. **A count is only
evidence once something has been shown to run**; the mutation now prints `MUTANT_COMPILES` before
the count is believed.

**Invariants on the execution side, 2026-08-14.** Every sweep so far examined plans, and the RUN
flags proved a graph can be perfectly well formed and still run the wrong command. So all 356
corpus graphs - 2,265 steps - now go through the real scheduler with a simulated executor, checking
what a plan cannot show:

* every step ran, exactly once. A step quietly skipped is a build reporting success without doing
  the work;

* every step completed after everything it stands on. Started early, it reads a filesystem that
  does not exist yet;

* no base stack repeats a layer. overlayfs refuses a repeated lowerdir with ELOOP, which names
  nothing about the cause and appears only on a real mount - on someone else's machine, in a build
  that passed here.

Simulated rather than sandboxed so it covers every graph the corpus produces instead of the handful
a VM has time for.

It found the gap the previous iteration created. Now that `FROM --platform` reaches nodes, those
nodes could not be scheduled at all: **the local worker declared no platform**, and the affinity
rule refuses a node whose platform a worker does not declare. So six targets planned correctly and
then failed with "no eligible worker" on a machine that runs exactly that platform. The rule is
right; the worker was lying about itself. It now declares the platform it runs, and a node asking
for one this machine cannot run still fails - which is the point, and is exactly what the earlier
entry on `BUILD --platform` promised.

Two mistakes in the sweep itself are worth recording, because both are about testing the system
that exists rather than the one imagined. The simulated executor was not safe for concurrent use,
and the scheduler really does run steps in parallel - Go's map checks said so on the first run. And
it borrowed a helper from a **darwin-only** test file, so it compiled here and failed
`GOOS=linux go vet`, which is why that check is in the loop rather than left to CI.

**Two more invariants, and a fifth instance, 2026-08-14.** The dash rule worked, so the same
treatment was given to two more properties every plan must have:

* **every image reference must parse as a reference**, checked with the registry code that will
  parse it later - asked while there is still a line number to blame. It subsumes several rules at
  once: an unexpanded `$TAG`, a quote the lexer left behind and a flag read as a name all fail it,
  and none needs a rule of its own.

* **every artifact must be produced by a step that is in the graph**. Otherwise the failure arrives
  at export as "the step producing X did not run", which describes a symptom and names no line.
  This one passed on all 324 artifacts, which is worth having anyway: it is now false rather than
  untested.

The first found the fifth instance of the family. `SAVE IMAGE app:$(cat version)` was producing a
reference containing the text `$(cat version)`, in seven targets. A `$(...)` in a value the
*engine* consumes has no shell to expand it, so the engine must - and `expandCommands`, built for
LET and FOR two iterations earlier, already did exactly that.

RUN is excluded by the same reasoning rather than despite it: a command is handed to a shell, whose
job this is. Evaluating it here would run it once at plan time and bake the answer in, so a step
reading the clock or listing a directory it is about to change would see the wrong moment.

**Corpus 363 to 357**, because those seven now refuse where they previously produced a reference no
registry would accept. That is the fourth time today the number has fallen for the right reason.

**One invariant over the whole corpus, 2026-08-14.** Three hand-written sweeps had each found the
bug they were shaped to find and missed the next one, so the question was asked structurally
instead: **no value the engine derived from an Earthfile may begin with a dash**. `RUN ls --color`
is ordinary and unaffected; a *command* that begins with `--`, or an artifact path, an image
reference or a working directory that does, is nonsense only an unparsed flag produces. Run over
every target that plans - 362 of them across 192 Earthfiles - rather than over a table someone
thought of.

It found a fourth instance in seconds, in the commonest command there is.
`FROM --platform=linux/amd64 alpine` was producing an image reference of `--platform=linux/amd64`,
because the flags were parsed and then the *unparsed* first argument was used as the image. The
platform was dropped on the same line, so a multi-platform build silently became a native one.
Those targets counted as planned throughout: they planned a pull of an image no registry has.

`fromTarget` now returns a `fromSpec` rather than five positional results. That is the actual
lesson rather than a tidy-up: the image and the target reference are alternatives, and a row of
strings whose meaning depends on which are empty is a shape that invites exactly the mistake made
here - taking one field from the parsed arguments and another from the raw ones.

**Asked of every command at once, 2026-08-14.** Twice is a pattern, so instead of finding the
third instance by accident there is now one test that asks every command with an options type
whether it reads its own flags. The rule it checks is that a flag is **either honoured or refused
by name, and never quietly becomes part of a value** - a refusal naming the flag is a fine answer,
a complaint about a path called `--if-exists` is not.

It found the third: `SAVE ARTIFACT --if-exists /out /dst` was saving an artifact whose path was
`--if-exists` and whose destination was `/out`. The wrong file, exported to the wrong place,
reported as success.

The test also had to be fixed before it could find that, and the reason generalises. It checked
only `Op.Args`, and SAVE ARTIFACT's flags never reach an operation - they reach an `Artifact`. A
sweep that looks in one place finds bugs in one place; it now checks artifact paths and
destinations too.

`--if-exists` is honoured rather than recorded, because a flag that is stored and ignored is the
failure this whole sweep is about. It is asked of the materialised filesystem before exporting,
not inferred from an export failing: "the file was not there" and "the export went wrong" must not
be the same answer, or a broken export becomes a silently skipped artifact.

**The same defect in IF, and COMMAND, 2026-08-14.** Once RUN's flags were parsed the corpus
surfaced `IF --no-cache ! aws sts ...`, which is the identical bug one command along: the flag was
read as the first word of the condition, so a decidable condition looked like one needing a
process. IF's flags are parsed now, with the same division - the ones that change what the
condition may *do* are refused, `--no-cache` is accepted and dropped. Dropped rather than recorded,
because a condition decided when the graph is built has no step whose caching it could govern, and
when the condition has to be run instead, the probe that runs it is a fresh step every time
already.

`COMMAND` is what `FUNCTION` was called before it was renamed. The parser knows both and keeps them
apart, which is right - a diagnostic should quote the word the author wrote - but the interpreter
knew only the newer one, so an Earthfile using the older spelling was refused as an unsupported
construct. One line, and the `WITH` group fell from 49 causes to 47 as the targets behind it got
further.

The `RUN --no-cache` end-to-end test is green: 3.86s for two builds against one cache. It had
appeared to hang for ten minutes, which was neither the engine nor the test - a stray `cat >> file`
with no input in the same command line sat reading stdin. `pgrep -f "go test"` then reported the
run as still going, because the pattern matched its own command line.

**RUN's flags were being executed, 2026-08-14.** Sweeping the refused flags for more hints found
something worse than a refusal: RUN's options were never parsed at all, so
`RUN --no-cache fetch` became `sh -c "--no-cache fetch"` - a command nobody wrote, which fails
saying `--no-cache` is not a program. A hundred and eleven RUN lines in this repository carry a
flag. The corpus could not see it, because the corpus measures planning and this defect is in what
gets run: every one of those targets planned perfectly and would have executed nonsense.

`--no-cache` is now honoured rather than stripped, which is a correctness matter and not a
preference. The author is declaring the step is *not* a function of its inputs - it fetches
something, or reads the clock - so serving it from cache hands back a stale result and reports
success. It joins the host-step rule in the scheduler: the two arrive by different routes and mean
the same thing, that there is no honest key for the result, so it is neither looked up nor
published. It is also in the key, because the same command with and without it are different
requests.

Adding the field to `ir.Op` was caught by the reflective key-coverage guard before any test of
mine ran - "changing Op.NoCache does not change the chain key" - which is the guard doing exactly
the job it was written for.

**The corpus fell from 391 to 363 targets, and again that is the fix working.** `--privileged`,
`--secret`, `--mount`, `--interactive` and `--entrypoint` change what a step may *do*, so they are
refused rather than stripped: a step that quietly loses its secret does not fail, it produces the
wrong thing. Those 28 targets were previously "planning" a command line with a flag embedded in it.

**A hint is not a feature to refuse, 2026-08-13.** `SAVE IMAGE --cache-from` and
`COPY --if-exists` were both refused, and they needed opposite answers. Corpus: **373 to 391
targets planned**.

`--cache-from` names somewhere to *look* for cache, so a build that heeds it and a build that
ignores it produce the same image. That is I5 - a hint may not change results - and it is exactly
what makes ignoring it safe. It is accepted and dropped, and deliberately never reaches the graph:
two builds differing only in where they were told to look must share cache entries, which they
cannot do if the hint is part of what is keyed. Refusing a flag that cannot affect the output turns
a working Earthfile away for nothing, and twenty-one targets in this repository were turned away by
that one.

`COPY --if-exists` is the opposite: it changes what is copied, so it had to be built rather than
waved through. A pattern that matches nothing is dropped under the same rule, because both forms
say "copy this if the build produced it" and refusing one while allowing the other is a distinction
the Earthfile never drew.

The test that got this wrong is worth recording. `go test -run X 2>&1 | grep -c FAIL` returned
**zero because the package did not compile** - the same shape as the silent no-op edit earlier
today. A count of failures is only evidence when something ran.

**One seam, two questions: `$(...)` (2026-08-13).** `interp.Conditions` became `interp.Commands`
and returns a `Result` carrying both an exit status and the output. A condition reads the status; a
`$(...)` reads the output. Two seams for one mechanism - running a command on the filesystem the
recipe has built up to that line - would have been a second thing to keep correct, and they would
have drifted.

That unblocked two refusals at once: `FOR d IN $(ls dirs)` and `LET tag = $(cat version)`. Proved
end to end in a sandbox, and it is worth naming what is new about it: a loop over command output is
the first construct whose **graph shape** comes from running something. A condition chooses between
branches that were both writable in advance; the number of iterations here is not known until a
command has run.

Three details, each with a test:

* the trailing newline is trimmed, because `LET tag = $(cat version)` means the version and not a
  version with a newline that then appears in an image tag;

* a command that exits non-zero is an error rather than a value - looping over an error message
  would build one absurd iteration per word and report success;

* brackets are counted rather than matched to the first `)`, so `$(cat $(ls -1 | head -1))` runs
  the whole command instead of half of one.

`--engine=buildkit` remains the answer when no runner is supplied, which is still the plan-only
path: producing a graph must not run commands in a sandbox behind the caller's back.

**FOR, 2026-08-13.** `FOR x IN a b c` unrolls into the graph. Corpus: **368 to 373 targets
planned**, and FOR leaves the blocker list. Unrolled rather than represented, for the reason a
condition is decided rather than deferred: a loop in the graph is a graph whose shape depends on
something that has not run yet. Unrolling also makes each iteration a step in its own right, so one
changed item invalidates one iteration instead of the whole loop.

The loop variable is *restored* after END rather than deleted, because the name may have been an
ARG before the loop borrowed it. `FOR m IN $(find . -name go.mod)` is refused by name: it needs a
command run in the build environment, which is the condition problem again and gets the same answer
until the evaluator returns output as well as an exit status. All four uses in this repository are
that shape, so the corpus movement comes from targets that reach a FOR rather than from the four
that write one.

**CACHE is a specification question, not a missing feature (2026-08-13).** Examined and deliberately
not started, because starting it badly is worse than the refusal. Two things stand in the way, and
only one is code:

* there is **no mount plumbing at all** - not in the guest protocol, not in either sandbox backend.
  A cache mount is a directory that outlives the step, so it needs one.

* a cache mount is state a step reads that **its key cannot bound**, which is exactly what I3 asks
  of a key and what I7 answers for a host step. The consistent reading of this engine's own rules
  is that a step carrying a cache mount is not soundly cacheable: the mount makes it fast, and the
  action cache cannot honestly claim its result. That diverges from BuildKit, which caches such
  steps, so it is a decision to take deliberately rather than discover.

`--persist` sharpens it: with it the mount's contents enter the image, without it they do not, so
the two forms differ in whether the unbounded state reaches the output at all.

**Auditing the discount, 2026-08-13.** Discounting 74 refusals as "the input is invalid" is only
honest if they are right, so each was checked against the Earthfile it came from. Most are:

* the twelve cycles are one fixture that exists to recurse; the parse errors are
  `duplicate-target-names` and `reserved-target-names`; the missing base images are
  `first-command` and `no-project`. Fixtures whose purpose is to be rejected.

* every `ARG --required X, and no value was given` is right by construction - the author marked it
  required and the corpus supplies nothing.

* `COPY output/repo` and `COPY data` name files a *previous* step or a CI job produces, so they
  are genuinely absent from a clean checkout. One of them is a fixture called
  `second-copy-should-fail`.

Two were not right:

* **`IMPORT github.com/org/repo:main` was named `repo:main`.** The alias defaults to the last path
  element, and the revision was being kept as part of it, so `repo+target` reported that `repo` had
  never been imported. The expected behaviour was written in a comment beside the line that broke
  on it, in this repository's own `examples/import/Earthfile`.

* **"has no base recipe" was a false statement.** The root Earthfile does have one - four `ARG`
  lines - it just sets no image. The refusal is right and the sentence was not, which is the same
  defect as the glob reported as a missing file: a reader sent to look for something that is
  already there.

That is two bugs from a category defined as "the engine is correct here", which is the argument
for listing those causes rather than subtracting them.

**Blocked and refused are different numbers, 2026-08-13.** The corpus now separates constructs the
engine cannot do yet from Earthfiles it is refusing because they are wrong, and the test signal is
the message itself: a refusal that offers `--engine=buildkit` is a limitation, and one that does
not is the engine asserting the input is invalid - there is nothing to switch to that would make
invalid input valid. Adding the two together had been overstating the work left by a fifth.

```text
367 targets planned, across 192 Earthfiles
552 blocked by 90 unimplemented constructs; 84 refused as invalid input, from 74 causes
```

The invalid-input causes are **listed, not dropped**. A refusal that says the Earthfile is wrong is
only worth discounting if it is right, and a pattern that could not be `stat`ed was reported as a
file missing from the build context until this afternoon - a bug wearing the costume of a correct
refusal. Keeping them printed is what makes the discount honest rather than a way of not counting
failures.

Splitting the table found a gap made three hours earlier: `IMPORT github.com/org/repo AS lib` was
still refused at the line declaring the alias, though the machinery to fetch a repository had just
landed. An import is only a name for a reference, so it now means exactly what writing the
reference out in full means - including being refused the same way when there is no fetcher. It is
recorded rather than resolved at the IMPORT line, because resolving there would clone a repository
for an alias the file might never use.

**The corpus report was ranking the wrong thing, 2026-08-13.** A refusal reads `FROM +a (x:1):
BUILD +b (y:2): IF at z:3 needs to run ...`, and the report grouped it by the *first* construct.
That names the line which referred to the problem rather than the problem, so `BUILD` and `FROM`
sat at the top of the table with fifty targets between them and nothing to fix. Two iterations of
work were chosen off that table. It now groups by the innermost message, and the table is a work
list rather than a census of how targets reach trouble.

Reading the corrected table found two defects and one thing the corpus was counting wrongly:

* **Twelve "cycles" are `tests/cli/testdata/infinite-recursion/Earthfile`**, a fixture whose whole
  purpose is to recurse. Several other refusals are the same: `duplicate-target-names`,
  `reserved-target-names`, `first-command`. The engine is right about all of them, and a corpus
  that counts its own correctness as a blocker overstates the work left.

* **`ARG --global IMAGE_REGISTRY=...` declared an argument called `--global`.** `declare` never
  stripped the flags, so the flag became the name and the real argument was never declared. It
  surfaced a hundred lines away as an `IF` complaining that an argument was not declared when the
  declaration was sitting right there. Now read with the repository's own `cmdopts.Arg` and
  `flagutil.ParseArgsCleaned` rather than by hand.

* **`ARG --required X` with no value is refused**, naming X and how to supply it.

**The corpus fell from 390 to 367 targets, and that is the fix working.** Those 23 targets declare
an argument the author marked required, and were planning with it silently empty - the same class
of failure this engine exists to refuse. A number that goes down because a false success was
removed is worth more than the number it replaced. Distinct causes are not comparable across this
change, because the grouping changed underneath them.

**COPY patterns, 2026-08-13.** `COPY scripts/*.sh /dst/` expands to the files it names. Corpus:
**382 to 390 targets planned**, distinct causes **167 to 161**, and the "missing context file"
group from **23 causes / 45 targets to 10 / 10**.

The bug was worth more than the feature. A pattern was passed to `os.Stat`, which cannot stat a
`*`, and the failure was reported as "is not in the build context" - a refusal of valid input
*and* a misleading account of it, because the files were there and it was the pattern that could
not be looked up. Fifty COPY lines in this repository use one. A diagnostic that names the wrong
cause is worse than one that says nothing: it sends the reader to look at their files.

Expansion happens at graph construction, for the same reason the digest does: what a COPY reads
has to be in the graph before the key is computed. Expanding at execution would key the build on
the *pattern*, so adding a file the pattern matches would not change the key and the build would
hit an entry that predates the file. The matches are sorted, because a directory listing is not
ordered and the order reaches the key - two machines expanding one pattern differently would key
the same build two ways and neither would ever hit the other's cache.

It also uncovered a quieter one. `filepath.Clean("/" + "../a.txt")` is `/a.txt`, so joining it to
the context root produced a path *inside* the context: `COPY ../a.txt` copied the wrong file
instead of refusing. Only the glob case failed loudly, and only because `*` cannot be stat'd. The
containment test is now on the source as written, before normalisation - normalising first destroys
the evidence the check needs.

**The same question asked of everything else, 2026-08-13.** One finding of a class means the class
is worth sweeping, so every place an Earthfile's text becomes a host path was re-read adversarially.
`COPY` was already contained. Two things were not:

* **`SAVE ARTIFACT /x AS LOCAL ../../etc/cron.d/evil` wrote there.** An absolute destination was
  used verbatim and a relative one was joined to the project directory without a containment
  check, so an Earthfile could write anywhere on the machine running it - which for a *fetched*
  Earthfile means a repository choosing where to write on someone else's laptop. Refused at plan time and
  again at the export, which is the layer that does the writing.

* **A fetched Earthfile could climb out of its own checkout.** `FROM ../../../../..+t` is ordinary
  in an Earthfile on this machine and the corpus is full of it; in one that arrived from elsewhere
  it walks out of the build cache and lets a remote repository name any Earthfile on the host and
  have it built. The rule that fixes it is about **provenance, not about the path**: a unit that
  came from a checkout is confined to it, and everything it loads inherits that confinement. A path
  rule could not have expressed this, because the same path is fine in one file and an attack in
  another.

The confinement check first refused every legitimate reference on this Mac, because `load` resolved
symlinks and the new root did not - `/var/...` against `/private/var/...`. Two ways of turning a
directory into a comparable string is one too many; there is now a single `realDir`.

Corpus, measured with the referenced repository checked out beside this one: distinct causes
**216 to 167**. Targets planned did **not** move from 382, and that is the honest result rather
than a disappointing one - the chains that one reference was gating now reach a *second* remote
(`EarthBuild/test-remote`) that is not checked out on this machine. One line was hiding another.
The blast-radius figure that made this look like a 300-target win was a count of targets *affected
by* the cause, never a count of targets that would plan once it was fixed, and the two are only the
same when nothing is behind it.

**Branch prediction for the rest (Giles, 2026-08-13).** For the conditions that must run, the
engine can predict the branch from that site's history and speculate on it, so work that would
otherwise wait for the condition starts immediately.

The safety argument is the one already in the specification: a prediction is a **hint**, and I5
requires hints not to change results. The branch a build takes is whatever evaluating the
condition yields; the predictor only decides what to speculate on. A misprediction therefore costs
the speculated work and nothing else - the same shape as a cache miss, which is the property this
engine keeps arriving back at.

`core.Predictions` implements it and is tested on the load-bearing property: whatever the predictor
believes, the condition's own result stands. A site seen fewer than twice, or one that alternates,
is reported as unpredictable rather than guessed - speculating on no evidence spends parallelism on
a coin toss, and that cost lands on builds with no history, which are the ones a new user runs.

Runtime conditions themselves are not built yet, so nothing consults the predictor. Written now
because the *rule* it must obey is easier to get right before there is a caller arguing for
exceptions to it.

**DO and FUNCTION, 2026-08-13.** The corpus's largest gap - 173 occurrences - and the one that
moved it most: **162 to 210 targets planned**.

`DO` inlines a function into the caller's chain, which is the distinction from `BUILD`: a function
is a way of writing the same steps in one place, not a way of running a different build. So its
recipe is evaluated with the caller's current node as its base.

The caller's arguments deliberately do **not** leak in. A function is a unit with its own
interface; one that silently saw its caller's variables would do different things depending on
where it was called from, and moving a call would change what it does. The working directory *is*
inherited, because the function runs in the caller's filesystem.

Two things real input corrected, neither of which appeared in the tests written first:

* `ARG` inside a function **overwrote** the value the call passed with its own default, so
  `DO +GREET --name=world` ran a function that had forgotten its argument. Declared values and
  supplied values are now separate: a declaration cannot overwrite what arrived from outside.

* Arguments come as `--name=value` **and** `--name value`, and both are ordinary. Handling only
  the first refused a perfectly good line with a message telling the author to write what they had
  already written.

**ARG, 2026-08-13.** Arguments expand where they are used, `-build-arg NAME=VALUE` overrides a
default, and a changed value re-runs exactly the steps that used it - measured: one step, with FROM
still hitting.

**Only declared arguments are substituted**, and that is the decision worth recording. Docker's
rule - expand every `$name` and leave undefined ones empty - turns a typo into a command that runs
and does the wrong thing, and it mangles ordinary shell: `for i in 1 2 3; do echo $i; done` is a
perfectly good RUN whose `$i` belongs to the shell, not to us. Anything the Earthfile has not
declared is passed through exactly as written.

Two more rules that keep a file meaning one thing:

* an ARG applies to the commands **after** it, so reading top to bottom is reading correctly. A
  declaration treated as retroactive would let the order of a file change its meaning invisibly;

* an ARG declared and never used changes **nothing** - not the graph, not a key. Otherwise adding
  an argument for one target invalidates the whole file, and people learn not to add arguments.

Expansion happens before the node exists, so a value is part of the operation and therefore part
of its key. That is the same false-hit risk as an edited COPY source arriving by a different route,
and it is tested as a key comparison rather than a graph comparison - the distinction that cost a
real build earlier today.

**Output streams as it happens, 2026-08-13.** A step's output reaches the terminal while the step
is running, each line prefixed with the Earthfile line that produced it.

The prefix is the point, not decoration. Steps run concurrently, so their output interleaves; an
unattributed line is worse than none, because a user reads one step's error under another step's
heading and debugs the wrong command. Chunks are buffered to line boundaries before printing,
since a write that splits mid-line would otherwise put a prefix in the middle of a sentence.

A streaming frame is explicitly *not* a reply - the request stays in flight and the caller keeps
waiting - and is marked by a flag rather than by "the chunk is non-empty", because a step
legitimately prints a blank line. Streaming is requested by the host, so a guest does not pay for
framing nobody is listening to, and the complete output is still returned at the end: a failing
step's message is what its error is made of.

Silence is the specific failure being removed. A build that says nothing for four minutes and then
prints everything is indistinguishable from one that has hung, which is why `--progress=plain`
exists in every other build tool.

**The parallelism now reaches the executor, 2026-08-13.** The two independent three-second steps
that took **7.2s** take **4.0s**. The guest protocol carries request ids and the client
demultiplexes replies, so a slow materialise no longer holds up an exec queued behind it.

The property that makes this worth doing rather than dangerous is tested directly: sixty-four
concurrent requests, replies deliberately arriving out of order, each asserting it got *its own*
answer. A client matching replies by arrival would hand one step another step's filesystem - a
wrong build that reports success. A broken connection now fails everything outstanding rather than
leaving callers waiting for a reply that can never come.

**Two protocol defects, and the second is the instructive one.**

Bumping `Version` was not optional: version 2's frames still *parse* under version 1, so an old
guest accepts them and answers without an id. The wire's shape was compatible and its semantics
were not, which is precisely the case a version field exists for.

Then the version check could not run. The handshake was going through the multiplexed path, so the
host waited forever for a reply it could never match - and a stale guest **hung the build** instead
of being refused. Negotiation that depends on the newest feature cannot negotiate. The handshake is
now exchanged synchronously, before the demultiplexer starts, and stays expressible in the oldest
dialect the protocol has spoken. A guest one version behind is refused in a second, naming both
versions and how to fix it.

**Cross-target references, 2026-08-13.** `FROM +other` continues from another target's
filesystem; `BUILD +other` makes it run without inheriting it. A shared dependency named by three
targets is built once - memoised during resolution, and collapsed again by node identity, so it
would survive even a naive expansion.

`BUILD` is modelled as a **second root**, not an operation. An operation taking the dependency as
an input would stack that target's layers into this one's base, which is what `FROM` means; the
difference is a target quietly inheriting a filesystem it never asked for. `ir.Graph.Also` says
"run these too" and nothing else.

Cycles are refused with the loop named - `+a -> +b -> +c -> +a` - and the error is *typed*, so the
frames above leave it alone: a cycle is the same fact at every level of the recursion, and
wrapping it once per hop buries the loop under the path that found it.

An unexpected confirmation while measuring: two targets whose steps are byte-identical over the
same base collapse to **one** step, because identity is content. It was noticed only because it
spoiled a benchmark that assumed two.

**Measured, and not good: parallelism does not reach the executor.** Two independent three-second
steps take 7.2s, not 4s. The scheduler evaluates them concurrently and they all queue on the guest
connection, whose client holds a mutex across each whole request/response exchange. A synchronous
wire behind a mutex cannot express concurrency; it needs request ids and a demultiplexer, or a
connection per concurrent step. Marked `[GAP]` at the mutex itself, where the next person to look
will be standing.

**Steps run in parallel, 2026-08-13.** Independent steps now overlap, bounded by `NumCPU`. A fan
of four went from 485ms to 250ms in the scheduler's own test. This is not an optimisation: the
prototype this engine replaces had a correct scheduler and a **serial build loop**, so it produced
the right answer at the speed of one core, and wall-clock is what a build tool is for.

Determinism is preserved by construction, not by luck, and making it so surfaced two defects:

* **Placement depended on observed load**, which was deterministic only while the build was
  serial. With steps finishing in whatever order they finish, the *schedule* varied run to run -
  and §4.7.3 requires it to be byte-identical from the same inputs and inventory. Placement now
  happens in a pre-pass over the deterministic topological order, simulating load rather than
  measuring it: still load-aware, and a pure function of the graph.

* **`core.Executor.Run` is now called concurrently**, which is a change to the port's contract
  rather than an implementation detail. The simulator had an unguarded slice append, and the race
  detector found it the instant the scheduler stopped being serial. The obligation is documented
  on the interface, where an implementer will see it.

Two further properties are tested rather than assumed, because concurrency leaks into *reporting*
even when it does not leak into results: the build record is sorted by graph position, so a
parallel build and a serial one produce identical records; and when several steps fail at once the
one **earliest in the Earthfile** is reported, not the first to lose the race. A build that blames
a different command each time is a build nobody can act on.

The whole engine is race-clean under `go test -race`.

**The key is guarded by reflection, 2026-08-13.** A test walks every field of `ir.Op`,
`ir.Platform` and `core.Observation`, varies it, and fails if the key does not change - naming the
field and the consequence. Adding a field without keying on it now breaks the build.

This exists because the alternative is a comment asking people to remember, and the false hit
recorded below is what remembering achieves. The guard was checked by adding an unkeyed field and
confirming it fails: a guard that cannot fail is the same defect as the vacuous tests it replaced.

One field is deliberately excluded and says so in place: `Observation.Incomplete` is a statement
about the observation's *quality*, not its content, and the scheduler refuses to derive Κ₂ at all
when it is set. Keying on it would make a complete and an incomplete observation of the same reads
into different steps.

**The cache persists, 2026-08-13.** `earth-native build` twice: the second is all L1 hits. Entries
live one-per-file under `~/.cache/earthbuild`, which is deliberately the least clever arrangement
available - concurrent builds need no coordination beyond what the filesystem provides, a damaged
entry costs one step rather than the cache, and eviction is `rm`. A corrupt or unreadable entry is
a **miss**, never an error: an action-cache entry is an unverifiable claim (§5.2), so a damaged
cache costs time and nothing else.

**A false hit reached a real build, and it is worth recording exactly how.** `Op.Content` was added
to node *identity* so the graph changed when a copied file was edited. It was not added to the
*key*. Identity and key are derived by different functions over the same operation, and adding a
field to one is precisely as wrong as adding it to neither: editing a source file produced four L1
hits and wrote the previous output. Then a second layer of the same fault - a local context is
deliberately absent from the base stack (a context is a source, not a base layer), so folding
content into the key was still not enough until unstacked inputs were folded in as well.

Both are now covered by tests that assert the *key*, not the graph. The lesson is structural: a
step's key must cover every input, including the ones it reads without standing on.

**It runs from a terminal, 2026-08-13.** `earth-native build` in a directory with an Earthfile:
pulls, copies the context in, runs the commands, writes the artifact. Three seconds for a first
build of a small project. The front end is a library (`engine/cli`) with a twenty-line `main`, so
the whole path stays testable without a process boundary, and `--dry-run` resolves everything that
can fail for reasons *in the Earthfile* - parse, target, context digests, capability refusals -
without needing a sandbox at all.

Three defects that only a real invocation could surface, all of the same family - **the tests ran
in the repository, and a user does not**:

* **A relative build context refused every file in it.** `--dir .` is the ordinary invocation, and
  a path joined onto `.` does not have `.` as a textual prefix. Same defect as the unpacker
  refusing everything on macOS: comparing a joined path against an unnormalised root. Normalise
  both ends or neither comparison means anything.

* **The sandbox built its own agent with `go build`, in the user's working directory.** That has
  no `go.mod`, so the first real run failed with a module resolution error from a build tool the
  user did not know they were invoking. A shipped binary cannot compile itself; `earth-guestd` is
  now *found* - beside the executable, or at `$EARTH_GUESTD` - and never built at run time.
  Providing it became the tests' job, which is the correct division.

* **The default platform was the host's.** Both backends run Linux - a VM on macOS, this kernel on
  Linux - so `runtime.GOOS` asked Docker Hub for a darwin image. The diagnostic was already good
  enough to diagnose it in one line, listing what the image does provide.

**[GAP]** the action cache lives for one process, so nothing is cached between invocations - which
is most of what a build cache is for. Marked in the code rather than left to be discovered by
someone wondering why their second build was slow.

**COPY executes, 2026-08-13.** A file on the developer's disk is readable by a command in the
sandbox, at the path the Earthfile asked for **and nowhere else** - the test asserts both, since
the second is the easier half to get wrong.

Two rules came out of making it run, and each is a place the obvious implementation is wrong:

* **A local context is a source, not a base layer.** Stacking it would merge the host's files into
  the image at the paths they occupy on the host, so `COPY src/main.go /app/` would also produce
  `/src/main.go`. The destination is the point; the source location is an accident of someone's
  directory layout. The scheduler therefore excludes `OpLocal` inputs from the base stack, and the
  guest reads the layer out of the store instead.

* **A repeated layer collapses.** Two steps producing identical output produce the same layer -
  deduplication working as intended - and the common case is two steps that write nothing, which
  both yield the empty layer. overlayfs refuses a repeated lowerdir with ELOOP, so a stack naming
  one twice cannot be mounted. Dropping the earlier occurrence is safe precisely because the
  layers are identical, and it shortens stacks, which is depth Φ does not have to flatten later.

**COPY and the build context, 2026-08-13.** A COPY resolves its source at *graph construction*,
not at execution, and the resulting node's identity covers the bytes it names. That ordering is
forced: a cache key is derived from the graph, so anything the result depends on must be in the
graph before the key exists. Resolving later would mean keying on a path and hitting on stale
content - editing a source file would leave every key matching and the build would reproduce the
previous binary. It is the most damaging false hit a build tool can have, because it looks like a
fast build.

Two digests, two jobs, and this is where having both pays:

* the context is keyed on **ℓ_con**, which excludes mtimes. Two checkouts of one commit differ in
  every timestamp, so keying on ℓ_id would mean a fresh clone never hits and CI rebuilds the world
  each run. It is the same reason git records content and not timestamps.

* timestamps still reach the *image*, because COPY writes files with them. They simply do not
  decide whether the copy has to happen again.

A COPY naming something absent is refused at parse time, saying what it looked for and where it
looked, rather than failing halfway through a build.

**Deduplication: once, by content.** A layer is named by ℋ over what it holds, so two steps
producing identical output converge on one directory and the second commit is a no-op. Many cache
keys - Κ₁ and Κ₂ for one step, or several steps that happen to agree - point at one stored layer.

Its limit, measured rather than assumed: **content differing only in mtime is a different layer.**
ℓ_id includes timestamps because a layer must restore faithfully (I8), so two builds producing
byte-identical files a nanosecond apart store both copies. ℓ_con (§3.3a) is what would detect it.

**[GAP]** deduplicating on ℓ_con needs a second index and a rule for which timestamps win. Not
built. There is also no *file*-level sharing: two layers differing in one file each store a full
copy of everything they have in common, as OCI does. Reflinks or a per-file CAS would fix that and
neither is in the plan yet.

**SAVE ARTIFACT works, 2026-08-13.** A file made inside the sandbox reaches the user's disk. The
export takes two hops and both are forced: the guest copies into the store they share, because the
host cannot read the sandbox's filesystem; the host then copies where the user asked, because the
store is the engine's and not somewhere a user's `dist/` should live.

**The cache hits, 2026-08-13.** A second build of an unchanged Earthfile executes nothing - all
L1 hits, against a real layer store on disk rather than a fake that claimed to hold everything.
That is the claim the whole design rests on, and until now it had only been tested against
simulated layers.

Two properties came with it, and both are about the cache failing safely:

* **A missing layer costs time, not correctness.** An action-cache entry is a claim; the claim is
  usable only if the layer it names is present. Evicting a layer - a GC, a partial copy, a
  truncated transfer - makes the step re-execute. Trusting the entry anyway would hand the next
  step a base that does not exist.

* **An empty layer is a layer.** A step that writes nothing produces an empty delta, and that is a
  perfectly good result to cache. Treating emptiness as a partial commit - which the first version
  of the store did - made every such step miss forever. Partial commits are prevented by
  committing under a temporary name and renaming into place, so a layer is either wholly present
  or absent; a crash mid-copy must not leave something that looks complete.

**Named gap:** `LayerStore.Has` checks presence, not integrity. Within a trust domain the store is
written only by this engine (A5), and rehashing every base on every hit would put a full capture
on the hot path. `LayerStore.Verify` is defined for the boundary that needs it - a layer arriving
from a fleet peer or a shared cache is unauthenticated data until it passes (§5.3) - but nothing
calls it yet, because there is no import path to call it from.

**An Earthfile builds, 2026-08-13.** Text through to processes that ran: parse, IR, schedule,
pull, unpack, VM, chroot, capture, commit. The only thing between this and `earth build` is the
command-line front end. The interpreter reuses `internal/earthfile`, so there is no second parser.

Three defects surfaced in the joining up, and all three were invisible to the simulator:

* **Every base stack contained each layer twice.** An input's stack already ends with that input's
  own layer, and the scheduler appended it again. overlayfs refuses a repeated lowerdir with
  ELOOP - "too many levels of symbolic links" - which names nothing about the cause. The
  simulator accepts duplicates happily, so this survived every test until a real mount rejected
  it. Stack depth was also growing at twice the true rate, which would have hit the 480-layer
  flattening limit at half the intended length.

* **A step's output was digested and then deleted.** Capture read the *merged* view rather than
  the upper delta, and nothing persisted it, so the layer a cache entry named ceased to exist on
  release. `Handle.Delta()` now distinguishes what a step *wrote* from what it *saw* - which is
  the layer model itself: digesting the merged view would make a one-line change over a 200 MB
  base produce a 200 MB layer sharing nothing with its predecessor.

* **`Run` replaced the caller's build record.** Every caller holding a pointer to the record it
  passed in read an empty one, which is indistinguishable from a build that did nothing.

A fourth was avoided rather than fixed: committing a delta by renaming it is wrong while the
overlay is still mounted, because the upper directory is moved out from under the live mount.
The copy is slower and correct.

**`FROM alpine:3.22` + `RUN` works end to end, 2026-08-13.** Registry pull, bearer-token auth,
manifest-index platform selection, SHA-256 verification, unpack, VM, chroot, capture - all real,
in 3.2 seconds. This is the shape of M1, though not yet its scope: there is no Earthfile parser in
front of it, so the graph is built by hand.

**Image pulling and unpacking landed**, which is what `FROM` needs. Two properties are load-bearing and both
are tested:

* **Everything a registry serves is untrusted.** A tar entry naming `../../etc/passwd`, an
  absolute path, or a write through a symlink pointing out of the layer is *refused*, naming the
  entry - not sanitised. Silently rewriting a hostile path produces a layer that does not match
  its digest, which is a different lie from the one being told.

* **Whiteouts are translated, not written.** OCI marks a deletion with a `.wh.<name>` file;
  overlayfs uses a character device 0:0, and an opaque directory is an xattr rather than an entry.
  Dropping the translation does not fail - it produces a layer in which a deleted file is still
  present, which is worse.

Two more refusals, both where silence would be worse than failure:

* **A blob is verified against its descriptor before its bytes are used**, not after. Unpacking is
  where an archive gets to create files, so verifying afterwards is verifying after the damage.
  This is the boundary between hash worlds: SHA-256 is confined to exactly this check, and ℋ takes
  over as identity from there (§3.1).

* **A manifest index with no entry for the target platform is refused**, naming what the image does
  provide. Falling back to the first available manifest yields a build running another
  architecture's binaries, which surfaces as "exec format error" far from here - or on a
  multi-arch fleet, only on the worker that happens to run it.

`Unpack` needs `CAP_MKNOD` for whiteouts, which is one more thing rootless operation has to solve
rather than work around.

**Linux landed too, and the honesty check paid immediately.** The `Sandbox` port had been written
expecting confinement to be *conditional* on Linux - the same binary confining as root and running
unconfined otherwise, against macOS where a VM always confines. That is not what happens.
overlayfs requires `CAP_SYS_ADMIN` (experiment E13), so an unprivileged guest cannot assemble a
layer stack at all: it does not run unconfined, **it does not run**.

The honest response is therefore refusal (I10) rather than degradation (I11), and the refusal
names the capability - "operation not permitted", raised by a mount several layers inside a guest
process, tells a user nothing they can act on. `Confines()` is now unconditional for both
backends, because neither has a state in which it works without confining.

This is exactly what a second implementation is for. With one backend the distinction between
"cannot confine" and "cannot run" was invisible, and the port encoded the wrong one.

Layer capture landed: a result now names what it produced, to the full metadata of §3.3.
Confinement has not, so the executor marks results captured only when the sandbox confines, and
the local backend never does. Until a confining backend exists the scheduler publishes nothing to
𝔄. That is designed, not a stopgap: an unconfined step must not write an entry a confined build
would later trust. The engine is correct and slow, which is the right order to arrive in.

**Finding, 2026-08-13: the executor was never told its base.** `runStep` materialised the base
stack, then called `Executor.Run(ctx, n, w)` - and the handle went nowhere. The executor
materialised an empty stack instead, so every step ran against nothing. The port now passes the
base stack, not a handle: on a real backend the executor is inside a VM and the scheduler cannot
see its filesystem at all, so naming the layers is the only thing that crosses the boundary.

**Finding, measured 2026-08-13.** Two byte-identical builds produce different layer digests,
because `mkdir` stamps a directory with the wall clock. This does not affect caching - Κ₁ keys on
inputs, not outputs - but it would have made experiment **E14** (build twice, cross-check) fire on
every build that creates a directory. Layers therefore carry two digests (§3.3a): the identity,
and a timestamp-free content digest that E14 compares. Without this the determinism screen would
have had a 100% false positive rate and been switched off within a day.

S5 is the one open *design* question rather than open construction - everything else is scheduled
work. It narrowed considerably while building the seam: the choice of source is **not** primarily
about overhead but about whether a source can report its own loss. An observation missing entries
turns Κ₂ into a false-hit generator, so a source that drops events silently is unusable at any
speed, while one that counts its drops is usable and merely slower on the builds where it drops.
`Observation.Incomplete` carries that, and the scheduler declines to key on an incomplete
observation. Experiment **E17** fixes the kill criteria and remains to be run.

### Sub-phases, as capability areas

The sections below describe *what* has to exist, not the order to build it. Read them as the
contents of the milestones above.

### 2.0 Architecture: where the lines go

The Green Paper defines Σ as a function whose result does not depend on when or where it runs.
That is not only a correctness property - it is the architecture. Anything that affects *when and
where* is separable from anything that affects *what*.

Four layers, strict dependency direction, each knowing strictly less than the one above:

```text
  ┌──────────────────────────────────────────────────────────────┐
  │ interpreter        Earthfile -> IR. Knows nothing about       │
  │                    execution, caching, or engines.            │
  ├──────────────────────────────────────────────────────────────┤
  │ core       PURE    graph, keys, cache policy, placement,      │
  │  engine/core       masks, beliefs, records, divergence.       │
  │                    Touches no fd. Imports no os/net/syscall.   │
  ├──── ports ───────────────────────────────────────────────────┤
  │ execution          run an op in a materialised rootfs,        │
  │  engine/exec       report exit code + observations            │
  ├──────────────────────────────────────────────────────────────┤
  │ materialisation    layer stack -> mounted filesystem;         │
  │  engine/snap       overlay, erofs, guest agent                │
  ├──────────────────────────────────────────────────────────────┤
  │ bits               digest -> bytes. CAS, registry, peers.     │
  │  engine/blob       Knows nothing of layers, steps or keys.     │
  └──────────────────────────────────────────────────────────────┘
```

**The three concerns are deliberately not one layer.** Moving bits, wrangling containers, and
deciding what to run are different problems with different failure modes and different tests:

| Layer           | Knows about                  | Never knows about         | Tested by                                                    |
| --------------- | ---------------------------- | ------------------------- | ------------------------------------------------------------ |
| bits            | digests, bytes, peers, HTTP  | layers, steps, keys       | containerd's `ContentSuite`, in-memory fake, fault injection |
| materialisation | layers, mounts, overlay, VMs | steps, keys, scheduling   | containerd's `SnapshotterSuite`, the 500-layer case          |
| execution       | processes, namespaces, argv  | caching, placement        | the differential oracle                                      |
| core            | steps, keys, graph, policy   | files, sockets, processes | pure unit and property tests, deterministic simulation       |

### 2.0.1 The core is pure, and that is enforceable

The core computes: key derivation (GP 4.5, 4.6), the two-outcome lookup rule (GP 4.4), readiness
and placement, mask and belief policy, record diffing. All of it is a function of values.

**Enforce it with a test, not a convention.** A unit test that walks the import graph of
`engine/core` and fails if it reaches `os`, `net`, `os/exec`, `syscall`, `time` (for `Now`), or
any adapter package. Fifty lines, runs in milliseconds, and it converts an architectural intention
into a build break. Architectures decay because nothing objects; this objects.

Ambient inputs - clock, randomness, environment - are injected as interfaces for the same reason.
A core that cannot read the clock cannot accidentally make a cache decision depend on it (GP A4).

### 2.0.2 Ports

| Port            | Shape                                        | Fakes available                                   |
| --------------- | -------------------------------------------- | ------------------------------------------------- |
| `BlobStore`     | has/get/put by digest                        | in-memory; corrupting; slow; flaky                |
| `ActionCache`   | get/put by key with writer identity          | in-memory; lying; unsigned                        |
| `Materialiser`  | stack -> handle; handle reports observations | simulated tree                                    |
| `Executor`      | (handle, op, ε) -> (exit, changed set)       | **simulated: sleeps and emits a synthetic layer** |
| `Transport`     | discover peers, fetch blob, publish          | in-process `MemTransport`; partitioned; lossy     |
| `Clock`, `Rand` | injected ambient                             | deterministic, seeded                             |

The simulated `Executor` and `Materialiser` are what make the model-first method work: with those
two fakes the entire core runs at memory speed, so a hundred workers with induced failures can be
exercised deterministically from a seed in milliseconds.

### 2.0.3 Two seams that are easy to get wrong

**Observations cross a layer boundary awkwardly.** The observation set 𝑟 (GP 3.4) is *produced* by
materialisation - that layer sees the faults - and *consumed* by the core, for key derivation. The
`Materialiser` port must therefore return a handle that reports observations. What must not happen
is the executor reaching upward to record into the core; that inverts the dependency and makes
both untestable.

**Placement is core, transport is an adapter.** The scheduler decides *which* worker runs a step,
given a snapshot of who holds what. That decision is a pure function and belongs in the core,
where it can be tested against a thousand synthetic topologies without a network. The transport
only moves bytes. Conflating them - a scheduler that asks the network directly - is how
distributed schedulers become untestable.

### 2.0.4 What not to abstract

Over-abstraction has a cost and this design has room to make that mistake. There is no general
"filesystem" port, no "container runtime" facade beyond the two operations above, and no plugin
system. A port exists where there are genuinely two or more implementations that must be
substitutable - `Executor` has runc, Apple and simulated; `Transport` has iroh and in-process.
Anywhere there is exactly one implementation and no test double, a direct call is correct.

The `engine.Engine` interface from Phase 1 sits *above* all of this: the entire native stack and
the entire BuildKit engine are its two implementations.

### 2a. IR and scheduler (4 weeks)

`engine/ir`: the fifteen node kinds above. Node ID = `hash(op, resolved input IDs, platform)`.

**Hard constraint, decided now because it cannot be retrofitted:** a node's *result* is a
content-addressed OCI layer blob, never a local snapshot identifier. If results are not
portable blobs, Phase 3 is impossible. Every step is pure and therefore retry-safe.

**Address content by uncompressed digest (diffID), not by compressed digest.** Every lazy-pull
format except SOCI - eStargz, zstd:chunked, nydus - re-encodes the layer and so changes its
compressed digest while the content is identical. A CAS keyed on the compressed digest would
silently miss every hit the moment a layer is converted, and would store the same bytes twice.
Compression is a transport encoding, not identity. See the experiments doc, E12.

`engine/sched`: one graph for the whole build, one worker pool, futures rather than
solve-shaped barriers. The interpreter awaits exactly the node it needs; everything else
keeps running. Cache keys reuse `inputgraph`'s hasher, subsuming auto-skip.

### 2a-pre. Should LLB be our IR?

Tempting, and worth taking seriously rather than dismissing: LLB is stable, documented, tooled,
and adopting it would shrink Phase 2 from "write an IR, a converter and a scheduler" to "write a
scheduler". `earthfile2llb` would survive intact. `dockerfile2llb` would come free, closing the
`FROM DOCKERFILE` gap that §2d otherwise admits is a wall.

**The answer is no for the internal IR, yes at the boundary** - and the evidence is already in the
deletion budget.

#### Three of our ugliest hacks are LLB's limitations, not BuildKit's

RFC §1b attributes these to the process boundary. They are not; they are the data model:

| Hack                          | LOC  | What LLB lacks                                                                                              |
| ----------------------------- | ---- | ----------------------------------------------------------------------------------------------------------- |
| `util/vertexmeta/`            | 163  | typed metadata - so we base64 JSON through the *display name*, the only user-controlled field that survives |
| `util/llbutil/fakedep.go`     | 65   | an ordering edge - so we `COPY` a UUID-prefixed file that cannot exist                                      |
| `llbsolver/ops/exec.go` patch | fork | a host operation - so `LOCALLY` is a patch to someone else's solver                                         |

Adopt LLB internally and all three come back, permanently, because they are not workarounds for a
socket. They are workarounds for a vocabulary.

#### The deeper conflict is identity

LLB's identity is the vertex digest over the operation and its inputs - a chain key by
construction. Our §2a-bis key is the *observed input set*, which is not expressible in LLB's model
at all. So the question reduces to something crisp:

> Is observed-input caching worth writing our own IR?

Given §2a-bis is plausibly worth more than everything else in this plan - a base-image bump
invalidating only what read the changed files - yes. If the counterfactual measurement in
Appendix B.5 comes back small, this decision should be revisited, and LLB-internal becomes
attractive again. That is the measurement that governs it.

Our IR also needs observation sets, masks, step classes, platform affinity as a scheduling input,
recorded flattening policy and prediction hints. None have an LLB representation, so an
LLB-internal design ends as LLB plus a parallel sidecar of our own metadata - the worst of both.

#### Where LLB earns its place: the boundary

* **Import.** Translate LLB into our IR, and `FROM DOCKERFILE` works via `dockerfile2llb` without
  the native engine understanding Dockerfiles at all. This is the cheapest available fix for the
  one v1 gap that §2d calls a wall, and it argues for building the importer earlier than planned.

* **Export.** Emit LLB for interoperation and debugging where the mapping is clean, accepting that
  it is lossy: host ops, observation sets and hints have no representation.

LLB therefore occupies the same role as an OCI layer's compressed form (green paper §3.2): a
transport and interchange encoding, never identity.

#### How close should our IR sit to LLB?

As close as Earthfile sits to Dockerfile - which is to say **a recognisable superset, not a
clone**. Earthfile kept `FROM`, `RUN`, `COPY` and their meanings, so knowledge transfers and the
mapping is obvious; it added targets, `SAVE ARTIFACT`, `BUILD` and `WITH DOCKER`, and it did not
inherit Dockerfile's execution model. Do the same here.

**Align the vocabulary. Do not copy the representation.**

|          | Decision                                                                                                                                                                                                                                           |
| -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Keep** | the op vocabulary and its semantics: exec, file ops, sources (image, git, local, http), merge, diff, platform, cache and secret and ssh mounts. Same names, same meanings, so LLB import is mechanical and anyone who knows BuildKit can read ours |
| **Add**  | the five things LLB cannot say: a `host` op, typed metadata, a real ordering edge, the observation set, and scheduling inputs - affinity, masks, prediction hints                                                                                  |
| **Drop** | the wire-format orientation                                                                                                                                                                                                                        |

That last row is the one worth arguing. **LLB is a wire format wearing an IR's clothes.** Its
design is shaped by having to be marshalled to protobuf, sent over a socket and interpreted by a
foreign process: digests stand in for pointers, metadata has to survive serialisation, and nothing
can hold a reference to anything live. We deleted that boundary. In-process, an IR can be a typed
Go graph with real references, interfaces and closures - which is precisely how it becomes able to
carry a typed metadata struct, a host operation and an observation set, the three things §1b shows
LLB forced us to smuggle.

So: an IR that a BuildKit engineer would recognise on sight and could map op-for-op, expressed as
native Go rather than a protobuf clone.

#### But we must send work to workers - does that not need a wire format?

It needs one, and having it be a *different, poorer type* is the point.

A worker is sent a **step assignment**, not a subgraph: the base as a list of layer digests, the
op, the ambient state, the platform, plus advisory hints (green paper C.2). It never learns how
its inputs were derived, because they are content-addressed and materialisable from the CAS.
**Content addressing collapses the graph into digests at the boundary** - which is exactly why we
do not need LLB's central design feature, digests standing in for *unevaluated* subgraphs. Ours
stand for evaluated results.

That is a much smaller thing to serialise than a build graph, and keeping it separate from the IR
buys three things:

* the wire's constraints - versioning, forward compatibility, canonical bytes - stay off the IR,
  which is how LLB acquired its shape in the first place;

* workers running different engine versions negotiate on a small, stable surface;
* the assignment type simply **cannot express a `host` op**, so a malicious peer cannot request
  one. A property of the type beats a check that can be forgotten.

Batching whole targets to one worker - which E11's 200 ms per-step floor argues for - is a
sequence of assignments, not a graph. Still flat.

#### Does flat dispatch cap the graph size?

Yes, and the limit should be named rather than discovered. With one assignment per step, the
driver holds the whole graph and makes a decision per step:

| Bottleneck    | Where it bites                                                                                                           |
| ------------- | ------------------------------------------------------------------------------------------------------------------------ |
| driver memory | graph, futures and records for every step; E8 caps this at 5 GB on a 7 GB runner                                         |
| dispatch rate | one scheduling decision plus one message per step; at 10⁶ steps even 50 µs of driver work is a minute of pure scheduling |
| fan-out       | one driver holding connections to thousands of workers                                                                   |

For a CI build of thousands of steps this is irrelevant. For a monorepo of millions of actions -
the Bazel-scale case - the driver is the ceiling.

**The fix is not putting graph structure on the wire. It is recursive delegation.** Send a worker
a *region it owns end to end*, and let it run its own scheduler over that region: one scheduler
becomes a tree of schedulers, and the parent makes one decision instead of ten thousand.

This needs no new wire concept, because the assignment's ω is already an operation:

```text
    ω = exec(argv)              run one command
    ω = build(target, args)     evaluate a whole target, recursively
```

**Earthfile already draws the delegation boundary: `BUILD +target`.** A target has a defined
interface - arguments in, artefacts and images out - and `inputgraph/` already hashes one without
evaluating it. A delegated sub-build is therefore an assignment with a coarser ω, and the child
resolves its own unknowns, its own `IF`s and its own `$(...)`. The unevaluated graph never crosses
the wire; the *authority to evaluate it* does.

Two honest caveats:

* **Bazel has a structural advantage we do not.** Its action graph is declared statically, so it
  can be partitioned and queued in advance. Ours is discovered progressively (§2a-preq), which is
  a real disadvantage at extreme scale and no wire format fixes it. Delegation helps precisely
  because the child does the discovering.

* **At that scale the driver stops being a CLI.** Bazel-scale means a scheduling *service* with
  persistent queues, priorities and multi-tenancy - a different product, not a bigger flag.
  Designing it now would be speculation.

**What to do now** is cheap: keep `build(target, args)` in the assignment vocabulary from the
start, even while nothing sends it. A wire type that cannot express delegation is expensive to
retrofit; one that can, and does not yet, costs an unused enum value.

**The artefact that keeps this honest is the mapping table** - LLB op to our op, in both
directions, with the lossy cases named. It belongs beside the importer, it is what makes
`FROM DOCKERFILE` maintainable, and it is exactly the sort of table that rots silently unless a
test walks it.

### 2a-preq. The graph is discovered, not given

Classical DAG scheduling assumes the graph is known before scheduling starts. Ours is not. Two
constructs make the shape of the build depend on results produced by the build:

* **`IF`** - the branch taken depends on a step's exit code.
* **`$(...)`** - *shell-out*: `ARG V = $(cat version.txt)`, or `BUILD +x --tag=$(git describe)`,
  runs a command in the build container and substitutes its stdout. The value can then determine
  which targets are built and with what arguments.

**This already costs us parallelism today.** `earthfile2llb/interpreter.go:2624`'s
`requiresShellOutOrCmdInvalid` detects a shell-out, and `isSafeAsyncBuildArgs`
(`interpreter.go:2649`) refuses to dispatch a `BUILD` asynchronously when one is present. A single
`$(...)` in a build argument turns a parallel fan-out into a serial one. The unknown does not
merely delay planning; it disables concurrency around it.

#### Separate speculative *planning* from speculative *execution*

They have completely different costs and should not be conflated.

**Speculative planning is nearly free and should be the default.** Record the graph *shape* from
previous runs. The scheduler then plans against the predicted graph - critical path, placement,
prefetch - and repairs the plan when reality diverges. Nothing is executed on speculation, so a
wrong prediction costs a re-plan, not compute.

**Speculative execution costs real compute and is therefore conditional.** It is *sound* for us by
construction - steps are pure (green paper I1), so running a branch that is not taken cannot
affect the result - but soundness is not affordability.

| Rule                                    | Reason                                                                            |
| --------------------------------------- | --------------------------------------------------------------------------------- |
| Never speculate a `host` step or a push | Side effects outside the sandbox. A speculatively executed deploy is unforgivable |
| Speculate only with slack capacity      | On a fleet with idle workers the cost is runner-minutes, not wall clock           |
| Bound the depth                         | Do not speculate past a second unknown; the branching factor compounds            |
| Prefer the predicted branch             | Speculating both sides is the fallback when confidence is low, not the default    |

**Mispredicted work is deposited, not wasted.** A speculatively executed branch produces a valid,
content-addressed cache entry. If that branch is ever taken - another platform, another developer,
next week - the work is already done. Speculation therefore has a much better expected value here
than in a CPU, where a mispredicted path is discarded entirely.

#### Predicting the unknowns

Both unknowns are facts about a step class, so they are recorded and generalised exactly as masks
(§2a-bis) and determinism beliefs (§2a-quater) are, with the same L0-L3 hierarchy:

* **Branch outcomes.** Most conditions are stable across builds - `IF [ "$TARGETARCH" = "amd64" ]`
  is fixed per platform, `IF [ -f Cargo.lock ]` changes almost never. A predictor with history is
  cheap and accurate; the interesting cases are the few that flip.

* **Shell-out values.** Not speculatable in general - the value space is unbounded - but highly
  predictable: last run's value is usually this run's value. Plan on it, and re-plan if the actual
  value differs. And because a shell-out is itself a pure step, its *value* is cacheable, so an
  unchanged `$(cat version.txt)` need not re-run at all.

Prediction is a hint and never a correctness input, the same rule as everywhere else: a
mispredicted branch produces a re-plan, never a wrong build.

### 2a-bis. Observed-input caching: the read-set is a cache key, not just a prefetch hint

The read-set recorded in §3.0a was introduced as a prefetch optimisation. It is worth more than
that. If we know exactly which bytes a step *read*, then those bytes plus the command are the
step's true identity - and two steps with **different parent layers** but identical observed
inputs are the same step.

Contrast the two models:

|                        | Cache key                                            | Consequence                                                                                              |
| ---------------------- | ---------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| BuildKit, Docker today | parent layer digest + command                        | any change anywhere in the base invalidates every step above it, whether or not those steps could see it |
| Observed-input         | hash of the files actually read + command + argv/env | a base-image change invalidates only steps that touched what changed                                     |

The practical effect is the one every user complains about: a security bump to a lower layer
currently rebuilds the world. Under observed-input caching it rebuilds only what read the
changed files. Across a fleet, where cache hits are the entire economic argument, that is
plausibly worth more than everything else in this plan - and it is only *possible* if we own the
executor, because BuildKit's key is the chain by construction.

**The mechanism is already half-built.** §3.0a's lazy CAS-backed lower layer sees every `open`,
`stat` and `readdir` a step performs, because every one of them faults through it. The prefetch
profile and the cache key are the same data structure read two ways.

**The failure mode that decides whether this is sound: negative lookups.** A step that does
`if [ -f /etc/foo ]` reads nothing when the file is absent, so a naive read-set omits it - and
the step would then falsely hit its cache against a base where `/etc/foo` exists. The
observation set must therefore include *failed* opens, `stat`s of absent paths, and the full
result of every `readdir`, not merely the bytes returned. Miss any of those and the result is a
**false cache hit**, which E5 rightly treats as an immediate stop rather than a percentage
regression - it is the one failure a build system must never have.

Prior art, all worth reading before writing code: `fabricate` and Vesta (post-hoc dependency
capture by tracing), `ccache`'s direct mode and Buck2's dep files (recorded include sets), and
Nix's content-addressed derivations (early cutoff when a rebuild is byte-identical).

#### Masks: priors for the *first* run

The read-set above is exact but useless the first time: it only helps when the identical step
runs again. Most of the value is in the case where something *similar* has run before. Every
`RUN cargo build` reads `~/.cargo`, `/usr/lib/rustlib` and the target dir, whatever the project.
Every `apt-get install` reads `/var/lib/dpkg` and `/etc/apt`. The filenames differ; the shape
does not.

So generalise the read-set into a **mask**: a bitmap over a layer's chunk or file index,
recording which parts of *that layer* get touched, indexed by *who was touching it*. Masks live
with the layer, which is content-addressed, so they are shared across every project using the
same base image.

A hierarchy of priors, consulted in order and unioned:

|     | Key                                                              | Available when            | Precision |
| --- | ---------------------------------------------------------------- | ------------------------- | --------- |
| L0  | exact step hash                                                  | this step ran before      | exact     |
| L1  | command class + base layer digest - "cargo build over rust:1.9x" | anything similar ran      | high      |
| L2  | base layer digest alone - what any step touches in this image    | the image was used before | moderate  |
| L3  | structural - directory-level shape                               | always                    | coarse    |

The important property: **L1-L3 are computable from the Earthfile alone, before the step's
inputs are even resolved.** Parse the file, extract the command shape and the base ref, look up
the mask, and a cold worker can begin fetching likely blocks while the graph is still being
built. That is the difference between a worker that starts fetching when the step is scheduled
and one that starts fetching when the build starts.

**A mask is a superset, and that is what makes it safe.** It over-approximates what a step might
need. Being wrong in the *inclusive* direction costs only bandwidth. Being wrong in the
*exclusive* direction costs nothing at all: the step demand-faults the missing block through the
same path it would have used anyway, gets the right answer, and the mask is extended for next
time. Correctness never depends on the mask being right, only latency does - so the mask is
free to be learned, shared, stale, or absent.

**But extension alone is a ratchet, and ratchets end at "everything".** A mask that only ever
grows converges on the whole layer, at which point it is eager transfer wearing a hat. Every
entry therefore needs to decay: keep a hit count per entry and drop entries unused across the
last N runs, so a mask tracks what steps *currently* touch rather than everything they have ever
touched. The decay rate is a tuning parameter and should be measured, not guessed.

Measure a mask with **precision** - fraction prefetched that was used - and **recall** - fraction
used that was prefetched. Recall is what saves latency; precision is what stops the mask
degenerating. Track both over time: a mask whose precision is falling is one whose decay is too
slow.

Two caveats. A mask is a *hint* and must never be a correctness input, the same rule as §3.0a.
And masks aggregate access patterns across builds, so sharing them beyond an organisation leaks
information about what those builds touched - keep them within the same trust boundary as the
cache itself.

#### Prior art: Dagger's `dagql`

Worth reading before designing any of this. Dagger's DAG and cache layer
(`github.com/dagger/dagger`, `dagql/`) already implements two things claimed as novel above:
`cache_evidence.go` records a `CacheOutcome` and a `CacheHitRoute` for every call - *why* a hit
happened, emitted as telemetry, which is the explainable-cache-miss idea from RFC section 1a.4 -
and `cache_egraph.go` maintains an e-graph over "operation shape plus canonicalised input/output
equivalence state", which is machinery for exactly the section 2a-bis problem of recognising that
two differently-derived steps are the same step. Their test suite is also the closest analogue to
ours: 409 test files, a broad `core/integration/` suite, cache tests including a canonicalisation
race test and a metadata-prune benchmark, and the whole thing dogfooded by running the tests
under Dagger itself.

#### Cost: we are not hashing files

The obvious objection is that a compile touching 50,000 headers would have to hash 50,000 files
per step. It does not, because **the inputs are already content-addressed**:

* Files from a lower layer arrived from the CAS and already carry a digest in the layer's index.
  Building the key is an index lookup, not a read. Zero I/O.

* Files from the local build context are hashed once per build during context transfer, not once
  per step.

* Files produced by an earlier step in this build get their digests from that step's layer diff,
  which we compute anyway (E4). Once per producer, never per consumer.

So the per-step cost is: collect N digests, sort them for determinism, hash the concatenation.
For 50,000 inputs that is 1.6 MB through BLAKE3 - about a millisecond - against the ~200 ms
per-step floor E11 measured. Not the bottleneck.

#### The real cost is negative lookups, and there is a trick

A compiler searching twenty include directories for five hundred headers performs on the order
of ten thousand *failed* stats. Recorded naively, the lookup-set dwarfs the read-set and the key
becomes enormous.

The fix: **a directory's listing digest subsumes every negative lookup inside it.** If the step
consulted `/usr/include` and that directory's listing hashes the same, then every "not found" in
it is still not found. Ten thousand failed stats collapse to one entry per directory consulted.
Store the positive read-set as a bitmap over the layer's index rather than a list of paths, and
the whole profile stays small enough to sit in the CAS beside the output.

#### Two-level lookup, so the fast path pays nothing

Keep the conventional chain-based key as **L1**: cheap, conservative, requires no profile, and
hits whenever the base is unchanged - the common case in a dev loop. Consult the observed-input
key as **L2**, only on an L1 miss, which is exactly when the alternative is a full rebuild. The
new machinery therefore cannot slow down the case it does not help.

#### Choosing the observation mechanism

| Mechanism               | Overhead                                    | Sees negative lookups                                              | Verdict                                             |
| ----------------------- | ------------------------------------------- | ------------------------------------------------------------------ | --------------------------------------------------- |
| our CAS-backed lower FS | none extra - reads already fault through it | **yes** - a lookup for an absent path still reaches the filesystem | **the natural choice**                              |
| eBPF                    | very low                                    | yes, with work                                                     | good second, needs privilege and kernel floor       |
| seccomp-unotify         | moderate                                    | yes                                                                | viable                                              |
| fanotify                | low                                         | **no** - open-centric                                              | disqualified by E5b                                 |
| ptrace                  | severe - two stops per syscall              | yes                                                                | unusable for a build tool                           |
| `LD_PRELOAD`            | low                                         | partly                                                             | unsound: static binaries and raw syscalls bypass it |

The lazy filesystem we need for §3.0a is therefore also the cheapest correct observer, and it is
the only one on that list that gets negative lookups for free rather than as an extra
subsystem. That is a strong argument for building the FS layer before the caching mode that
depends on it.

**Staging.** This lands *after* the native engine works with conventional chain keys, as an
opt-in mode, with a verification harness that runs a corpus both ways and compares outputs
byte-for-byte. Correct-but-slower beats fast-and-occasionally-wrong by an enormous margin here:
a false hit ships a wrong artefact, and the user will not find out from us.

### 2a-ter. Unpoisonable caching

**The invariant: a poisoned cache may make a build slower. It may never make it wrong.**

Everything below exists to hold that line. It is achievable, but only by recognising that a
build cache is two different things with two different trust properties, and that today they are
usually conflated.

| Layer            | Maps                      | Self-verifying?                                                                                                             |
| ---------------- | ------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| **CAS** - blobs  | digest -> bytes           | **Yes.** Ask for digest D, hash what arrives, reject on mismatch. An attacker can withhold bytes but cannot substitute them |
| **Action cache** | step key -> result digest | **No.** That mapping is a *claim*. Nothing in the key lets a consumer check the result without doing the work               |

So the CAS already satisfies the invariant by construction: corrupt it and you get a miss and a
refetch, which is slower. **All the risk lives in the action cache**, and it is the only place
where poison turns into incorrectness.

#### The rule that makes it hold

**A cache lookup has exactly two outcomes: verified hit, or miss.** There is no third. Bad
signature, digest mismatch, malformed entry, unknown writer, quorum disagreement, unreadable
metadata - every one degrades to *miss*, meaning "do the work". Never to an error, and never to
using the entry anyway. Written as an invariant in the lookup path and tested by fault injection
that corrupts entries at random and asserts the build still produces byte-identical output,
only slower.

That single rule converts every failure of the caching system, malicious or otherwise, into a
performance problem. It is worth more than any amount of cryptography bolted on afterwards.

#### Who may write

Verification needs something to verify against, and there are three usable answers:

* **Signed entries, scoped by trust domain.** Entries carry a signature from a writer the
  consumer trusts. An attacker without the key can publish nothing that is honoured, so their
  best attack is denial of service - slower, not wrong. This is the practical baseline.

* **Write-scoping, which matters more than signing.** An untrusted build - a pull request from a
  fork - gets **read-only** access to the shared cache and writes to an isolated namespace. Its
  absence is the standard CI cache-poisoning attack, and no amount of signing helps if the
  attacker is a legitimate writer.

* **Quorum reproduction, for the steps that support it.** k independent workers compute the same
  step and the entry is accepted only if they agree, so an attacker must control k of them. This
  only works for deterministic steps - which is precisely the set the oracle corpus screening
  already identifies, so the classification is free.

And a fourth, which is really the transport option 5 wearing a different hat: for a step cheap
enough, **recompute instead of trusting**. A step that costs less than verifying its provenance
should not be cached at all.

#### Can the action cache be made self-verifying at all?

The action cache is a claim, and checking a claim about a computation without performing it is
the verifiable-computation problem in general form. It is not solved for arbitrary programs at
build-sized cost. But the space is not empty, and one reframing makes most of it tractable:

**We do not need to prevent a wrong entry. We need cheating to be detectable, attributable and
recoverable.** A build cache is unusually forgiving here, because every entry is *re-derivable* -
throw it away and rebuild. That is a far weaker requirement than a payments system, and it puts
several mechanisms in reach that "verify before use" does not.

| Mechanism                                                | What it buys                                                                                                                       | Cost                                                                                                     | Viable for us?                                                                          |
| -------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| **zk proofs of execution** (zkVMs: RISC Zero, SP1, Jolt) | genuine self-certification: verify the mapping in milliseconds without the inputs                                                  | proving is currently 10^4-10^6x native execution                                                         | **No.** Name it so nobody re-derives the idea; revisit in years, not quarters           |
| **TEE attestation** (SEV-SNP, TDX, Nitro Enclaves)       | hardware-signed binding of key to output digest; verification is a signature check                                                 | real infrastructure; unavailable on hosted runners; moves trust to the CPU vendor, and TEEs keep falling | Only for a controlled fleet, not for GitHub-hosted workers                              |
| **Sampled re-execution**                                 | probabilistic detection: verify a random fraction asynchronously                                                                   | epsilon extra compute, tunable                                                                           | **Yes, cheap.** The single best value here                                              |
| **Append-only publication log**                          | attribution and revocation: every entry is signed and logged, so a bad writer's whole output can be found and invalidated          | a log service, plus signing                                                                              | **Yes**, and it is what makes sampling useful - detection is worthless without recovery |
| **Quorum / k-of-n agreement** (Trustix, rebuilderd)      | an attacker must control k independent workers                                                                                     | k-times compute, deterministic steps only                                                                | Yes for the deterministic subset                                                        |
| **Record-and-replay of nondeterminism**                  | turns a nondeterministic step into a pure function of (inputs + recording), so it *can* be quorum-verified                         | recording infrastructure; the lazy FS already sees most of it                                            | Promising, and it enlarges the set the previous row applies to                          |
| **Execution receipts**                                   | the step emits its observed read-set and a hash chain of lookups; a verifier checks the receipt is consistent with the claimed key | almost free - the observation layer exists anyway (section 2a-bis)                                       | **Yes.** Catches key *unsoundness*, which is our own likeliest failure                  |
| **DAG bisection on dispute**                             | when two results disagree, binary-search the build graph to find the first divergent step, then re-execute only that one           | log(N) comparisons plus one step                                                                         | **Yes**, and it is what makes disagreement cheap to adjudicate                          |

The last one deserves a note because it is not standard practice in build systems. Borrowed from
optimistic-rollup fraud proofs: you never verify the whole computation, you make *disagreement*
cheap to resolve. If two workers produce different final artefacts, comparing intermediate step
digests pairwise and bisecting locates the first divergence in log(N) comparisons, and only that
single step needs re-running to determine who was wrong. Our graph is already content-addressed
per step, so the comparison material exists for nothing.

**What to actually build, in order:** execution receipts first, since the observation layer is
already required and receipts catch the self-inflicted failure. Then signed entries plus an
append-only log, because detection without attribution and revocation is theatre. Then sampled
re-execution over the deterministic subset. Bisection when there is a second executor to
disagree with. Everything else stays a reading reference.

Verdict on the impossible part: **the mapping cannot be made self-verifying, and pretending
otherwise would be the dangerous move.** What it can be made is *auditable* - wrong entries get
found, attributed to a writer, revoked, and rebuilt - and, per the two-outcome rule, never
capable of turning into a wrong answer for anyone who verifies before use.

#### The residual risk is ours, not an attacker's

An unsound cache *key* produces incorrect builds from a perfectly honest cache. If the key omits
a negative lookup, an environment variable, argv, the platform, or the locale, then a legitimate
entry becomes poison in a context it was never valid for. No signature detects this, because
nothing was forged.

That makes key soundness (section 2a-bis, experiment E5b) the larger half of "unpoisonable" -
and the self-inflicted half. It also argues for hermetic steps: the smaller the ambient state a
step can observe, the smaller the set of things the key must capture, and the less there is to
get wrong. Nix's whole design follows from this observation.

Prior art worth reading: Trustix, which builds multi-party agreement logs over build outputs;
rebuilderd, which independently reproduces packages and publishes disagreements; and in-toto and
SLSA for the attestation vocabulary.

### 2a-quater. Determinism screening, and attributing the cause

Build everything twice and compare. Simple, and it turns out to be load-bearing rather than a
nicety: **the deterministic subset is exactly the set of steps eligible for the strong
guarantees** - quorum verification (section 2a-ter), sharing results between workers, and
treating a cache entry as checkable by anyone. Screening is therefore the classifier that
decides which steps get the good properties, not a QA afterthought.

#### Localisation is free

A naive double build tells you "the artefact differs", which is nearly useless. Ours localises
by construction: every step has a content-addressed result, so comparing two runs step by step
gives **the first step whose digest differs** - everything downstream is collateral. That is the
bisection idea from section 2a-ter turned on ourselves, and the comparison material already
exists.

Then localise *within* the step by diffing the two layer trees: which paths differ, and how.

#### Attributing the cause

This is where the observation layer pays off a third time. Common causes have signatures, and we
can see both the bytes that differ and *what the step read*:

| Cause              | Signature                                                                            |
| ------------------ | ------------------------------------------------------------------------------------ |
| timestamp          | differing bytes parse as a recent date; or mtimes differ while contents match        |
| embedded path      | the diff contains the build directory or a temp path                                 |
| hostname, uid, pid | the diff contains them                                                               |
| randomness         | high-entropy difference, and the step read `/dev/urandom` or `getrandom`             |
| ordering           | same multiset of bytes in a different order - detectable by sorting before comparing |
| parallelism        | differs at `-j N` but not at `-j 1`                                                  |
| locale, timezone   | differs when `LC_ALL` or `TZ` is perturbed                                           |
| network            | the step opened a socket at all - visible because we own the sandbox                 |
| CPU features       | differs across machines but never on one machine                                     |

The systematic version is **controlled perturbation**, as `reprotest` does it: re-run the step
varying one environmental axis at a time - clock, hostname, pid, build path, `TMPDIR`, CPU count,
locale, `TZ`, `umask`, uid - and the axis that flips the output *is* the cause. That is bisection
over the environment rather than over the graph, it is entirely mechanical, and it turns "this
build is not reproducible" into "line 14 embeds `$PWD`".

#### Paying for it once: confidence-weighted spot checks

Doubling every build doubles CI, which nobody will accept. Treat it instead as a **budget
allocation**: spend a fixed fraction of build time - say 5% - on verification, and spend it where
it buys the most information.

**Determinism cannot be proven by repetition, only disproven.** N matching runs bound the failure
rate rather than establishing determinism: by the rule of three, N successes with no failures put
the 95% upper bound at roughly 3/N, so thirty clean checks means "fails less than 10% of the
time", not "is deterministic". Two consequences: the check rate falls as confidence rises, and it
**never falls to zero** - there is a floor, because the belief is a bound and bounds decay.

Allocate the budget by expected information gain:

* **Confidence.** Track successes and failures per step class and check less as the interval
  tightens. A step verified thirty times without divergence earns a low rate; a step checked twice
  earns a high one.

* **Risk, which the observation layer already tells us.** A step that read `/dev/urandom`, opened
  a socket, or ran with `-j > 1` is a far better candidate than one that copied a file. Bias the
  sample towards steps whose *observed behaviour* suggests exposure to nondeterminism, rather than
  sampling uniformly. This is the single biggest improvement over random spot checks, and it is
  free: we are recording those reads anyway.

* **Rarity is the danger.** A step that diverges one run in a thousand - a genuine race - passes
  thirty checks comfortably. Risk-weighting is the only practical defence, since no affordable
  sampling rate finds a one-in-a-thousand fault by chance.

* **Consequence.** Weight by blast radius: a nondeterministic step near the root of the graph
  disqualifies everything above it, so it is worth more checks than a leaf.

Beliefs generalise the same way masks do (section 2a-bis): a verdict keyed on the exact step hash
is worth little, since that hash may never recur, but a verdict for a *command class over a base
image* transfers to every future step of that shape. Same hierarchy, same reasoning.

**And harvest the free comparisons.** Duplicate executions happen naturally - a retried step
after a worker dies, the same target built on two branches, a worker recomputing something
another already has, a cold local cache next to a warm shared one. Every one is a determinism
check that costs nothing. **Never discard a duplicate execution without comparing it first.**
Over a busy CI fleet this may well produce more evidence than deliberate sampling does.

Finally, screen only what matters: steps whose results are about to enter the shared cache or be
shipped to another worker. A purely local step nobody trusts remotely need not be classified at
all.

#### It is also a feature

"Your build is 94% deterministic; here are the six steps that are not, and why" is a genuinely
rare thing for a build tool to be able to say, and it falls out of machinery we need anyway. It
also gives users a route to *fixing* their nondeterminism rather than merely being told it
exists, which is the difference between a diagnostic and a lecture.

### 2a-quinquies. One primitive: first divergence over build records

Several mechanisms above are the same operation wearing different hats. Dispute resolution
(2a-ter) bisects the graph to find where two workers disagreed. Determinism screening
(2a-quater) bisects to find the first step that differed between two runs. Cause attribution
bisects over environment axes. Explaining a cache miss walks the graph to the first changed
input. One algorithm:

> **Given two builds, find the earliest step at which they diverge, and say why.**

Make it a first-class primitive rather than four internal mechanisms, and it becomes a
user-facing command as well as the engine's own debugging tool.

#### What it needs: a build record

Every build emits a **record**: the step graph, and per step its cache key, result digest, the
inputs it was keyed on, the ambient state captured in that key (platform, relevant environment,
tool versions), timings, and optionally the observed read-set. Content-addressed and small - it
holds digests and structure, not content - retained for the last N builds.

The record is what makes divergence-finding near-instant: it compares recorded digests rather
than rebuilding anything. Contrast `git bisect`, which pays a full build per probe.

#### What it subsumes

| Question                                         | A and B are                       |
| ------------------------------------------------ | --------------------------------- |
| "why did this rebuild when nothing changed?"     | last build, this build            |
| "is this step deterministic?"                    | two runs of the same build        |
| "which worker is lying?"                         | two workers' records for one step |
| "why does it work locally but not in CI?"        | laptop record, CI record          |
| "what did that dependency bump actually change?" | before, after                     |
| "which change broke it?"                         | last green record, current record |

Same structure, same topological walk, six questions. The last row is the one users notice:
`git bisect` over a slow build is hours; first divergence over two records is milliseconds, and
it names the *step* rather than the commit, which is usually the more useful answer.

#### Saying why, not just where

The frontier step alone is not an answer. The report classifies: an input file's digest changed -
name it; the command changed; an environment value in the key changed; the graph shape changed so
the step exists in only one record; or **nothing in the key changed and the output did anyway**,
which means the step is nondeterministic and that is the finding.

That last case is the single most valuable diagnostic a build tool can emit, and no chain-keyed
system can emit it, because it does not know what the step actually depended on.

#### Name the files, not just the step

"Step `+build` missed cache" is a location, not a diagnosis. The report has to descend to
examples:

```text
+build  cache miss
  keyed on 8,412 inputs; 3 changed:
    src/parser.rs          contents differ
    Cargo.lock             contents differ
    .git/HEAD              contents differ   <- read by the step; likely unintended
  ... plus 2 unchanged directories re-listed
  at ./Earthfile:41
```

Three things make that affordable and useful:

* **Recovering paths costs no extra storage.** The record holds an aggregate key plus the
  read-set bitmap; the layer's index is itself content-addressed and already stored. Resolve the
  bitmap against the index and the paths come back on demand. Store a Merkle tree over the
  sorted input list rather than a flat list, and diffing two records descends only where subtree
  hashes differ - the differing files are found in time proportional to the number of
  differences, not the number of inputs.

* **Show examples, ranked, never the whole list.** Four thousand changed files is not a
  diagnostic. Print a handful, grouped by directory, with a count for the rest. Rank by what is
  likely to explain the miss: files in the step's own read-set above files inherited from the
  base; files the Earthfile mentions explicitly above ones it does not; unexpected paths - a
  `.git` directory, a timestamp file, an editor swap file - promoted, because those are usually
  the actual bug.

* **Call out the pathological cases by name.** "These files differ only in metadata, not
  content" is a distinct and common cause, and infuriating to diagnose by hand. So is "this
  directory was re-listed but nothing in it changed".

#### The counterfactual is worth reporting

Once read-sets exist, the tool can compare *what changed* against *what the step read* and say:

```text
  note: 3 of the 3 changed files were never read by this step.
        observed-input caching would have hit here.
```

That is a diagnostic and a measurement at once: run it across a corpus and it quantifies exactly
how much section 2a-bis is worth, in hit rate, **before** the feature is switched on. If the
number is small, that is an argument against building it - which is the point.

**Requirement, decided now:** emit build records by default from M2, the first milestone with a
cache to explain. They are cheap, every mechanism above assumes they exist, and retrofitting a
record format after four consumers have grown their own ad-hoc versions is the expensive path.

### 2a-sexies. Reliability: transient failure should cost time, not the build

CI must be boring. A 503 from a registry, a Docker Hub pull limit, a reset connection mid-layer -
none of these are interesting, and all of them currently fail builds. The invariant to hold is
the sibling of the caching one in section 2a-ter:

> **A transient infrastructure failure may make a build slower. It may never make it fail.**

Holding it needs three things: knowing what is safe to retry, needing the network less often, and
proving it under injected faults.

#### What is safe to retry, and what is emphatically not

Blind retry is how a five-minute failure becomes a forty-minute one. Classify by *effect*, not by
error:

| Operation                                              | Retry-safe?                                  | Why                                                                                                                                                        |
| ------------------------------------------------------ | -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| registry pull, blob fetch, `git fetch` of a pinned sha | **always**                                   | idempotent and content-verified - a retry either yields the right bytes or fails again; it cannot yield the wrong bytes                                    |
| peer blob fetch in the fleet                           | **always**, and try a *different* peer first | multi-source fallback is free in a P2P design; the registry becomes the last resort rather than the first                                                  |
| a pure `RUN` step                                      | **yes, wholesale**                           | steps are pure and content-addressed by construction (2a), which is the same property the fleet relies on for worker loss                                  |
| registry push of layers                                | yes                                          | layers are content-addressed; a re-push of existing content is a no-op                                                                                     |
| manifest or tag update                                 | yes, with care                               | idempotent as an HTTP operation, but last-writer-wins between concurrent builds                                                                            |
| **`RUN --push`**                                       | **never automatically**                      | arbitrary side-effecting commands - deploys, migrations, notifications. Retrying one of these can do real damage. This is the sharp edge in the whole list |
| **`LOCALLY`**                                          | **never automatically**                      | side effects on the developer's machine, outside any sandbox                                                                                               |

Two rules follow. Retry decisions belong in a **table of error taxonomy** - 429, 502, 503, 504,
connection reset, TLS timeout, truncated body are retryable; 401 once, after a token refresh;
403 and 404 never - held as data rather than scattered through call sites. And there must be a
**global retry budget**, in the Google SRE sense: retries capped as a fraction of operations, so
a systemically broken dependency fails fast instead of consuming the whole CI window.
`Retry-After` is honoured when the server sends it.

#### Needing the network less

Retries treat the symptom. The design already contains the cure, and it is worth making explicit:

* **The fleet CAS is a pull-through cache.** Once a base layer is in it, no further pull of that
  layer touches a registry from any worker. Docker Hub's rate limit stops being load-bearing
  because we stop asking.

* **Pin by digest.** `FROM alpine:3.22` is a moving target *and* a network round trip on every
  build; `FROM alpine@sha256:...` is content-addressed, cacheable forever, and needs no registry
  once the blob is local. This serves reliability, determinism (E14) and supply-chain security
  at once, so it deserves a first-class affordance - a command that rewrites tags to digests,
  and a lint that notices unpinned bases.

* **Degraded mode.** With everything pinned and present, a build should complete with the
  registry entirely unreachable. That is a testable claim, and a good one: *unplug the network
  and the cached build still works.*

#### Proving it

Reliability claims decay silently unless they are tested, so this gets the same treatment as
cache poisoning: a fault injector in the network path, a corpus, and an assertion that builds
still succeed. See experiment E15.

### 2b. Executor - macOS backend first, Linux second (8 weeks total)

The executor is an interface, `engine/exec.Backend`, with two implementations. **Build the
Apple one first.** Not for parochial reasons - three real ones:

1. **Honest abstractions.** The second implementation is where interfaces get broken. Writing
   the constrained, unusual backend first means the interface cannot quietly assume runc,
   overlayfs, cgroups or CNI. Doing Linux first and Apple second guarantees a leaky interface
   and a retrofit.
2. ~~**A much smaller v1.**~~ **Retracted - see experiments E1b.** The argument was that Apple's
   runtime takes an OCI image and returns a VM, so the backend needs no snapshotter of its own.
   It does. `container exec` accepts no mount options, so a running VM cannot have filesystems
   attached from outside: the host CAS is shared in at boot over virtiofs, and overlay
   assembly, rootfs construction and per-step snapshots all happen **inside the guest** via a
   small `earth-guestd` agent. Mac-first no longer buys a smaller v1.
3. **It is where the dev loop lives**, which is where watch mode (§1a.2 of the RFC) pays out,
   and PR #614 has already built the plumbing.

Against, and to be held in view: our CI is Linux, `macos-26` runners are scarce and dear, the
fleet in Phase 3 is `ubuntu-latest`, and most users build Linux images for Linux targets. So
**the Linux backend must land before Phase 3 and before the dual-engine matrix means
anything.** Mac-first is an ordering, not a scope cut.

**Measured** - see [experiments-adversarial.md](experiments-adversarial.md) E1. VM lifecycle
~690 ms, `exec` into a live VM ~65 ms, concurrency scaling 1.16x at 4-way. A VM is a **worker**,
not a step: boot a small pool at build start and keep it for the build, so boot is a
once-per-build cost of a couple of seconds. The 1.16x figure is the one that constrains design -
fill the pool steadily and ahead of demand, never in an on-demand burst.

Guest-side components the Linux backend gets from containerd and the macOS backend must supply
itself: overlay assembly, rootfs construction, per-step snapshotting, process isolation. This is
`earth-guestd`, and it is the honest cost of mac-first.

#### Is containerd the natural choice?

It is the *conventional* one, and E13 shows it embeds cleanly. But the honest argument for it is
narrower than "natural": **it is the substrate BuildKit already uses**. Same runc, same overlay
snapshotter, same content store. Choosing it means the bottom of the stack does not change, so
the executor swap is a smaller step than it looks and the two engines can be compared like for
like.

Two alternatives, and one of them is underweighted in this plan:

* **`containers/storage` + `containers/image`** - the Podman/Buildah/Skopeo stack. Buildah is
  literally "build OCI images without a daemon", which is our exact problem, and these libraries
  were designed for embedding rather than being daemon plugins that happen to embed.
  `containers/image` also carries `zstd:chunked`, its answer to E12's lazy pull. Against it:
  a second, unfamiliar dependency universe, and it is *not* what BuildKit uses, so the
  like-for-like comparison is lost.

* **Embed BuildKit itself as a library.** Considered and **rejected** (decision, 2026-08-12). It
  would capture much of the §1b deletion budget without writing an IR or a scheduler, but it is
  a halfway house: it keeps LLB, keeps the fork, keeps the dependency, and delivers none of the
  fleet - which is the half of this plan that LLB structurally cannot do (§2a). It buys time by
  entrenching the thing the project is trying to leave. Not to be reopened as a shortcut when
  Phase 2 gets hard; that is exactly when it will look attractive.

**Recommendation:** containerd, on the narrow argument above - same substrate, smaller step,
like-for-like comparison - rather than on "it is the obvious choice".

#### Linux backend

Link the libraries; do not require a containerd daemon (a "use the host daemon" mode is a
later, cheap addition).

| Need              | Package                                                         |
| ----------------- | --------------------------------------------------------------- |
| OCI content store | `containerd/v2/plugins/content/local` - SHA-256 only, see below |
| snapshots         | `containerd/v2/plugins/snapshots/overlay`, native fallback      |
| mounts            | `containerd/v2/core/mount`                                      |
| pull/push         | `containerd/v2/core/remotes/docker`                             |
| unpack/diff       | `containerd/v2/pkg/archive`, `plugins/diff/walking`             |
| run               | `containerd/go-runc` + `runtime-spec` (already direct deps)     |
| net               | CNI (`buildkitd/cni-conf.json.template` already exists)         |

**The snapshotter is ours, not containerd's. Decided 2026-08-13, revisit at S4.**

§2b's table names `containerd/v2/plugins/snapshots/overlay`. The implementation at
`engine/mat/overlay` mounts overlayfs directly instead, and the divergence is recorded here
rather than left to be discovered.

|        | containerd's snapshotter                                                                                                | ours                                                               |
| ------ | ----------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| brings | GC, a bbolt metadata store, `SnapshotterSuite`                                                                          | nothing we did not write                                           |
| costs  | string keys and parent chains that must be mapped onto our layer identities; a second source of truth about what exists | a conformance suite we maintain                                    |
| suits  | a daemon that owns its own view of the world                                                                            | an engine whose identities are content-derived and whose GC is 𝔅's |

The deciding argument is identity. containerd's snapshotter names snapshots with opaque string
keys and tracks parentage itself; ours are named by content, and the parent chain *is* the stack.
Adopting containerd's would mean maintaining a mapping between two naming schemes and two ideas
of what exists - which is the same conflation the two-content-stores note warns about.

Revisit at S4, when the executor needs GC and disk budgeting, because that is what containerd's
brings that ours does not. Until then the cost of adopting it exceeds the cost of the conformance
suite we already have.

**There are two content stores, not one, and they must not be conflated.** Green paper §3.1 fixes
ℋ ≡ BLAKE3-256 for internal identity and confines SHA-256 to OCI-facing structures, "distinct and
never compared". That separation is forced by the libraries, not merely preferred:
`opencontainers/go-digest` resolves an algorithm through a map keyed on `crypto.Hash`, which
admits SHA-256, SHA-384 and SHA-512 and nothing else. BLAKE3 is not a `crypto.Hash`, so
containerd's content store **cannot address a BLAKE3 blob**.

| Store             | Addressed by | Implementation                  | Holds                                    |
| ----------------- | ------------ | ------------------------------- | ---------------------------------------- |
| internal CAS      | BLAKE3-256   | `engine/blob`, ours             | step results, records, masks, profiles   |
| OCI content store | SHA-256      | containerd's, adopted unchanged | registry blobs, layers as OCI knows them |

**Consequence for S2's exit criterion.** "containerd's `ContentSuite` passes against our store" was
mis-specified: `ContentSuite` exercises `content.Store` over `ocispec.Descriptor`, which our
BLAKE3 store cannot implement without registering a counterfeit SHA-256-shaped algorithm. The
criterion splits:

* **internal CAS** - our own property tests: verification on read, insert-or-remove, concurrent
  writers, corruption refused. These exist and pass.

* **OCI store** - adopted from containerd unchanged, so `ContentSuite` passes by construction.
  What needs testing is our *use* of it, not the store.

**Verified, not assumed** - experiment E13 walks the full inner loop in ~90 lines with no
daemon and no plugin registry: create a content store, write and read back a blob, prepare a
snapshot, mount it, write into it, unmount, commit, stack a child snapshot on the committed
layer, confirm the child sees the parent's file, query usage. Two findings from it: the
executor needs `CAP_SYS_ADMIN` to mount (so rootless is a real deferred item, not a detail),
and `fsverity` is unavailable on tmpfs and overlay, which is a warning rather than a failure.

Ours to write, and the places bugs will live: cache-mount locking (`CACHE`/`CacheMountLocked`),
GC and disk budget, secret and ssh injection, `LOCALLY` (trivial here - it is why the fork
patches `llbsolver/ops/exec.go`), `WITH DOCKER`.

**Automatic layer flattening, in v1.** Experiment E11 shows a target of 1,000 sequential steps
failing on today's engine at step 500 - `OVL_MAX_STACK`, the overlayfs limit on lower layers -
reported as a bare `invalid argument`. Both backends inherit the limit, since both use
overlayfs. The engine must commit and squash the chain every N layers, and the policy needs
designing rather than defaulting: squashing trades away per-step cache granularity across the
squashed range.

### 2c. Nanosecond mtime fidelity (2 weeks, cross-cutting)

Requirement: `cargo` (and every other mtime-fingerprinting incremental compiler - `ccache`,
`ninja`, `tsc --incremental`) must keep working across layer boundaries. Cargo compares the
mtimes of dep-info inputs against the fingerprint file. Coarse timestamps do not merely cause
spurious rebuilds: two writes inside the same second are indistinguishable, so cargo can
*miss* a change and link a stale artifact. That is a correctness bug, not a performance one.

**Verified finding: upstream containerd truncates to whole seconds.** Its `ChangeWriter` sets
`hdr.Format = tar.FormatPAX` - which can carry nanoseconds - and then throws them away
(`pkg/archive/tar.go`, `hdr.ModTime.Truncate(time.Second)`). The apply side is already fine:
`UtimesNanoAt` in `pkg/archive/time_unix.go`. The loss is entirely in the writer.

**Done, in our containerd fork.** `github.com/gilescope/containerd`, branch
`giles-nanosecond-mtimes`: nanoseconds preserved by default, truncation available as an
opt-in `WithSecondPrecisionModTime()` `ChangeWriterOpt`, with a `FORK DELTA` note on why.
`atime`/`ctime` stay zeroed deliberately - reading a file bumps its atime, which would make a
layer digest depend on who last read the source tree. The native engine links containerd v2
and therefore picks this up directly.

**The BuildKit engine keeps truncating**, and that is fine. It pins containerd v1.7.8 through
`earthbuild/buildkit` and imports the old `containerd/archive` path, which the v2 fork cannot
reach. Not worth back-porting: it is the status quo, not a regression, and the engine's
remaining job is `FROM DOCKERFILE` and registry cache rather than fast incremental Rust.
Consequence for §2d: the dual-engine matrix asserts nanoseconds on `native` only. Since the
engines are never mixed and share no cache, their layer digests diverging is a non-event -
it costs nobody a cache hit.

Remaining work:

1. Wire the fork in: `replace github.com/containerd/containerd/v2 => github.com/gilescope/containerd`
   once `engine/exec` exists, and drop it again if the change lands upstream.
2. Extend the assertion past the tar header: the fork's tests pin the writer, so add a
   full round trip - write diff → apply into a fresh snapshot → `stat` → nanoseconds equal -
   once `engine/exec` can produce a snapshot to apply into.
3. An Earthfile-level regression test: build a Rust crate, re-run with no source change
   through a layer round-trip (and, in Phase 3, through a *remote worker*), assert zero
   recompilation.
4. Do not re-flatten downstream. BuildKit's local and tar exporters stamp
   `time.Now().Truncate(time.Second)` (`exporter/local/export.go:94`, `exporter/tar/export.go:77`);
   our equivalents must not, or the writer fix is undone one layer later.

**Two policies, chosen per output kind** - this is a genuine tension and must be explicit:

| Output                                      | Policy                                                                     |
| ------------------------------------------- | -------------------------------------------------------------------------- |
| cache layers, step results, fleet transfers | preserve exact nanoseconds                                                 |
| published/exported images                   | clamp to `SOURCE_DATE_EPOCH` for reproducibility (`WithModTimeUpperBound`) |

Never the reverse. Normalising cache layers destroys incrementality; preserving timestamps in
published images destroys byte-reproducibility.

This lands in Phase 2 but constrains Phase 3: a step result shipped to another worker is a
layer blob, so a truncating writer would silently break incremental caching across the fleet -
in the exact configuration where the win is meant to come from. Filesystem support is not a
concern (ext4, xfs and overlayfs all store nanoseconds); the tar writer was the only lossy hop.

### 2d. Export and parity (4 weeks)

Write images straight into the local docker/containerd store. The embedded registry,
pull-ping and EarthBuild-exporter machinery exist only to cross the process boundary and are
deleted for this engine, not ported.

Parity gate: run `tests/` under both engines in CI, as a matrix. A native-engine failure is a
release blocker for `--engine=native` only; BuildKit remains the default until parity holds.

Deliberately **not** in v1 for the native engine, and documented as such in the flag's help:
`FROM DOCKERFILE`, registry cache import/export, rootless mode.

Because engines are not mixable, "not in v1" means *a project needing any of these stays on
the BuildKit engine entirely* - there is no per-command fallback. So each gap is a wall, not a
speed bump, and each has to be priced as one:

| Gap               | Who it walls off                                           | Escape                                                                                           |
| ----------------- | ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `FROM DOCKERFILE` | anyone migrating from Docker incrementally - likely common | **import LLB** and reuse `dockerfile2llb` unchanged (§2a-pre); cheaper than porting the frontend |
| registry cache    | shared CI cache across machines without the fleet          | fleet CAS covers the fleet case; standalone CI still needs it                                    |
| rootless          | hardened/shared CI hosts                                   | genuinely deferred                                                                               |

`FROM DOCKERFILE` should therefore move into the native engine before it becomes the default,
not stay a permanent exclusion.

**Burn-down, done.** `goconst` 302 -> **0**, over E199-E202. Three findings came out of it that
were not lint at all: production re-spelling the language's own command names, production
re-spelling the registry protocol's media types, and a refused-flag ratchet left a notch below the
truth. Two lessons that outlive it - a test constant is visible only in its own package, and a
file's `package` clause is not its first line. Lint backlog 769 -> 504 (linux), 717 -> 454 (darwin);
the largest remaining are `govet` 156 and `gosec` 95.

### Decisions taken, 2026-08-17 (second round)

| question                                                   | decision                                                                                                        |
| ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| S5's RUN tracer, after access times were eliminated (E203) | **seccomp user notification**; the `unsafe` is granted for the three calls set out above                        |
| what the filter traps                                      | **opens, metadata and exec** - openat/openat2, newfstatat/statx, access/faccessat2, readlinkat, execve/execveat |

Exec was added later and on different evidence. It was declined when the argument for it was diagnostics; it went in when a step running `./main` was found to record the loader's libraries and not the program - an observation any base with the same libc satisfies, which is a false-hit vector rather than a missing nicety (E220).
| go-iroh has no tagged release                              | **pin the pseudo-version** and build against it                                          |
| the Go lint backlog, 505 findings                          | **report only** - `+lint` runs in CI and does not gate                                   |
| the nits file at 38 sections                               | **leave it**, keep appending; revisit once this branch is signed                         |

The filter scope is the one with a specification behind it. 𝑁 - what a step looked for and did not
find - is not optional under I3: a step that runs `[ -f /etc/foo ]` and branches on the answer has
read the *absence*, and a source recording only opens would admit exactly the false hit I3 exists
to prevent. The cost is real - every `stat` in a configure script becomes a round trip - and it
buys a source that does not have to declare itself lossy on the steps the tier exists for.

The lint decision splits rather than surrenders. `+lint-gating` keeps shell and changelog linting as
merge gates, both of which pass; folding two working gates into one broken one to excuse the broken
one would lose more than it saves. The Go linters run as their own reported step, and the count
lives here so it stays visible while it comes down.

### Decisions taken, 2026-08-17 (third round)

| question                                                                                                                                                                             | decision                             |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------ |
| a `CACHE` mount makes a step uncacheable, so the slowest step in a maven, npm or cargo build rebuilds every time. BuildKit and Earthly cache it with the mount excluded from the key | **keep refusing, and say so louder** |

Excluding the mount from the key admits exactly the reuse I3 forbids: two builds whose mounts
differed can be served each other's result. Being stricter than the incumbents here is what the
engine is for. What changes is visibility - the build now names each uncacheable step and what makes
it so (E228), because a refusal costing a minute a build was happening in silence.

A narrower version was considered and rejected for now: the tracer knows which paths a step read, so
a step that touched nothing under its mount is honestly keyable. Sound, and it buys nothing for the
cases that matter - `mvn package` certainly reads its own cache.

### S6, where it stands

| piece                                                         |                                              |
| ------------------------------------------------------------- | -------------------------------------------- |
| the assignment type, which cannot express `host` (C.3)        | done, structurally (E229, E230)              |
| canonical serialisation and its decoder (B.1)                 | done, and 𝒮 is now one function (E231, E245) |
| the reply, which cannot carry a payload (C.3)                 | done (E232)                                  |
| identity and the allowlist (C.1)                              | done (E233)                                  |
| `Transport`, and an in-process fleet (C.5)                    | done (E234)                                  |
| a delegating executor, refusing rather than failing           | done (E235)                                  |
| a build through a fleet producing what a local build produces | done (E236)                                  |
| the fetch order and multi-source fallback (C.4, I6)           | done (E237, E239)                            |
| chunk-level verification (C.4, I2)                            | done (E238)                                  |
| `earth/ctl/1` over a real connection                          | done (E247)                                  |
| `earth/blob/1` over a real connection                         | done (E248)                                  |
| a worker binary, told where and deriving who (C.1)            | done (E254)                                  |
| a driver that actually uses them                              | done (E255)                                  |
| a worker that goes away leaving the fleet (C.5)               | done (E256)                                  |

The worker and driver rows were the different kind of work: every piece above them is a mechanism
with a test, and those two were a decision about how a person asks for a fleet. The answer taken is **workers
dial the driver, and a shared session key gates who may join** - so the configuration is three
environment variables on a worker and one on a driver, with no port to forward, no certificate to
manage and no discovery mechanism to run.

What is left in S6 is no longer protocol or product but **operation**: whether a worker should serve
blobs to its peers as well as to the driver, and what a driver reports when a fleet shrinks to
nothing mid-build. Both are named in the open questions below rather than claimed.

## S5's RUN tracer: the `unsafe` this needs, and what it buys

Requested for per-use consent before anything is written. Access times were tried first and are
out on measurement (E203), so the field is seccomp user notification or FUSE.

### What seccomp-unotify costs in `unsafe`

`golang.org/x/sys/unix` ships every constant - `SECCOMP_FILTER_FLAG_NEW_LISTENER`,
`SECCOMP_IOCTL_NOTIF_RECV`, `SECCOMP_IOCTL_NOTIF_SEND`, `SECCOMP_IOCTL_NOTIF_ID_VALID` - and the
`SockFilter`/`SockFprog` types. It ships **no wrapper for any of the three calls**, and its typed
ioctl helpers cover `Winsize`, `Termios` and a dozen others, none of them these. So three uses:

| #   | call                                                     | the pointer                                    |
| --- | -------------------------------------------------------- | ---------------------------------------------- |
| 1   | `seccomp(SECCOMP_SET_MODE_FILTER, …NEW_LISTENER, &prog)` | `&unix.SockFprog`                              |
| 2   | `ioctl(fd, SECCOMP_IOCTL_NOTIF_RECV, &req)`              | `*seccompNotif`, a struct this engine declares |
| 3   | `ioctl(fd, SECCOMP_IOCTL_NOTIF_SEND, &resp)`             | `*seccompNotifResp`, likewise                  |

A fourth, `…NOTIF_ID_VALID`, takes a `*uint64` and closes a real TOCTOU: a pid can be recycled
between a notification arriving and this engine reading that process's memory, so the cookie is
checked before the read and the read is discarded if it fails.

**Why no safe route exists.** `prctl(PR_SET_SECCOMP)` installs a filter but cannot return a listener
descriptor, so #1 is `seccomp(2)` or nothing. #2 and #3 are ioctls whose argument is a struct; Go
has no way to pass one without `unsafe.Pointer`.

Reading the *path* an intercepted `openat` was given needs no `unsafe` at all: the argument is a
pointer into the target's address space, and `pread` on `/proc/<pid>/mem` is an ordinary file read.

### The invariants

* **Lifetime.** Each pointer is taken in the same expression as the call, which is the documented
  pattern. `prog.Filter` aliases a `[]SockFilter` the garbage collector cannot see through a
  `uintptr`, so a `runtime.KeepAlive(filter)` follows the call. The kernel copies the filter during
  the syscall, so nothing must outlive it.

* **Layout.** The risk here is ABI, not memory: a struct that does not match the kernel's is a
  silently wrong read. **The kernel states the size itself** - `SECCOMP_IOCTL_NOTIF_RECV` is
  `0xc0502100`, whose size field is `0x50`, so `unsafe.Sizeof(seccompNotif{})` must be 80. Same for
  the other two. That is a table-driven test, per architecture, and it turns the whole ABI question
  into something mechanically checked rather than reviewed.

* **Blast radius.** All three sit in one file behind `Tracer`, which hands out observations. Nothing
  above it sees a descriptor or a struct.

### The alternative, honestly

FUSE needs no `unsafe` in this tree at all - `github.com/hanwen/go-fuse/v2` is pure Go - works
exactly where the engine runs (measured, E-earlier), and sees **filesystem operations**, which is
what Ω is defined over in §4.7. seccomp sees syscalls: a wider, coarser net that needs a path
resolved out of another process's memory before it means anything.

Against that, FUSE puts a userspace round trip in front of every read a step makes, and a step that
reads a large tree pays for all of it. seccomp's filter can be narrowed to the handful of syscalls
that open things, and everything else runs at full speed.

`github.com/seccomp/libseccomp-golang` does not help: it needs cgo and a shared library, which
changes how this engine is distributed, and it does not remove the unsafety - it moves it into C.

## Phase 3 - fleet (10-12 weeks)

### 3.0 The actual problem: getting bytes to where the work is

Everything else in this phase is scheduling detail. The question that decides whether
distribution wins or loses is **how a worker gets access to the bytes a step needs**, and the
measurements so far constrain the answer more than intuition does:

* A step costs ~200 ms cold, ~16 ms warm, of pure machinery (E11). Shipping work is only worth
  it for steps substantially longer than that, which argues for coarse granularity - whole
  targets, not individual steps.

* Capturing a layer costs ~1.5 s per 100k-file tree even when the changed set is known (E4).
  *Producing* the artefact is expensive, not just moving it.

* go-iroh's `blobs` already gives content-addressed, BLAKE3-verified chunked streaming, so
  verified transport is free; the design question is what to send, not how to send it safely.

* Identity must be the uncompressed digest (§2a), or every re-encoding misses the cache.

Six options. They compose, and the real design is a policy that chooses per step.

| #   | Approach                                                                                                        | Wins when                                                                     | Costs                                                                                       |
| --- | --------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| 1   | **Eager whole-layer, peer to peer** - ship complete blobs between workers, as rebuck2 does                      | layers are small, or reused by many steps                                     | moves bytes nobody reads                                                                    |
| 2   | **Lazy remote filesystem** - mount the layer, fault chunks in on read (eStargz/SOCI/nydus shapes, peer-sourced) | closures are large and sparsely read                                          | per-fault latency; a FUSE or virtiofs layer; ugly failure semantics if a peer dies mid-read |
| 3   | **Move the work to the data** - schedule each step onto the worker already holding most of its input closure    | inputs are concentrated and reused                                            | idle workers when data is concentrated; needs a real placement algorithm                    |
| 4   | **Shared object store** - S3/GCS, or `actions/cache`, as the one substrate; no peer-to-peer at all              | simplicity, NAT traversal, auditability                                       | re-centralises the bottleneck; egress cost and latency                                      |
| 5   | **Recompute instead of fetch** - re-execute a cheap deterministic step locally rather than ship its output      | step is short and its output is large                                         | needs a cost model and genuine determinism; E4 says capture is dear too                     |
| 6   | **Chunk-level delta** - ship only the chunks the receiver lacks (rsync, casync, zstd:chunked's rolling hash)    | rebuilt layers differing slightly from their predecessor - the common CI case | chunk index maintenance and hashing CPU                                                     |

**The measurement that picks between them** is one ratio: **bytes actually read by a step,
divided by bytes in its input closure.** If that is small, options 2 and 6 win decisively and
option 1 is waste. If it is near 1, option 1 is right and the others are complexity for nothing.
E12 measures exactly this ratio and should therefore run *before* any transport is built.

**Provisional design, to be overturned by that number:** 3 as the default policy (locality-first
scheduling, since the cheapest transfer is the one that does not happen), 1 as the mechanism
when transfer is needed, 6 layered on once chunk indices exist, and 5 only for steps the
scheduler can prove short and deterministic. Option 4 is worth keeping as the boring fallback
for environments where the mesh cannot form - it is what rebuck v1 does, and it works.

Note that 2 and 3 pull in opposite directions: lazy access makes placement matter less, good
placement makes laziness matter less. Building both first is how this phase becomes a year.

### 3.0a Demand-fault now, prefetch the rest in the background

Option 2 is worth expanding, because the interesting version is not "fetch on read". It is
**fetch on read, then speculatively fetch what this step is about to want**, and our design has
a property that makes that prefetch exact rather than heuristic.

**Don't replace overlayfs - replace what is under it.** The overlay *upper* dir is what makes
write capture cheap: E4 measured 1.5 s against 21.8 s for the same layer when the changed set
is known, a 14x difference. Keep overlayfs for writes. Make the *lower* layers lazily
materialised. Three ways to do that:

| Lower-layer mechanism                                                              | Notes                                                                                                                                                                                                                                                       |
| ---------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **erofs**, already in containerd (`plugins/snapshots/erofs`, `plugins/diff/erofs`) | Kernel filesystem, no FUSE overhead. Its **tar index mode** indexes the original tar instead of extracting it, which is the "mount a tar and read only what you need" mechanism, native to the stack we chose. Actively developed upstream. **Start here.** |
| **FUSE** via `go-fuse`                                                             | Total control over fault handling and prefetch policy; per-syscall cost is real, and FUSE beneath overlayfs has known sharp edges                                                                                                                           |
| **virtiofs** on macOS                                                              | The guest already reads host directories, and E1c proved the boundary preserves nanosecond mtimes                                                                                                                                                           |

**The property worth exploiting.** Steps are content-addressed and pure (§2a). So the set of
files a step reads is a *function of its input hash*, and can be memoised under that same hash
like any other result. After a step has run once anywhere in the fleet:

* its read-set is known and stored in the CAS beside its output;
* any worker later scheduled that step can prefetch exactly that set in one batched request,
  before the step starts, turning a stream of demand faults into a single bulk transfer;

* a miss is safe - an unexpected read simply demand-faults as normal, and the profile is
  updated.

eStargz does a weaker version of this: a prioritised-files list baked in at *image build* time.
Nydus does chunk-level prefetch by pattern. Neither can key the profile by step identity,
because neither has one.

**Between the two, nydusd is the better reference and probably the better dependency** (Giles,
2026-08-24). It is a purpose-built lazy-loading daemon with chunk-level deduplication and a real
prefetch story, where eStargz is a tar-compatibility trick: a seekable gzip whose prioritised-files
list is fixed when the image is built and cannot know anything about the step that will read it. The
places this matters here are the fragment format (§2 lazy bases) and anything that later fetches a
layer over the network without materialising it whole. What this engine has that neither does is the
step's *observed* read-set as a key, so a borrowed design should be read for its chunking and its
daemon shape rather than for its prefetch policy - the policy is the part already answered better
here. Keying it by the step hash makes the prefetch exact and shareable
across the whole fleet through the machinery the CAS already provides. Prior art for the general
shape is real and worth reading before building: Meta's EdenFS, Buildbarn's `bb_clientd` for
Bazel remote execution, and CernVM-FS.

**Three caveats before anyone gets excited.** The 500-layer overlayfs limit (E11) applies to
lazy lower layers just as it does to eager ones. Prefetch competes with demand faults for the
same link, so it must yield to them rather than saturating. And a step that reads
nondeterministically - a wildcard over a directory, a timestamp-dependent path - will have an
unstable profile; the design must treat the profile as a hint that is always safe to be wrong
about, never as a correctness input.

### 3a. Transport (3 weeks)

`engine/fleet/mesh`, over go-iroh (`iroh.Bind(ctx, iroh.WithALPNs(...))`, `ep.Connect(ctx,
addr, alpn)`, `Router` for inbound ALPN dispatch, `key` for ed25519 identity).

* ALPN `earth/ctl/1` - control: claim, heartbeat, result, cancel.
* Data: use go-iroh's `blobs` package (BAO-verified streaming, tickets) rather than
  inventing a CAS protocol. Our node results are already content-addressed blobs, which is
  exactly its shape.

* Membership: `gossip` for worker liveness and provider hints.

**Rendezvous and its security.** Session key = HKDF over `(github.run_id, run_attempt, repo)`
*plus a per-run secret* - a workflow secret or an HMAC of the OIDC token. rebuck2's keyless
form derives from public metadata; on a public repo that lets a stranger join the mesh and
serve poisoned outputs. The driver additionally publishes an allowlist of worker node IDs and
rejects the rest. Cheap now, painful later.

### 3b. Driver and worker (5 weeks)

* Driver = the `earth` process the user ran: owns the graph, the interpreter, local outputs.
* Worker = `earth worker --session <s>`: stateless, holds a CAS shard, claims steps.
* Assignment is least-loaded **plus data locality plus platform affinity** - runners are
  amd64, the dev machine is arm64; never schedule a step onto the wrong architecture.

* Blobs move worker-to-worker, not through the driver; the driver schedules, it does not
  relay bytes.

* Batch blob requests. One stream per blob does not survive a thousand-blob sync - rebuck2
  hit exactly this and replaced it with chunked `GetMany`.

* Worker loss re-queues the step elsewhere. Steps are pure, so this is a retry, not a
  failure - rebuck2 v0 fails in-flight actions here and we can do better for free.

### 3c. GitHub Actions integration (2-4 weeks)

An action pair (`driver`, `worker`) pinned to one full SHA so engine and choreography cannot
drift - rebuck's hard-won lesson. Prove the mesh on a two-machine LAN before trusting CI NAT.

**Exit criterion:** a build of our own `Earthfile` completes on a driver plus four
`ubuntu-latest` workers, faster than the same build on one runner, with byte-identical
outputs.

### WITH DOCKER: the cache question, settled 2026-08-20

The largest single item to parity, and the first question is not the daemon.

**Sharing the inner daemon's storage and being cacheable are one axis, not two.** A block with
`--cache-id` is handed storage an earlier build wrote, so its steps are not a function of their
inputs and are marked `NoCache` (I3). A block without one starts empty and is cached.

That answers both halves of what is wanted: sharing is what most uses reach for and costs exactly the
cacheability it was always going to cost, and the full isolation a test looking for cache misses
needs is the **default** rather than a flag to remember.

Done: `--cache-id` is accepted, reaches both keys, and marks every step of the block - including the
`--pull` and `--load` steps this engine generates, which write into the same storage (E354).

Also done: a block that asked for isolation is **refused** the host daemon rather than quietly given
it (E355). Those were two sound decisions - what sharing means, and what daemon exists - that had
never had to agree, and together they cached a step under a key saying nothing about what it saw.

**A daemon runs.** `dockerd` starts inside a plain user namespace on the machine this project
measures on - no `rootlesskit`, no `slirp4netns` - and answers in about a second: `29.4.3 vfs`
(E364). Every flag it needs came from a daemon refusing to start, and an integration test starts one
and asks it what it is.

**And one of that recipe's five requirements was an artefact of the experiment.** The tmpfs over
`/run` was needed because E364 ran in the *host's* mount namespace; a step's root is an overlay and
`/run` in it is writable already (E365). Written down as mounts, what a step's own daemon needs is
just its cache: `--cache-id=x` mounts the directory that name derives to and puts the daemon's root
inside it, and naming no cache mounts nothing, so the daemon writes into the step's own root and
goes away with it. The isolation E354 promised is the absence of a mount, not the presence of a
flag. `awaitDaemon` is the other half: it refuses the empty answer that made E364's first test pass
in 350ms against no daemon at all.

**The wire can now ask for one** (E366): a `Daemon` on the request, nil for every step that is not
in a WITH DOCKER, saying both the root and the socket rather than deriving the second at each end -
the two-implementations-of-one-rule failure that presents as a client unable to reach a daemon that
is running. Protocol version 11, for the reason mounts got 3 and cancel got 8: a guest that ignored
the field would run the body with nothing behind the socket.

**And the guest can refuse one** (E367): a request with no root, no socket, or a relative path for
either is a caller bug and says which half, and a platform with no namespaces to put a daemon in
refuses rather than running the body with a socket and nothing behind it - which would read as
Docker being broken rather than as this engine declining. Both mutants there are about whether the
check runs at all, which is why the assertion goes through the client.

**Where it runs is settled, and it is not where I expected** (E368): *beside* the step, not in it.
The step's namespaces are made by the step process itself, so there is nothing to join before it
exists - and the daemon does not need to be in them, because the step's root is a directory on the
guest's disk and a unix socket in it is reachable from both sides. The consequence is worth stating:
a WITH DOCKER image needs a Docker **client**, not a daemon. `daemonArgs` and the wait moved into
`engine/guest` for this, since the guest is what starts it.

**The lifetime is written** (E369): make the directories, launch, wait until it answers, run the
body, stop it on every path - including the path where the wait failed, which is where the natural
early return leaks a `dockerd` holding the step's overlay open while the capture reads it. The
shutdown's own error is reported only when nothing else went wrong, because a failing body outranks
a failing shutdown but a succeeding one does not.

**The process exists** (E371): `launchDockerd` starts the guest's own `dockerd` in its own process
group - a daemon leaves shims behind, and a signal to the leader alone leaves them holding the
step's filesystem open - and refuses with a message naming the *guest*, because every stock message
about an unreachable daemon advises installing Docker in the image, which is the one thing that
would not help. `Stop` is SIGTERM then SIGKILL after a grace period, and a daemon that died on its
own flags is reported as that rather than as a shutdown failure.

**A daemon has now run for real, through the code a step will use** (E373-E375): 1.359s to
`29.4.3 vfs`, and a clean shutdown. Three things had to be true and only one was in the plan. The
daemon needs a user namespace it is root in and a writable `/run` - `--exec-root` does not cover the
plugin manager, which uses `/run/docker/plugins` and nothing else - so the launch re-executes this
binary as a shim, which mounts a private tmpfs and `execve`s `dockerd`. Not `unshare -Ur … sh -c`,
which would build a shell command out of two paths that arrived over the wire. And the exec root is
a fixed `/run/earthbuild-docker` rather than a path under the step, because containerd refuses a
socket over 104 bytes and a step's root is most of that before the daemon appends anything.

That also invalidated an earlier conclusion. E365 argued the tmpfs away as an artefact, correctly,
for a daemon running *inside* the step; E368 then moved it out, and nobody re-derived it.

**The step's wiring is in**: `execRequest` runs the body inside `withDaemon` when the request
carries a `Daemon`, after `bindMounts` and inside its mounts - a daemon started before them would
write into the step's overlay and a named cache would be silently empty on every build.

**And the first step-level run met the nesting problem early** (E376). The shim re-executes this
binary, which needs `/proc/self/exe` rather than `os.Executable()` - a build-cache path does not
survive - and, inside a test that is itself in a user namespace, fails with `fork/exec
/proc/self/exe: permission denied`. A namespace inside a namespace, which is precisely what an
inception build is. Three hypotheses are written down with the experiment for each; none is
guessed at, and the integration test stays in the tree failing rather than being skipped into
silence.

**Solved, and the solution is the inception answer in miniature** (E377). Bisecting one variable at
a time showed the user namespace was never the problem: the **pid** namespace was. Go's parent writes
the child's `/proc/<pid>/uid_map`, and inside a pid namespace whose `/proc` was not remounted that
path names a different process, so the child keeps no mapping and its exec is refused. The fix is to
stop asking for what is already true - a user namespace is created **only when the process is not
already root**, which it usually is (E105). Nesting works by not nesting where there is nothing to
gain.

A step now reaches a daemon at its own `/var/run/docker.sock`, confined and chrooted, in 2.51s. It
first appeared to do so in 0.27s, because the readiness check was asking the *machine's* daemon
whether the step's was up (E378) - the third time in this project that the clock has been the only
thing to disagree.

**And the daemon can now explain itself** (E379). Every failure above was diagnosed from a line the
daemon printed and none of them reached the caller - they went to the guest's stderr while the build
got `exit status 1`. The tail of its output now travels with the exit, and EPERM on the `/run` mount
carries the answer for the case still to come: a private `/run` needs `CAP_SYS_ADMIN`, a container
has not got it by default, and the outer step is where it must come from.

**The polarity-independent half of nesting is built** (E380). Both spellings of the flag have to
answer the same question about a socket an inner build can already see, and it answers the same way:
inside a container it is the outer *step's* daemon and needs nobody's permission, because the
decision was taken one level up and this build is inside its blast radius; on the machine it is the
machine's daemon, root on it, and refused unless the operator has said otherwise; and nothing to
inherit is refused now rather than ninety seconds later as a daemon that appears broken. Being inside
a container is read from `/.dockerenv` or `/run/.containerenv`, never inferred from `/proc/self/cgroup`,
which stopped being true with cgroup v2.

Both functions are tested and **not yet called** - they wait on the default's polarity, which is
Giles's decision and is recorded above rather than guessed at.

**And there is a parity number at last** (E410, E411): the 116 `tests/*.earth` files the corpus walk
never saw plan **257 of 456 targets**, with the refusals named - `--wildcard-copy` and
`--wildcard-builds` at 24 between them, remote target references at 14, `HOST` at 7. **65** of the 199 refusals are this sweep's own conditions rather than engine
gaps - a `tests/` file expects a harness that puts context files beside it and builds the targets it
references - so the parity figure is **262 of 318, 82.4%**. It read 66.6% until the sweep was
given the same remote fetcher the corpus sweep uses: 68 refusals were a capability this harness
withheld rather than one the engine lacks, and `remote target references at 19` had gone onto a work
list (E417). Two smaller gaps were hidden behind them, and one was not a gap: the four
`parse error`s are `tests/`'s **negative cases**, Earthfiles that exist to be rejected, so counting
them as failures to plan meant an engine that stopped rejecting invalid input would have scored
higher (E418). Discounted too, the figure is **262 of 314**. The `COPY` four were real, and `--chown` is now
implemented (E419): the specification travels to the guest and resolves against the *destination
image's* passwd file, because resolving it on the guest would give a different machine's answer and
produce an image whose files belong to somebody who does not exist in it.
`COPY --allow-privileged` is accepted too (E420): it grants a permission this engine never uses,
because privileged execution is refused by name wherever it appears - both halves asserted. Parity is
**262 of 310, 84.5%**, and the last three increments moved the denominator rather than the numerator,
which is what a long tail looks like from the inside.

**And the planning sweep is mined out** (E421). Tallying only the engine's own refusals - after the
harness's and the deliberately-invalid are excluded - leaves 48 over about twenty causes, none above
three: `BUILD`, `RUN`, `no base image`, `FROM`, an artifact reference, an import. Every one is a
one-file question. The next real signal is the one the test plan named at M1 and nobody has built:
`tests/` **running** under the native engine rather than planning, because planning at 84.5% says
nothing about whether a build produces the right bytes.

**The first run proved that in one experiment** (E422). Thirty-seven `tests/` targets built rather
than planned: 13 succeeded, and one failure was a real bug the planning sweep could never see - an
`ENV` value referring to another variable was set literally, so `ENV MYPATH=hello:$PATH` gave a step
the five characters `$PATH` and any step adding to its PATH lost everything already on it. Expanded
in the guest, where the base image's environment is known; `tests/env.earth` builds.

**`CACHE` no longer makes a step uncacheable** (E424). It was, on the grounds that the mount's
contents are undescribed by the key - which is equally true of `RUN curl`, cached without hesitation,
so the rule refused the local directory and permitted the internet. Its effect was the opposite of
the construct's purpose: adding `CACHE` to go faster made every rebuild slower. A cache mount is an
accelerator and is cached; `--persist` and secret mounts still are not, because their contents are
part of what the step produced. The mount's identity is already hashed, so two steps naming different
caches remain different steps.

**And the builtin arguments** (E423): `ARG TARGETARCH` worked and `ARG EARTH_TARGET_NAME` did not -
the mechanism that supplies builtins on declaration covered the platform family and nothing else.
`EARTH_TARGET_NAME`, `EARTH_TARGET` and `EARTH_LOCALLY` now come with it, under both the current and
the legacy `EARTHLY_*` spellings, and the git ones stay unanswered because an empty string is a claim
to have read a repository. `tests/empty-git.earth` builds.

**And `ARG --global`** (E425): the flag was parsed and dropped, so a global argument reached no
function - `tests/command-explicit-global.earth` asserts a global *and* a local in one function, and
only the second held. Globals now travel on the state, a call's own value beats them, and they
survive a function calling a function. All three failures the execution probe found are fixed. That discount is computed by a predicate
with a test naming which refusals fall on each side, after a first version estimated it by eye in a
paragraph and got 51 (E413).

**`HOST` is implemented** (E415), which was seven of those refusals: state in the interpreter,
hashed into the key at both mirrors, version 13 on the wire, and a resolver file bound into the step
rather than written into it. Asserted from inside a step, by resolving the name. The sweep moved 257
to 261, and the mount validation turned out to have a hole that this was the first mount to fall
into - a mount whose source is only its contents.

**Done** (E409): a build inside a build. `earth-native` runs inside a `WITH DOCKER --isolate` block,
the inner build produces an artefact and the outer one carries it out, and the test asserts that
artefact's contents rather than an exit code. It needs three of this work's findings at once - the
inner store on a cache mount because overlayfs cannot stack on the step's overlay root, `--isolate`
so the inner engine does not share the outer step's daemon, and the daemon running beside the step so
the image needs only a client.
Then nesting one inside another. Two open questions are recorded rather than guessed: what two blocks of
the *same* build should share (they see one daemon today on both backends), and whether a nested
daemon inherits its parent's cache decision or takes its own.

With the host daemon, **a cache name buys sharing but not separation** - one storage area, every
block in it - and the step now says so rather than letting the name imply a division it does not get
(E362). That is the first thing a daemon of its own fixes.

A refusal now says whether *this machine* could host a rootless daemon when one is built, naming
every missing piece - the id-mapping helpers, a range in `/etc/subuid`, user namespaces, and a
`dockerd` to run (E361, E363). **Not** `rootlesskit` or `slirp4netns`: those make a namespace and a
network for a daemon started from a login shell, and a step here is already inside a namespace this
engine made. The Linux machine this project measures on reports ready and has neither of them, which
is the evidence for that distinction rather than an argument for it.

Where a shared cache lives is decided: `<store>/docker-cache/<name>`, and the name is checked again
at the executor because it arrives from a driver this worker did not write (E360). No daemon makes
that directory yet, which is the plan's own sequencing rather than an omission.

`--cache-id` is validated where it is written - it becomes a directory name, so a traversal is
refused at the line rather than at a mount (E358) - and `WITH DOCKER` now refuses arguments it does
not take, which had been silently discarding a word and changing the cache's name.

**Blocks already nest**, whatever the syntax suggests: `--load=+other` plans another target's
`WITH DOCKER` while this one is open, and the cache was being cleared rather than restored at each
`END` (E356). A nested block takes its own cache decision today, and that is now a deliberate answer
rather than an accident of scoping. The cache decision is settled first because it determines what the
executor has to mount, and building the mount before deciding what it means is how a knob gets a
meaning nobody chose.

**The execution gate exists** (E428): twelve `tests/` targets built rather than planned, sixty seconds
each, every target named as it is attempted - because its first run timed out after twenty-three
minutes and reported nothing but a goroutine dump. It builds 2 of 12 and that is a floor on a prefix
rather than a parity figure; the wider probe that found E422, E423 and E425 built 16 of 37. It is in
`+engine-daemon`, which now asserts seven tests by name - and adding it found that it had been
locating the tree with a path relative to two different things, so it passed under `go test` and
skipped in a container (E429).

**Increment one is done** (E426, E430): placement now refuses a fleet worker every step the fleet
would refuse to delegate - and that turned out to be four things it did not know rather than one. The
list lives in `ir.Op.OnInvokerOnly` and both read it. The schedule was deterministic and untrue - a worker charged for
work it never did, the invoker uncharged for work it did - and every later decision was made against
that load map. Nothing about where steps run changes; what changes is that the model and the
guarantee now agree.

**And a claim inside that design was false** (E427): it says `--sharing=locked` already serialises
concurrent steps, and nothing did. `locked` is the default, `shared` and `private` are refused with a
comment about "providing locked", and the guest's only lock is per handle - so two steps naming one
cache used it at once. An option accepted and not provided, and nobody even typed it. Steps now take
a lock per cache id, in sorted order, secrets excluded.

### CACHE mounts in a fleet: locality, snapshots, and what cannot change

**What a cache mount is.** A `CACHE /path` (or `RUN --mount=type=cache,target=/path`) declares a
directory that outlives the step and is shared by every step naming the same `--id`. Its identity -
target path, id, read-only flag, persist flag, sandbox - is hashed into the step key. Its contents
are not. The directory is bound into the step's chroot via `unix.MS_BIND|unix.MS_REC`, which makes
it a hole in the overlay: writes go to the host directory, not the step's upper layer. A cache mount
is a performance advisory only. It can change how long a step takes; it cannot change what layer the
step produces. An action-cache hit replays the stored primary layer regardless of whether the cache
directory is warm or cold on the serving machine, because the stored layer was never a function of
the cache contents and the overlay mechanics make that structural rather than promised.

**Why that invariant is preserved at every layer of the system.** `hashOperation` in
`engine/core/key.go` hashes each mount as `(Target, ID, ReadOnly, Persist, Sandbox)` and never
reads the directory. The overlay upper - what a step wrote, the thing that becomes the output layer -
physically cannot contain cache-mount paths: those paths are a bind mount over the overlay, so the
kernel routes writes to the host directory and the upper stays clean. An L1 hit is safe because the
replayed layer was produced against the same abstraction - a hole at the mount target - regardless of
what has accumulated in the hole since.

**Placement: hard mount affinity, any worker, first mover wins.** The current code enforces
invoker-only at execution time via `ErrNotDelegable` in `fleet/delegate.go:109`. This is correct but
dishonest: the placement pass assigns cache-mount steps to fleet workers (consuming their simulated
capacity), then the runtime overrides the assignment silently. The invoker is under-counted and fleet
workers are over-counted for the steps that actually matter.

The design replaces the silent runtime fallback with an explicit placement constraint. Before the
topological placement loop a `mountOwner map[string]string` (mount ID to worker ID) is initialised
empty. `eligibleFor()` takes this map as a parameter. For each plain (non-persist, non-secret) mount
on a step:

* If `mountOwner[id]` is unset, any worker may claim it - including fleet workers, not only the
  invoker.

* If `mountOwner[id]` is set to a different worker, that worker is ineligible for this step.

After `place()` selects a winner, it records `mountOwner[id] = winner.ID` for every plain mount the
step carries. First mover in topological order is the single authority per mount ID per build.

The `ErrNotDelegable` guard for plain mounts in `delegate.go` is then relaxed to match: a plain
mount step is delegable when its mount ID has been assigned to the requesting worker by the placement
pass. The guard for persist mounts and secret mounts is untouched.

This delivers real fleet distribution. A worker that claims mount ID `cargo` on build 1 warms up
`<LayerDir>/mounts/cargo` on its disk. Placement is deterministic (same graph plus same worker
inventory equals same schedule per §4.7.3), so the same worker claims the same mount ID on every
build until the worker set changes. The cache is never on the invoker by privilege; it is wherever
the placer first put the work.

**Multi-mount conflict.** A step that needs mount ID A (already assigned to W1) and mount ID B
(already assigned to W2) has no eligible worker. First mount in slice order - declaration order in
the Earthfile - wins: W1 becomes the owner of B, B's prior assignment to W2 is revoked, and any
future steps needing B are redirected to W1. W2's warm copy of B is abandoned. This is emitted as a
named warning (mount ID, both conflicting steps, the worker that lost) - not silent. Users who hit
this repeatedly should give the conflicting mounts distinct `--id` names.

**What a mutating mount is.** Not every cache mount writes. A step that only reads from a warm
dependency cache gets no benefit from being pinned when it could instead receive a pre-built snapshot
and run anywhere. `ir.Mount` gains a `Mutating bool` field (default true; the interpreter sets it
false when it can prove the step performs no writes, which is conservative - unknown is mutating).
Non-mutating mounts bypass affinity entirely: they are served to any eligible worker as an immutable
blob input, identical in treatment to a base layer, and the lazy-fragment machinery applies. Only
mutating mounts carry the hard eligibility constraint.

**Snapshot primitive.** A mutating cache-mount step's directory is useful to any future worker
assigned the same mount ID after a worker-set change, and to the action cache's co-indexing
requirement. After a mutating step completes - any exit code, because a partially-populated cache is
still a warm cache - the guest packages each mutating mount directory via `layer.PackOwned` and
stores the result as a content-addressed blob in the blob store. The blob ID is returned in a new
`Reply.CacheSnapshots map[string]ir.NodeID` field.

The action-cache `Entry` gains a `CacheLayers map[string]ir.NodeID` field. On a miss, the put path
writes `Entry{Layer: primaryOutput, CacheLayers: {"cargo": snapshotID}}` in one atomic call. On an
L1 hit, `e.CacheLayers[id]` supplies the cache snapshot that was in force when the primary layer was
produced. This is not a global sidecar. There is no global sidecar. A global last-writer-wins table
decoupled from the action cache produces wrong-but-green builds: revert to an older `pom.xml`, hit
L1 for the prior entry, receive the sidecar's current snapshot (not the one that was used when the
entry was written), run `mvn test` against the old bytecode with the new JARs. The `Entry` co-index
closes that window because the snapshot consulted is always the one the action-cache entry was
produced alongside.

**Fleet transfer.** When a cold worker is assigned a mount ID and a snapshot exists, the driver
includes a `CacheHint{SnapshotID, HolderAddr}` in `Assignment.Hints`. The worker fetches the
snapshot from `HolderAddr` peer-to-peer over the existing `earth/blob/1` QUIC wire (same
`PeerSource.Fetch` path used for layer transfer), unpacks it to `<LayerDir>/mounts/<id>`, and then
binds the directory before the step runs. This mirrors the layer provision path in `runner.go:98`;
no new protocol, no new ALPN, no new security surface.

`layer.PackOwned` buffers the entire tar in a `pipeBuffer` before transfer (layers.go:261-272). For
a large cache directory - a Rust `target/` or a Maven `.m2` in the gigabytes - the buffer allocates
that many bytes of RAM on the worker before the size gate fires. A configurable `MaxCacheSync` (512
MiB default) bounds what is transferred: a snapshot exceeding it causes the hint to be omitted and
the step to run against local state - which is warm if the worker has run this mount before, empty
on true first use. This is logged as a named SKIP with the mount ID and observed size. A mechanism
that is not running must not look the same as one that found nothing (I10).

Streaming pack (write directly to the blob store without buffering the whole tar) is required before
the 512 MiB bound is meaningful for large caches. Deferred.

**What is refused.**

*Cache contents in the step key.* Putting the snapshot ID in `DeriveChainKey` would cause every
cache write to produce a new key, cascading misses through the whole downstream graph. Contents never
enter the key.

*A global sidecar as the authoritative cache-state source.* The `Entry.CacheLayers` field is the
authority. The sidecar that populates `cacheHolders` on the driver is a warm-start hint used only
when the action cache misses; it is never consulted to resolve what cache state accompanied a stored
result.

*Two workers holding the same mutating mount ID within one build.* Hard affinity in `eligibleFor()`
makes this impossible by construction. The `--sharing=locked` constraint already serialises
concurrent steps on the same worker; the affinity constraint makes the same-worker property explicit
rather than emergent.

*Silent degradation when the snapshot bound is exceeded.* The driver emits a named SKIP. The build
continues; the step runs with whatever local state the worker has.

*`--persist` mounts distributed.* `--persist` copies cache contents into the output layer at step
end; it is a correctness property. `uncacheable()` returns true for persist mounts; they are not
delegated and not snapshotted.

*Secret mounts relaxed.* The `ErrNotDelegable` guard at `delegate.go:102-107` for secret mounts is
untouched. The plain-mount relaxation is a separate branch and does not affect it.

*Derived-cache / OpCache.* Turning every `CACHE` into a DAG node whose output is a content-addressed
layer sounds appealing - transfer cost falls out of the wave model for free - but parallel branches
sharing one mount ID each receive a copy-on-write view of the base snapshot, write disjoint subsets
of the cache (parallel Maven modules downloading different JARs), and the last-writer-wins join
discards all but one branch's writes. Every downstream step re-downloads what the losing branches
already populated. This is strictly worse than the mutable shared directory that Maven's own file
locking handles correctly today. The fix - deterministic overlay union at DAG join points, an
`OpCacheMerge` node - is architecturally correct but is a second design's worth of work. Refused for
now; the mutating-mount snapshot approach above is the right first move.

**First three tested increments.**

*Increment 1 - hard mount affinity (files: `engine/core/schedule.go`,
`engine/fleet/delegate.go`, `engine/core/schedule_test.go`).* Add `mountOwner map[string]string` to
`Scheduler`. Change `eligibleFor(n, w, native)` to `eligibleFor(n, w, native, mountOwner)` and add
the mount-ID guard: for each plain mount on `n`, if `mountOwner[m.ID]` is set to a worker other
than `w`, return false. After `place()` selects a winner, record `mountOwner[m.ID] = winner.ID` for
each plain mount. Relax `expressible()` in `delegate.go`: remove the `len(op.Mounts) > 0` blanket
refusal; retain refusal for `m.Persist || m.Secret`. All call sites to `eligibleFor` updated in the
same commit. Failing tests first: (a) two workers, two exec nodes both mounting `--id=cargo`, assert
both `Assignment.Worker` fields equal the same worker; (b) a third node with no mounts, assert it
may land on either worker; (c) a node with a plain mount actually delegates rather than hitting
`ErrNotDelegable`.

*Increment 2 - snapshot primitive (files: `engine/ir/ir.go`, `engine/fleet/reply.go`,
`engine/core/record.go`, `engine/guest/guest.go`).* Add `Mutating bool` to `ir.Mount` (default
true). Add `CacheSnapshots map[string]ir.NodeID` to `Reply`. Add `CacheLayers map[string]ir.NodeID`
to `Entry`. After a mutating cache-mount step completes (any exit code), the guest walks each
mutating mount directory via `layer.PackOwned`, stores the blob, and returns the IDs in
`Reply.CacheSnapshots`. The action-cache put path writes `Entry{Layer: ..., CacheLayers: ...}` in
one call. Failing test: a step writes a sentinel file to a cache mount; assert `Reply.CacheSnapshots`
is non-empty; fetch and unpack the snapshot blob; verify the sentinel is present with the correct
content. No fleet transfer yet; no change to `Assignment`; no change to placement.

*Increment 3 - fleet snapshot transfer (files: `engine/fleet/assignment.go`,
`engine/fleet/delegating.go`, `engine/fleet/runner.go`).* Add `CacheHints map[string]CacheHint` to
`Assignment.Hints`, where `CacheHint` carries `SnapshotID ir.NodeID` and `HolderAddr string`. The
driver maintains `cacheHolders map[string]CacheEntry` (mount ID to last snapshot plus holder
address), updated whenever a `Reply.CacheSnapshots` arrives. On assignment, if
`cacheHolders[id].Bytes <= MaxCacheSync`, populate `Hints.CacheHints[id]`. The worker provision
step - before `bindMounts` - fetches and unpacks each hinted snapshot via `PeerSource.Fetch`,
mirroring the layer provision path in `runner.go:98`. Snapshot exceeding the bound: hint omitted,
SKIP logged, step runs with local state. L1 hit path reads `e.CacheLayers[id]` to supply the correct
cache snapshot to downstream steps without re-executing anything. Failing test: two-worker setup,
step A warms mount `depot` on W1 and returns a snapshot, the driver populates the hint, step B (same
mount, would naturally go to W1 by affinity, but forced to W2 via a test override that clears
affinity for this case) receives and unpacks the snapshot before execution; assert the sentinel file
is present before the step body runs.

## Phase 4 - steady state

No deletion of the BuildKit engine. `engine/bkengine` remains the default and the fallback
for `FROM DOCKERFILE` and registry cache. Revisit only if native parity becomes total.

## Risks

| Risk                                            | Mitigation                                                                                                                                                        |
| ----------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Phase 0 shows solves are not the cost           | Stop after Phase 1. It is independently valuable and the fleet case still stands.                                                                                 |
| Cache-mount locking bugs corrupt caches         | Property tests plus a concurrency fuzz harness before enabling `CACHE` on native.                                                                                 |
| go-iroh is young (~35 stars)                    | Vendor-review it, budget for patches, keep the transport behind `engine/fleet/mesh` so it is swappable for `quic-go` + a relay.                                   |
| Two engines diverge in *Earthfile semantics*    | The dual-engine CI matrix is the contract. Output bytes may differ freely (no shared cache, no mixing); meaning may not.                                          |
| A v1 gap silently strands a project on BuildKit | Each gap is a wall, not a fallback - see §2d. `FROM DOCKERFILE` closes before native becomes the default.                                                         |
| Scope creep into a BuildKit clone               | v1 explicitly drops rootless, registry cache, `FROM DOCKERFILE`. Written into `--engine` help text.                                                               |
| PAX sub-second headers upset a consumer         | PAX `mtime` is standard and ignorable by readers that do not want it; the round-trip test in §2c pins our own behaviour, and published images are clamped anyway. |
| Nanosecond fidelity silently regresses          | The §2c round-trip test plus the "rebuild recompiles nothing" test run in the dual-engine CI matrix, including through a remote worker.                           |

## Order of work, one line each

1. Phase 0 harness and report, including the incremental-rebuild baseline. Decide.
2. `engine` interfaces + `bkengine` + call-site migration. Ship.
3. **M1: `FROM alpine` + `RUN` + `SAVE ARTIFACT`, end to end on the native engine.** Then M2,
   M3, ... - each a working build of a larger Earthfile, each with its test in the ratchet.
4. Nanosecond-preserving diff writer (**done** in the containerd fork) + the round-trip and
   no-recompile tests, once there is a native snapshot to apply into.
5. Dual-engine CI matrix to parity.
6. Mesh, driver, worker, action. Ship experimental.

Note on ordering: the writer half of step 4 was cheap enough to do up front, and the
truncation it fixes is upstream of everything the fleet is supposed to make faster. Its
remaining half cannot land before step 3, since it needs a native snapshot to apply into.

**An artifact's name is not a path, and half the lookup had forgotten, 2026-08-15.** The first
genuine engine failure off the E26 list: `Earthfile:12: COPY /dist: nothing in that target has it`,
on `examples/tutorial/js/part2` and its siblings.

```earthfile
build:
    SAVE ARTIFACT index.js /dist/index.js   # at /js-example/index.js, called /dist/index.js
docker:
    COPY +build/dist dist                   # a *directory* in the artifact namespace
```

`SAVE ARTIFACT <path> <name>` gives a file a name in a namespace of the target's own making. Nothing
of that name exists in any layer - the file is at `/js-example/index.js`. The lookup matched only on
where the file *is*, so both of these resolved to nothing and were passed through to the guest as
paths, which then reported a directory the Earthfile does mention as missing, in the consuming
target, two steps from the line that decided it:

* `+build/dist` - a directory in the namespace, holding everything saved below it
* `+build/dist/index.js` - the file, by the name its author gave it

Both fixed. `savedUnder` expands a namespace directory the way `+target/*` is already expanded -
in the interpreter, so each entry is its own copy in the plan and the key covers exactly what was
taken - and each keeps its path below the name asked for, so `COPY +build/dist out` puts
`/dist/index.js` at `out/index.js`. `savedAt` now matches an artifact's name as well as its path.

Verified end to end, not only in the plan: a target that copies the directory and `cat`s the file
out of it gets the contents.

**And the flaky `race (short)` finally reproduced, and it was three bugs, 2026-08-15.** It has been
in the nits file with two unreproducible sightings. It fired during this iteration's gate, in
`recordingExec` - a test double keeping a map of what each step was handed, called from the
scheduler's concurrent workers with no lock. It needs two steps ready at the same instant, which the
ordering usually prevents, which is exactly why it appeared at random.

Two siblings had the same defect and had never fired: `observingExec.runs` (an increment) and
`triedExec.ran` (an append). All three now hold a mutex. Fixing only the one that fired would have
left two landmines and a nits entry that looked closed.

**Two messages made to name something the reader can act on, 2026-08-15.**

The first is the 90-second wait behind every `WITH DOCKER` failure - 5 of the 8 remaining corpus
failures (E28), across four Earthfiles, and unattributed because
`the sandbox has no /usr/local/bin/docker to give this step` is the same sentence whether the image
has no docker in it, nothing was mounted, or the store was still being unpacked when the timer ran
out. Three faults, three remedies, one message. It now says what it found instead: whether the
directory exists and what else is in it, or how far up the tree the first existing directory is.

`explainMissing` lives apart from the waiting, in a file with no build tag, because the waiting is
Linux-only and a message is worth checking on the machine the developer is sitting at. Bounded
deliberately - a directory of four hundred entries listed in full is the reason people stop reading
errors - and sorted, so the same directory reads the same way twice.

**It has not been seen in anger**, because the failure itself has never reproduced outside a full
sweep. That is the point of doing it now: the next sweep explains itself instead of producing five
more identical sentences.

The second was a regression of my own, from the day before. The case-collision refusal was changed
to name the directory that could not hold both paths, which sounded like an improvement and read
like this:

```text
... differ only in case, and /...//imagecache/.pulling-3744278413/usr/lib/xtables cannot hold both
```

A staging directory that is deleted before anyone reads the message, inside a path nobody chose.
True, precise, and impossible to act on - the same fault as "the build cache" the day before, in the
opposite direction.

The layering was wrong rather than the wording. `Unpack` is handed a directory to write into and has
no idea what that directory *is*; the caller does. So the refusal states the fault and no path, and
`fetchImageFrom` adds `while filling the image cache at <path>` - which is the directory the reader
can move to a case-sensitive volume, and the one the next line tells them to move.

Three attempts at one sentence in two days is worth recording as its own lesson: **when a message
keeps naming the wrong thing, the fix is usually not a better noun but a different layer.**

**The corpus to build against is this repository, 2026-08-15.** The tutorial corpus stopped moving
at 118 of 129 and was read as saturated. It was not saturated, it was small: pointing `-dry-run` at
this repository's own Earthfile - 32 targets, the tool building itself - found three engine defects
in the first hour, none of them reachable from a tutorial (E48). A substituted command took the
build's output as well as its own; `--dir` was wrong in both directions at once, with a second bug
compensating for the first; and an artifact was the last step's share of a directory rather than
the directory.

All ten sampled targets now plan, `+all-binaries` among them at 115 steps. Planning is not
building, and the next step is the obvious one: run them. `+go`, `+deps` and `+code` are the
thinnest chain that ends in a real Go build, and each is a milestone in the M-series sense - a
working build of a larger Earthfile, with its test in the ratchet.

What this changes about the plan: **the ratchet's corpus is this repository first and the tutorials
second.** Tutorials measure the constructs tutorials use. A build tool that cannot build its own
repository has a more interesting number to report than 118 of 129.

**M-final, early: the engine builds the engine, 2026-08-15.** `earth-native` compiles the `earthly`
binary from this repository's own `+earthly` target - a real Go build of the whole tool, in 32
seconds, landing at `build/linux/arm64/earthly` where the reference puts it (E49). `+go`, `+deps`
and `+code` build too.

That is the milestone step 3 of the order of work was aiming at, reached from the other end: not a
larger toy Earthfile but the real one. It cost three defects, and the shape of all three is the same:
**a limit, a value, or a rule that was never checked against the thing that decides it**:

* `MaxStackDepth` is 480 because overlayfs stops at 500. The binding limit is the mount option
  page, which stopped this build at 41. Both are now measured, and the short-name farm moves the
  ceiling to about 90 - it moves the cliff rather than removing it, and the mount now refuses
  over-long stacks with a message that says to flatten.

* The built-in platform arguments were absent. Their values came from the reference, including the
  fact that they are opt-in.

* An `ARG` default naming another argument was never expanded, and an undeclared name in an
  `AS LOCAL` destination should vanish rather than survive.

**Φ now runs, and did not work when it first did, 2026-08-15.** The threshold is the mount's rather
than overlayfs's (`MountableStackDepth`, 64 against a measured ceiling of about 90), so a deep build
flattens instead of failing. The first build that reached it lost its base: the scheduler named a
squashed layer that nothing had built, and the mount answered with an empty directory (E50). The
executor now merges the range with hard links behind a `core.Squasher` port, and a 72-step target
builds and keeps the file its *first* step wrote.

Two limits, measured, and the scheduler is told which applies: 480 is overlayfs's wall (E11), 64 is
the mount option page (E49). The specification said only the first.

After that, the rest of this repository's targets: `+unit-test` runs the test suite, `+lint` runs
golangci-lint, and each is a larger Earthfile than anything in the tutorial corpus.

**The repository's own targets have never seen the engine, 2026-08-15.** `+code` copies a
hand-written list of directories into the image every other target builds from, and `engine` was
not on it. So `+lint`, `+unit-test` and every binary target have been green about a tree that does
not contain this effort (E51). One line in the Earthfile, and a test that reads the list so the
next directory cannot be forgotten the same way.

With the engine visible, `+lint` reports 1,415 findings against it and none anywhere else in the
repository. The correctness classes are fixed - `unused` and `staticcheck` are at zero, which cost
five deletions, one file split and five assertions rewritten to compare two values rather than one
value with itself. The remaining ~1,380 are stylistic, mechanical, and tracked in the nits file
rather than started: they want a dedicated pass, and the first question of that pass is a
repository-wide policy one (whether `goconst` and `paralleltest` should apply to `_test.go` at
all), which could remove most of the work rather than doing it.

**Every target of this repository has now run on the native engine, 2026-08-15.** `+unit-test`
exits 0: 97 packages, this engine's own suite, inside the sandbox this engine provides.

Getting there cost one production bug and three environmental guards (E52). The bug is the one
worth carrying forward: preparing a bind mount created its target *by opening it*, so a second
concurrent step opened whatever the first had bound - `/dev/tty`, which is ENXIO wherever there is
no controlling terminal. **That is every container, every CI runner and every daemon**, and none of
the machines this was developed on. It would have shipped.

What remains before this branch can be green in CI is `+lint`, which is the ~1,380 stylistic
findings in the nits file rather than anything about the engine's behaviour.

**A build can now be stopped, 2026-08-15.** `ExecStream` took a context and dropped it, so nothing
could be interrupted while a step ran - measured at thirty seconds' wait for a step cancelled after
150 milliseconds (E56). Fixed with a protocol message rather than a local `select`, because
abandoning the wait alone leaves a step running in a sandbox the host has stopped tracking. Protocol
Version 8.

The sandbox *boot* remains deliberately uncancellable, and that decision is now written at the call
site: it happens once behind a `sync.Once` and serves every step, so one caller's deadline must not
take it away from the rest.

**Corpus: 119 of 129, and nothing in the remainder is the engine, 2026-08-16.** Re-measured after
a dozen fixes (E60). Three failures, each read rather than counted: two are one tutorial whose
module requires nothing while its source imports logrus, and one greps its own output for a
sentence. Seven targets need an image with no arm64 manifest or credentials this machine has not
got.

**The corpus has been saturated for two sessions and should stop being the headline.** Nearly every
defect found since it plateaued came from this repository's own Earthfile, which exercises
constructs no tutorial does - fourteen directories copied in three steps and saved as one artifact,
`ARG GOOS=$TARGETOS`, a 41-layer stack. The ratchet's corpus is this repository first (see the test
plan) and the tutorials second, and the numbers should be reported in that order.

**Lint, honestly counted: 522 findings, 335 of them in tests, 2026-08-16.** Every category that
could hide a defect has been triaged to zero or accounted for (E59); what remains is style. The
single decision that would clear 205 of them is whether `goconst` should apply to `_test.go` at
all - its advice there is to name `test`, `true` and `make`, and a constant named for its own value
is worse than the literal it replaces.

That is a repository-wide call and is left to a maintainer, with the numbers in the nits file.
Reconfiguring it from inside this branch is how E55 orphaned six directives in four other people's
files.

## Optional VM encapsulation on Linux

Asked for on 2026-08-15, and it is cheaper than it looks because the macOS work already paid for
the design.

Today the Linux backend runs steps in namespaces. That is one boundary and it is a shared kernel: a
kernel bug is an escape, and the thing on the other side is an Earthfile - which for a build tool is
routinely somebody else's code, fetched from somewhere else, running on a machine with credentials
on it. The macOS backend already has a second boundary, not for security but because Apple's
runtime only offers a VM. Making that a *choice* on Linux rather than a platform accident is the
proposal.

**The seam already exists and is four methods:**

```go
type Sandbox interface {
    Start(ctx) (Conn, error)
    Stop() error
    StoreDir() string
    Confines() bool
}
```

`earth-guestd` is unchanged: it already owns overlay assembly and process isolation *inside* a
guest, speaks its protocol over stdio, and reads layers from a store shared in over virtiofs. A
Linux VM backend implements those four methods and reuses the agent verbatim. That is the whole
reason this is worth doing now rather than later - the guest half is the expensive half, and it is
built.

**`Confines()` is already the right concept.** It exists because a sandbox that does not hold a
step's writes to its own layer must not produce cache entries (green paper A3): the key would be a
claim the sandbox cannot support. Encapsulation strength slots into the same idea rather than
introducing a new one - what changes is what an escape costs, not what the type means.

Candidates, with the trade-off that decides between them:

| Runtime          | Boot   | Note                                                             |
| ---------------- | ------ | ---------------------------------------------------------------- |
| Firecracker      | ~125ms | microVM, minimal device model; virtiofs needs a separate daemon  |
| Cloud Hypervisor | ~200ms | richer device model, virtiofs first-class                        |
| QEMU microvm     | ~300ms | available everywhere, slowest and largest                        |
| Kata Containers  | -      | an OCI runtime that does this transparently; a bigger dependency |

Not gVisor: it is a user-space kernel rather than a VM, which is a different threat model and a
different set of syscall compatibility problems. Worth its own assessment, not a substitute for this
one.

**Two things must be decided before this is scheduled**, and neither is technical:

* **The default.** Off, and the security-conscious opt in? Or on, and the fast path opt out? E1
  measured ~650ms per VM on macOS against ~65ms to exec into a running one, and a VM is per *worker*
  rather than per step - so the cost is small but not nothing, and it is paid by everyone.

* **What happens where nested virtualisation is unavailable.** Many CI runners cannot start a VM at
  all. A flag that silently falls back to namespaces gives a false assurance, which is worse than
  refusing; a flag that refuses makes builds fail on machines that were working. This wants the same
  treatment as `Confines()`: say what was actually used, and let the cache rule follow from it.

**Not scheduled.** Recorded here with its shape so that the next person to want it - or the first
person to run an untrusted Earthfile on a shared machine - does not start from nothing.

**A WITH DOCKER block leaves its containers running, and the next build inherits them, 2026-08-15.**

Found the moment the wrong-sandbox bug (E30) stopped hiding it. Two `typescript-node` targets fail
with:

```text
docker: Error response from daemon: driver failed programming external connectivity ...
  Bind for 0.0.0.0:8080 failed: port is already allocated
```

Nothing in those targets is wrong. A different target, earlier in the same sweep, ran
`docker run -d -p 8080:8080 app` inside its own WITH DOCKER block and never stopped it - and the
daemon is still there, because **the sandbox VM outlives the build**. That is the same reuse that
takes a rebuild from 700ms to 65ms, and here it carries a container into a build that never asked
for one.

`compose down` already runs at the end of a block, so a `--compose` block cleans up after itself. A
bare `docker run -d` does not, and it is the commoner of the two in the corpus.

**This is a determinism defect, not an inconvenience.** A build's result depends on which builds ran
before it on the same machine - the failure above is literally "a port another build took" - and
that is the class of thing the whole engine exists to remove. It also means a build can *pass*
because of a leftover, which is worse.

The shipping engine does not have this problem because it gives each WITH DOCKER block its own dind
container, so everything in it disappears when the block ends. This engine shares one long-lived VM
deliberately. Three ways out, and the choice is not obvious:

* Remove what the block started, at its end. Needs the containers attributed to the block, which
  means recording ids as they are created rather than asking the daemon afterwards - a `docker ps`
  at the end cannot tell a leftover from something a concurrent block is using.

* Give each block its own daemon inside the shared VM. Isolation without a second boot, but two
  daemons on one machine want separate data roots and separate sockets, and the port collision above
  would still happen because ports are the VM's, not the daemon's.

* Accept it and document it, as `docker run -d` in a shell script would behave. Cheapest, and wrong
  for the same reason accepting a shared cache key would be.

**Done the same day, by the first option.** The block records what was already running before its
body starts, and removes the difference afterwards - so a container another build is using is left
alone, which matters because the daemon is shared and removing something that is not ours would be
a worse fault than the one being fixed. Proven both ways: a block's own container is gone
afterwards, and a container started outside the block survives a build untouched.

**And the fix would have shipped doing nothing.** The removal is
`docker ps -aq | grep -vxF -f <before-list>`, and on a clean machine - the ordinary case - that list
is empty. GNU grep with an empty pattern file and `-v` matches every line; **busybox matches none**,
which is what the sandbox has:

```text
: > /tmp/empty; printf "aaa\nbbb\n" | grep -vxF -f /tmp/empty   ->   (nothing, exit 1)
```

So the cleanup ran, removed nothing, and left no trace of having done nothing. It was caught only
because the two-build check counted the containers left behind rather than reading "run 2 ok". The
list is now seeded with `__none__`, which is never a container id under `-x`, so the pattern file is
never empty.

The general lesson is the one this session keeps relearning in new clothes: **a cleanup that cannot
fail is a cleanup that cannot report, so it has to be checked by its effect.** `; true` on the end
of that pipeline is right - a failed teardown must not fail a build that has produced its result -
and it means the only evidence is the state afterwards.

**The failure path is still open, and the cheap way out does not exist.** A block whose body *fails*
still leaves its containers, which is how the two `typescript-node` targets are broken by a
different target that failed before them. The IR has an `OnFailure` edge - the one CATCH uses - and
a second teardown guarded by it looked like the whole fix. It is not, for two reasons found by
trying it:

* **Two teardowns alike in everything but their guard are one node.** `OnFailure` is deliberately
  absent from a node's identity, because it decides *whether* a step runs and never what it
  computes. So both copies hashed the same, `Graph.Nodes()` folded them into one, and the plan came
  out with a single teardown wearing whichever guard it was built with. Making them distinct means
  making them differ in the key, for a difference that is not about what they compute.

* **A guarded teardown never runs anyway.** A handler runs because TRY *tolerates* the failure and
  lets the build continue; without that the scheduler stops at the failed step and nothing after it
  is reached. Checked rather than assumed - the guarded step was built, the body was made to fail,
  and neither teardown ran.

So the note beside `composeDown` was right the first time: closing this needs TRY's machinery and
TRY's error reporting. Reverted rather than left half-done, and recorded here so the next attempt
starts from the two facts above instead of from the same idea.

**Measured the next day, and TRY's machinery turns out to be nearly enough, 2026-08-15.** Setting
`Op.Tolerate` on the body's last step was tried against the failing Earthfile, and it does exactly
what is wanted:

* the teardown ran - `docker containers after` appears in the output
* the build still failed, with the original error, because the scheduler keeps a tolerated failure
  and returns it once everything that had to run has run

That is not an accident of the implementation, it is written down: *"A tolerated step is TRY: what
stands on it must still run, because FINALLY reads the filesystem this step left behind... The build
still fails - remembered here and returned once everything has run, so a red test suite cannot
report a green build."*

**What stops it being the fix is scope, not mechanism.** Tolerance has no end: everything downstream
of the tolerated step runs, which for a WITH DOCKER block includes every command *after* `END`. A
failed block would go on building the rest of the target, do work nobody wants, and probably fail
again somewhere less informative. Tolerating each body step in turn is worse - it would run the rest
of the body after a command that failed, which is not what a block means.

So what is missing is a way to say "tolerate this far and no further" - a teardown that runs during
unwinding rather than as another node downstream of the failure. That is a scheduler feature and a
green-paper question about what a step's failure means, not an interpreter change. Recorded with the
measurement so the next attempt starts from "the mechanism works, the scoping does not".

**`gosec` splits the way the other lints did, and one finding was worth having, 2026-08-15.** 142
issues: **121 in test files**, 21 in production. The test ones are `G304` on a path the test itself
just wrote and `G204` on an argv the test itself just built - declined, on the same evidence-shaped
grounds as `goconst` and `fieldalignment`.

Of the 21, most are `G301`/`G302` on directory modes and already-assessed `//nolint`ed reads. One
was not:

```text
engine/exec/export.go:83:20: G703: Path traversal via taint analysis
```

Line 83 is the *write*, and the tainted value is `SAVE ARTIFACT ... AS LOCAL <dest>` - the one
command in the language that names a path on the machine running the build, in a file that is
routinely somebody else's code fetched from somewhere else.

The interpreter already refuses an absolute path, a `~`, and a destination that climbs out with
`..`, so gosec is not reporting a hole so much as an unguarded layer. That distinction matters less
than it sounds: this engine is a library, the interpreter is one caller, and the check that counts
is the one next to the damage. It is the arrangement `within()` already has in front of the git
fetcher's `RemoveAll`, and it was put there for exactly this reason.

`insideProject` now refuses at the write. It **resolves** rather than cleans, which is the part the
string checks cannot do:

| Destination                        | interpreter | at the write |
| ---------------------------------- | ----------- | ------------ |
| `../escaped.txt`                   | refused     | refused      |
| `/etc/passwd`                      | refused     | refused      |
| `sub/../../sibling/out`            | refused     | refused      |
| `dist/out.txt`, `dist` → elsewhere | **allowed** | refused      |

The last row is the case worth having. `dist/out.txt` is a relative path that does not climb, so
there is nothing wrong with the text - what is wrong is `dist`, and only the filesystem knows it.
The project directory is checked out from wherever the Earthfile came from, so whoever wrote the
Earthfile may also have written the symlink.

An empty context refuses nothing: a caller that has not said what "outside" means has not been
given an answer to invent.

## The refusal list, checked flag by flag

`--keep-ts` was refused while this engine did exactly what it asks (E34), which is the least
defensible kind of incompatibility: the build it turned away would have been correct. That was a
reason to check the rest of the list rather than trust it, and the checking is what this section
records - each answer measured against the reference rather than reasoned about.

| Flag                  | reference default       | reference with the flag | verdict                                 |
| --------------------- | ----------------------- | ----------------------- | --------------------------------------- |
| `--keep-ts`           | clamps to a fixed epoch | preserves               | **was wrongly refused**; now accepted   |
| `--keep-own`          | `0 0`                   | `65534 65534`           | **implemented**; refuses on macOS (E84) |
| `--symlink-no-follow` | follows the link        | carries the link        | **implemented** (E83)                   |

**`--keep-own` is correctly refused, and now that is a measurement.** A file `chown`ed to 65534 and
carried through an artifact arrives owned by root, in *both* engines - so the default agrees and the
flag is the thing this engine has not implemented. Pinned by an oracle case, which is the part worth
keeping: the agreement on the default is what makes the refusal a gap rather than a divergence.

**`--symlink-no-follow` was unresolved, and the probe was the reason.** The first attempt used a
symlink to a *file*, where following it and not following it put the same bytes in the same place -
so both answers looked identical and the row read *not established*. Re-asked with a symlink to a
**directory**, which E74 had just shown is where the difference becomes loud, it took two builds:

| `SAVE ARTIFACT`       | `COPY`                | what arrives                    |
| --------------------- | --------------------- | ------------------------------- |
| plain                 | plain                 | the tree                        |
| `--symlink-no-follow` | plain                 | the tree                        |
| `--symlink-no-follow` | `--symlink-no-follow` | the link, dangling in the image |

**The flag on the `COPY` is what decides**, which is why the documentation says it must be given in
both places. It is a real feature, this engine has not implemented it, and refusing it is correct -
now as a measurement rather than as a defensible guess.

Worth recording separately: `SAVE ARTIFACT --symlink-no-follow link AS LOCAL saved` makes the
*reference* fail, at the export rather than the build, with `stat .../link: no such file or
directory`. It carried a link whose target it had not carried. Not this engine's defect, and not
something to copy.

**The lesson is about the instrument, not the flag.** An experiment that cannot distinguish its two
hypotheses returns *not established* - and in a table, *not established* is indistinguishable from
*no difference*. The first probe was underpowered, and the row recorded the underpowering as though
it were a property of the flag. What fixed it was not more reasoning but a fixture in which the two
answers cannot look alike.

The remaining refusals - `--chmod`, `--chown`, `--from`, `--force`, `--allow-privileged` - change
what is copied or how, by their own description, and no measurement is needed to say that accepting
them silently would be wrong.

**The general rule this leaves behind:** a refusal list is a set of claims about what this engine
does not do, and a claim that goes unchecked can be wrong in the expensive direction. Refusing
something already implemented costs a user a working build; accepting something not implemented
costs them a wrong one. The first is the failure that happened.

## What a symlink means, decided by measurement

A nit filed the day before had the defect right and the reason for leaving it exactly backwards. It
said `COPY` of a symlink to a directory copies the link, that no corpus target does it, and that the
open question was which semantics are wanted - Docker carries a build context's symlinks as links,
and an artifact is not a build context.

**That question had an oracle sitting on this machine.** One ninety-second build settled it: the
reference dereferences, and from a build context it fails with `"/real": not found` rather than
carrying a link whose target was not transferred. It follows links on purpose, in both directions.

The pattern is now three for three - `--keep-ts` (E34), `--keep-own`, and this one. A behaviour that
cannot be reasoned out from documentation can usually be *asked*, and the answer arrives in less
time than one round of arguing about it. The standing rule this leaves: **when a decision is
recorded as open because the sources disagree, check whether the reference is one of the sources
that can be asked directly.**

### The half that was not a style question

The failing test was written first, with a case for each way the copy could be wrong. Four went red
as expected. The fifth was not in the nit:

```text
the copy followed an absolute link onto the host and took a file with it
```

`ln -s /opt/app link` inside a layer names *that layer's* `/opt/app`. The guest resolves with the
host's filesystem, so it named the guest's, and the contents came into the image. A step reaching
outside itself is A3, and it was filed under a heading about a cosmetic difference with the note
*"not urgent - no corpus target does it"*.

**"No corpus target does it" bounds the nuisance and says nothing about the hole**, because the
corpus is a set of builds written by people who were not trying. This is the second time a triage
note has been right about the symptom and wrong about the severity, and the thing that caught it
both times was writing the test before the fix - a test asks what else this code does, where a fix
only asks what it should do instead.

### The information that was missing from the call

Re-rooting a link needs the root, and `findInStack` returned bare paths. **A symlink's text is
meaningless without the place it is relative to**, so every caller resolved against the guest's own
filesystem for want of anything better to resolve against. It now returns `layerPath{root, path}`.

The failure class is worth naming: the function had all the information and returned half of it, and
the half it dropped was the half that made the other half safe. Nothing about the signature looked
wrong - a path is a plausible thing to return - which is why it survived three rewrites of the code
around it.

## Closing a [GAP] by reading the code it was about

`green-paper.md` §5.1 is the table that says, per invariant, what enforces it and what tests it. I9's
row read *"store panics on rewrite of an existing key"* at level 3, tested by **[GAP]**.

Both columns were wrong, in opposite directions. Nothing panicked, so the enforcement column
described a mechanism that did not exist; and the blob store *did* enforce the invariant properly by
a different means, so the level was understated. The action cache, meanwhile, renamed straight over
whatever was there - the one place in the engine where state was modified in place.

**A [GAP] marker is an admission that nobody has checked, and it is worth reading as an instruction
rather than as a status.** This one had been sitting next to a defect for the life of the branch.

The row now reads *"insert-only stores: an existing entry is never rewritten"* at level 2, tested by
E76. Level 2 rather than 3 because it is part of the mechanism - `os.Link` refuses at the syscall -
rather than an assertion somebody remembered to write.

### Refusing is half a fix

A key determines a result by construction. Two layers under one key is a step that read the same
things and produced different output, which is I1 and exactly what §6's screening is for. Keeping
the first entry silently would honour I9 and destroy the finding: the step would miss the cache on
every build from then on, and no line anywhere would say why.

So conflicts are counted, recorded (capped at 32, with the remainder named), ordered by key so two
runs of the same broken build report the same list (I12), and printed after the per-step lines only
when there are any. The rule the last three iterations keep re-deriving: **a check that prevents a
fault without reporting it converts a loud failure into a quiet cost.**

### What the existing concurrency test was testing

`TestConcurrentWritersDoNotCorrupt` uses thirty-two distinct keys. Thirty-two goroutines, thirty-two
files, no contention on any of them - it exercises the directory and not the entry. The one-key
version fails immediately on the shared `<key>.<pid>.tmp` name.

This is worth a general note, because the same shape will be elsewhere: **a concurrency test whose
workers do not contend is a test that the hazard is absent.** It passes forever, it looks like
coverage in a list of test names, and the thing it is named after was never run.

## Finishing a feature means executing its consequence once

Three features on this branch were built correctly, tested per piece, and reached by nothing: the
artefact a `FINALLY` saved, the image a `SAVE IMAGE` named, and the scheduler's flatten dispatch.
Each was covered by a unit test of the part and by nothing at the seam. The conflict reporting added
in E76 was the fourth candidate and is the first one checked before it shipped.

**The rule this settles into: a feature is finished when something has executed it end to end, not
when every piece of it has a test.** The pieces are where the confidence comes from; the seam is
where the defects live, because a seam belongs to nobody - both sides are correct and there is no
call.

The check cost twenty minutes and immediately paid for itself twice. It established that the path is
reachable through the Κ₁ lookup after eviction, which is ordinary rather than exotic. And it
established, by failing first, that the **Κ₂ path is inert** - publication is gated on a profile
store the front end does not set, which is correct while S5 is simulated and is a filed nit for
whoever lands real capture.

Worth being precise about what the negative arm buys. Eviction is routine, so a check that reported
every re-run after it would warn about correct builds, and a warning that fires on healthy builds is
gone within a week. **The arm that proves a diagnostic stays quiet is the one that keeps it worth
printing.**

### A sweep that found nothing, recorded anyway

E76 ended on a generalisation - a concurrency test whose workers do not contend tests the absence of
the hazard - and the obvious follow-up was to look for that shape elsewhere. Four other concurrency
tests in the engine, and all four contend properly. The blob store's deliberately has half its
writers produce identical content so they collide on one path; the guest mux test asserts overlap
rather than merely permitting it.

The cache was an instance, not a pattern, and saying so is worth a paragraph: an unrecorded negative
sweep gets run again by the next person with the same idea.

## Optionality is what lets a port go unheld

Every port on `core.Scheduler` is optional by design, and the design says why: *"with no cache every
step executes, which is slower and never wrong"*. That tolerance is correct - it is what lets stages
S0 to S3 be real before S4 exists, and what keeps a missing dependency from becoming a wrong answer.

It is also exactly what lets a port stay unwired for the life of a branch with nothing to say so.
**A field whose absence is harmless is a field nothing will notice the absence of.** Three were found
in three iterations, each while looking for something else, and the third - `Stats` - was not even
an input: it is filled in by `Run` and read by nobody, which no amount of looking at the front end's
constructor would have shown.

So the accounting is now mechanical, in the shape `seam_test.go` established over `interp.Plan`:
reflect over the fields, and require each to be wired, read, or explained. The explanations are the
durable part - `Trusted` is nil because there is one writer in the cache and A5's "outside" arrives
with the fleet transport; `Materialiser` is nil because the VM executor owns the filesystem. Both
were decisions somebody made and neither was written anywhere a reader would find.

The guard is intolerant in both directions on purpose: a port declared inert that the front end
starts setting fails too, so a reason cannot go stale by being overtaken rather than by being wrong.

### What it cost to have kept the answer

`Stats` had been counting hits, misses, observed-key hits, stale predictions and Φ flattenings on
every build since the counters were written. "Did it use the cache" is the first question asked of
any build that took longer than expected, and every one of those builds had the answer in memory
when it exited.

The line now printed is one row under the per-step table, with the rare counters suppressed when
zero. That last rule is the same one the conflict warning follows and the same one E75 arrived at
for refusal messages: **a diagnostic that appears when there is nothing to say trains the reader to
stop seeing it**, and it is then absent from the build that needed it.

## Reading the refusals, not just counting them

The corpus has always reported two numbers: targets planned, and targets refused. The second splits
into work this engine has not done and claims that somebody else's Earthfile is wrong, and only the
first half has ever been treated as a work list. **The second half is a work list too, and a more
urgent one**, because a wrong refusal costs a user a build that would have succeeded while looking
exactly like a right one.

Reading all 84 causes took an hour and produced three different kinds of answer, which is the
argument for reading them rather than sampling:

* **Correct, and the engine could not say so.** Twelve `cycle` refusals were one fixture named
  `infinite-recursion`. The report printed the cause and no site, so there had been no way to check.

* **Ours.** A `--load` reference quoted after the `=` rather than around the whole value went to the
  target resolver with its opening quote attached and came back as an undeclared import alias.

* **Real, and neither engine's.** Five Earthfiles inherit from a `+base` with no `FROM`. The
  reference fails the same way. Filed as a nit against the repository.

### The report that asked for verification and withheld the evidence

The "unimplemented" list printed one example site per cause, with a comment explaining that a
construct name is not a place to go and look. The list headed "verify these are right" printed none.

That is worth naming as a shape rather than a bug: **the section of a report that most needs
evidence is the one where the author was most confident, and confidence is exactly what suppresses
the evidence.** The unimplemented list is a list of things known to be missing, so its sites are
courtesy. The refusal list is a list of assertions about other people's work, so its sites are the
whole case - and they were the ones omitted.

### The safe direction of an asymmetry

Two `VERSION` flags were refused that grant permissions this engine does not extend:
`--allow-without-earthly-labels` relaxes a check it does not make, and
`--allow-privileged-from-dockerfile` widens a door it keeps shut by name at every construct.

E34 established the asymmetry - refusing something already implemented costs a working build,
accepting something not implemented costs a wrong one. This is the case where the asymmetry says
*accept*: **an engine stricter than a permission can ignore the flag that grants it**, because the
refusal still happens at the point of use. Asserted rather than assumed, with a test that declares
the flag and then checks `RUN --privileged` is still refused by name.

Corpus: **478 to 485 targets planned**, 102 refusals down to 91, 84 causes down to 81.

## The build corpus, and what its number is worth

**22-24 of 24 attempted targets build**, from a cold store, on this machine. The range is the
measurement: five runs over an unchanged tree produced 22, 24, 22, 3-of-3, and 24, with the two
failures being `npm install` and `apt update` failing fast - two seconds against a six-second
success for the same target run alone.

Quoting the last run as "24 of 24" would be the E73 mistake again: a figure that moves while nothing
changes has an error bar, and the honest form of it includes the bar. 105 of the 129 buildable
targets were not attempted at all, which is the larger caveat and the reason the cap exists.

### What actually needed fixing was the instrument

The failures reported no diagnosis at all - a step name, an exit code, and "its output is above",
where "above" was a buffer the harness never printed. That is E73's fix arriving in a second caller
where its premise does not hold, and it had made every build-corpus failure since then unreadable.

**The failure class is worth the name it gets in E80: a decision verified against one caller and
shipped to two.** The engine streams a failing step's output to the sink it was given, and points at
it rather than repeating it. For the front end that sink is a terminal, and the message is right.
For a harness collecting into a buffer, the same message is an instruction to look at nothing. The
engine was not wrong; the second caller was never asked.

That is the third time this branch has shipped a correct change into a caller nobody checked - the
artefact a FINALLY saved, the image a SAVE IMAGE named, and now this. The pattern is stable enough
to plan against: **when a change alters what an error carries, enumerate the things that read
errors**, not the things that raise them.

### The classifier that is deliberately absent

`cannotHere` sorts a failure this *machine* cannot do from one the engine got wrong, so the number
stops moving for reasons nobody can act on. A transient network failure is a third thing and
currently lands in the second bucket, which is what makes the figure swing by two.

Adding a bucket for it needs the output of a real one, and the failures stopped recurring the moment
the instrument was fixed. Writing `ECONNRESET|Temporary failure resolving` from memory would produce
a classifier fitted to a guess - and one that then quietly absorbs the first genuine networking
defect the engine has. It waits for evidence.

## Ask the question of values, not only of ports

E78 guarded the fields of `core.Scheduler`: wired, read, or explained. The same question aimed at a
*value* found something worse within an hour. `core.Result.Content` is computed by the guest,
carried by the protocol, returned by both of the executor's capture paths, held by `core.Result` -
and read by nothing at all.

Four layers of plumbing, each of which looks correct in isolation, ending nowhere. **A field is
easier to leave unread than a port, because every layer that passes it along is evidence that
somebody meant it.**

### It was not merely unused

The conflict check added two iterations earlier compared `Layer`, which is the digest `Content`
exists to replace: a layer's identity includes its timestamps, so two runs of one deterministic step
produce two layers. Measured - the same step built twice from cold stores gives `d599575a…` and
`679c4e36…`, base image identical.

So the one diagnostic this engine has for non-determinism would have fired on **every re-run after
eviction of any step that creates a directory**, and been ignored inside a week. A check that
over-reports is not a weaker version of a check; past a threshold it is the absence of one, and it
costs the credibility of whatever it is printed beside.

The fix's own premise was then measured rather than assumed - `content` is byte-identical across the
two runs where `layer` is not - because swapping one unstable digest for another would have looked
exactly as convincing.

### What this is and is not

I1's enforcement row in green paper §5.1 is unchanged and still reads *sampled determinism screening
(§6)*, which this is not: it catches non-determinism only where a key happens to be claimed twice.

But it is the first thing that has ever read a digest four components conspired to produce, and the
question that found it is cheap and repeatable: **for each field of each value crossing a layer
boundary, who reads it?** The port guard answers that mechanically for one struct. Nothing answers
it for the rest, and `Content` suggests the rest is where to look next.

## "Why did this rebuild" now has an answer

`Diverge` is green paper B.4, listed under S0 in the stage table as **real**, and it answers the four
questions every build system is asked: why did this rebuild, is this step deterministic, why does it
work locally and not in CI, which change broke it. It reads the component digests - base, command,
environment, platform - that `StepRecord` carries for exactly that purpose.

It had no caller, and could not have had one. A record was assembled every build, three of its
nineteen fields were printed, and it was dropped at exit. **There has never been a second record.**

The record is now written to the store beside the layers it describes, and the next build of the
same target compares against it:

```text
  since the last build of this target:
    RUN echo one > /a.txt  the command changed
      at Earthfile:5
```

Silent on a first build and on an unchanged one, which is the rule the cache summary and the
conflict warning already follow.

### The stage table's "real" was doing two jobs

S0 says *real - Κ₁, Κ₂, Φ, Λ, records, first-divergence reporting*, and the column is defined as
"not simulated, not stubbed, and exercised by the same conformance suite the simulator passes".
Every one of those is true of `Diverge`: it is implemented, tested, and correct.

**And it was unreachable.** The word the table was missing is not *implemented* but *reached*, and
the two came apart without the table being wrong. Worth a note here rather than a change there: the
same reading applies to Κ₂, which is real and inert until S5, and it is better for that to be a
sentence somebody can check than a column somebody has to interpret.

### A source guard follows its seam upward

The guard for this asked whether anything called `core.Diverge`, and went green the moment a helper
in the same package did - while the helper itself had no caller. The seam had moved up one level and
the guard moved with it.

**This is the standing limit of source-level checks**: they prove a call exists somewhere, and
somewhere acquires a new floor every time a function is extracted. They are worth having anyway -
three defects this branch found were exactly a missing call - but each one has to name the outermost
function and the file it must not be satisfied by, and each is paired with a behavioural test that
proves a build reaches it. Here that is two real builds with a changed command between them.

## A refusal removed, three measurements later

`--symlink-no-follow` is implemented. It is worth recording how long the road was, because none of it
was implementation:

* **E74** established that a copy follows a link by default - by asking the reference, after a nit
  had sat for a day saying the semantics were unknowable because Docker was not a clean guide.

* **E75** re-measured the flag itself with a probe that could distinguish its two answers, and found
  that the `COPY` side carries the meaning. The earlier probe used a link to a *file*, where
  following and not following put the same bytes in the same place.

* **E79** set the rule for accepting a flag an engine already satisfies.
* **E83** wrote the code, which took an afternoon and produced two mistakes that the existing guards
  caught within minutes of each other.

**The implementation was the cheap part and it was last.** That ordering is the point: a refusal
removed on a guess is a wrong build, and every one of those steps was a measurement rather than an
argument.

### What the guards earned this time

The key-coverage test caught `NoFollow` reaching node identity and not Κ₁ - the field in one of two
derivations over one struct, which `key.go` records having cost a real build once before. It is the
only guard on this branch to have caught the same class twice, and it costs one reflection loop.

The differential caught a design error that no unit test could: refusing the flag on `SAVE ARTIFACT`
was defensible in isolation and made the only cross-engine-compatible spelling unbuildable here. **A
test written per engine agrees with itself forever.**

### Two structs where a bool would have gone

`copyArgs` had six return values and `copyIn` one trailing bool; both needed one more. Two adjacent
booleans transpose without a compiler error and produce a build that copies the wrong thing, so both
became named fields.

The guest's `copyOpts` has its zero value equal to what the engine already did. The inverse
spelling, `Follow bool`, would have made every unconverted call site silently stop following links. **When
a new option's default is "as before", the field has to be named for the *change*, not for the
behaviour.**

## A shared layer store cannot carry POSIX ownership

`--keep-own` is implemented and **cannot work on a macOS host**, and the second half is the finding.

The layer store is a host directory shared into the sandbox. That is E1b's decision and a good one:
a running VM cannot have filesystems attached from outside, and the host reads artifacts straight
out of the store rather than through a second copy. The cost, measured here for the first time, is
that a share whose host filesystem has no uids of its own cannot carry them:

```text
Earthfile:6 | 65534 65534                              <- inside the step
-rw-r--r-- 1 501 20  .../layers/a3f11be.../w/d/f.txt   <- the same file, in the store
```

The reference does not hit it because its store lives inside its daemon's Linux volume. This is a
consequence of an architectural choice rather than a defect in either engine, and it is the first
time that choice has cost anything visible.

**Green paper A2 already governs it** - where the host filesystem does not preserve the metadata
§3.3 enumerates, results stay correct and *the engine must say so rather than silently degrade*. So
the copy probes the store once and refuses, naming the reason and the configuration where the flag
does work. Silently degrading would put root-owned files in an image whose author asked for 65534,
and that failure appears at runtime, in a container, with nothing in the build log.

### What this implies for the fleet

S6 has not started, and this is a fact it will need: **a worker's store must be on a filesystem with
real uids for `--keep-own` to mean anything**, which makes it a property of a worker rather than of
a build. A fleet that mixed hosts would produce artefacts whose ownership depended on which worker
ran the step - a divergence I1 would not catch, because the key would be identical and the
difference is in ground the key does not describe.

Recorded here rather than in the specification because it is a deployment constraint, not a
mechanism. If it becomes a *stated* requirement on workers it belongs in §5 with a number.

### The differential learned a new shape

A declared divergence used to mean "the two engines produce different bytes". This one is "the
reference produces something and this engine refuses", which the harness treated as a hard failure -
the refusal counted as the fault rather than as the finding.

It is the more interesting of the two shapes, because it is I10 working: an engine that cannot do a
thing says so instead of approximating. The table records it now, and stops the moment the refusal
does.

## "Real" was doing two jobs, so the table now says which

The stage table's state column is defined as *"not simulated, not stubbed, and exercised by the same
conformance suite the simulator passes"*. Every S0 mechanism met that, and `Diverge` had no caller
for the life of the branch (E82). The word was carrying **implemented** and **reached** at once, and
they came apart without the sentence being wrong.

Three states, not two, and the third is the useful one:

| state       | meaning                                                             |
| ----------- | ------------------------------------------------------------------- |
| implemented | it exists, it is tested, it is correct                              |
| **reached** | a build with the default configuration calls it                     |
| **gated**   | it is called only behind a port nothing sets, and the port is named |

Κ₂ is *gated*: the publication and the lookup both sit behind `Profiles`, which the front end does
not set and should not while S5's observation source is simulated. That is a true and useful
statement, and it is not "real".

### Encoded rather than asserted

`engine/cli/reached_test.go` carries the S0 row as a table: per mechanism, the call and **the file on
a build's path that must contain it**. The first version asked instead whether anything outside the
defining package called it, and produced two false positives immediately - the scheduler calls Κ₁ and
Φ from inside `core`, which is exactly the path a build takes. *Where* a mechanism is called from is
not the question; whether the caller is on the path is, and the only honest way to say that is to
name the file and let it be wrong out loud.

It cross-checks the port table: a gated mechanism must name a `core.Scheduler` port that
`schedulerPorts` still declares inert. Two tables describing one fact drift, and then one is quietly
wrong - which is the shape that put "real" beside a mechanism nothing called. **The day somebody
wires up `Profiles`, this fails and says Κ₂ has woken up**: the good news arriving as a red test
rather than as nothing at all.

Both guards were checked against a deliberate break before being believed - the divergence row
pointed at a file that does not call it, and the port table flipped to a non-inert role. Each turned
exactly one test red.

## Crash safety, and the verification a layer does not get

Test-plan c4's tractable half is done: a build is killed with SIGKILL once the store has committed
two layers, and the next build of the same target completes and produces the artifact. A subprocess
rather than a goroutine, because the property is what an ungraceful stop leaves behind and a
cancelled context is the graceful path.

It passed on the first run. The store's writes were already atomic - a blob is written to a
temporary and renamed, a cache entry is linked into place, a layer is committed by rename - so this
is a verification rather than a repair, which is worth stating as such.

**What it also turned up is a gap in I2's reach.** *"Every blob is verified against its digest before
use"* is true and enforced: `blob.Get` re-hashes on every read and rejects a mismatch. A **layer** is
a directory rather than a blob, nothing re-digests one, and a layer corrupted on disk after it was
written would be used without complaint.

That is not a hole a crash opens - the crash test found it by accident, trying to assert the property
and discovering it does not hold even on a clean store. Filed with the measurement; the cause is not
established and the fix is not the obvious one, because making the digest reproducible from the store
invalidates every existing cache entry for a property nothing currently relies on.

### The control is the cheap half of every finding

The strengthened assertion went red on two layers and the first reading was "a crash left partial
layers". The second was "the assertion is wrong". One command told them apart: re-digest a store from
a build that never crashed, and watch every layer disagree with its own name.

**An assertion that fails is not yet a finding.** It is a disagreement between code and assertion,
and which of the two is wrong is a separate question with its own experiment. This branch has had it
both ways within a week - E74's absolute-symlink escape was real and this one was not - and nothing
about how convincing either looked distinguished them. Running the control did.

## One hop of the digest, established

E86 left a stored layer's identity unverifiable and the cause unknown. Bisecting it in a unit test -
a capture, a `commit`, a second capture - took one run and named the hop: `commit` copies the delta
rather than renaming it, because the delta is the upper directory of a live overlay mount, and
`copyTree`'s directory branch returned before restoring the mtime. Every directory in a stored layer
took the wall clock instead.

`copyTree`'s own doc comment says what that costs, which is the part worth keeping: *"a copy that
reset them would produce a layer whose digest does not match the one just computed."* **A function
can document an invariant and break it in one of its own branches**, and no behavioural test in this
tree noticed for the life of the branch, because nothing re-digests a stored layer - which is the
gap E86 found and this does not close.

### What it did not fix

The obvious follow-on claim was that this also explains why two cold builds of one deterministic step
produce two layer digests (E81). It does not - measured, they still differ, because the directory the
step creates is stamped with the wall clock *inside the step*, before any copy. Two defects with one
signature, and fixing the second does not touch the first.

Nor is the end-to-end property restored: a clean store still holds layers that do not re-digest, and
the base image layer differs too **without ever passing through `commit`**. That rules the image
unpack path *in*, which is more than the previous iteration could say.

### The correction

E86 recorded the timestamp hypothesis as disproved, on the grounds that the without-times digest did
not match the stored name either. That is not a comparison - the name is the with-times digest, and
the two were never going to be equal. The label *not established* was right and the reasoning under
it was wrong, which is the worse of the two failures: a wrong conclusion invites a check and a right
conclusion reached wrongly does not.

## Deletions did not survive the layer store

`RUN rm /x` had no effect on anything downstream. The delta of a step that deletes something contains
an overlayfs whiteout - a character device - and `copyTree`'s last branch skipped devices, with a
comment saying they "rarely appear in a delta". They appear in the delta of every step that cleans up
after itself.

Every layer this engine has ever stored claimed that nothing was deleted. `rm -rf /var/cache/apk/*`
is the shape it appears in, which is most Earthfiles anybody writes, and the symptom is an image
larger than it should be with files in it the author removed - reported as a successful build.

**It was found by following a digest that did not reproduce**, which is the argument for having
chased it. A content-addressed store whose contents do not hash to their own names has lost
something; two iterations asking why turned that from a curiosity into the most consequential defect
on this branch.

### The fix is blocked by the shared store, again

`mknod` into the store returns `EPERM`: it is a host directory shared into the sandbox, and a macOS
host has no device nodes to share. **The same architectural choice that cannot carry uids (E84),
four experiments later, at a second cost.**

That is now a pattern rather than an incident, and it should be recorded as one: the shared store is
a *Linux-filesystem* interface, and every POSIX feature a layer needs - ownership, device nodes, and
whatever the next one turns out to be - is unavailable when the host is not Linux. The engine refuses
each as it finds it, which is correct and is not a plan.

**The decision this needs from a maintainer** is which of three:

* a portable whiteout representation in the store (`.wh.` marker files, as OCI tar layers already
  use) plus materialiser support to turn them back into device nodes in a VM-local directory;

* a VM-local store with an explicit export step, giving up the property that the host can read
  artefacts straight out of it (E1b's reason for the current shape);

* macOS remains a host that can build anything that does not delete, and says so.

The third is what ships today, by accident until this iteration and deliberately now.

## The digest question, closed after three iterations

A stored layer did not hash to the name it was filed under. Three causes:

| cause                                     | status                                  |
| ----------------------------------------- | --------------------------------------- |
| directory mtimes not restored on commit   | fixed (E87)                             |
| hard links flattened on commit            | fixed (E89)                             |
| ownership not carried by the shared store | E84's wall - impossible on a macOS host |

The second is the same shape as the first and as E88's whiteouts: `layer.Take` records inode
identity and says why, `copyTree` copied each regular file independently, and `alpine`'s `/bin` -
one busybox under several hundred hardlinked names - became several hundred copies. **Three times
now the documentation of a property and the code implementing it have been maintained by different
people at different times, and one of them was not reading the other.**

The third is not a defect. A layer's digest covers uid and gid; the guest captures a delta as root;
the store is a host directory shared into the sandbox and macOS maps everything written through it
to the invoking user. On a Linux host all three are addressed and a stored layer re-digests. Here it
cannot, and that is a property to state rather than a bug to chase.

### What the three iterations were actually worth

The intermediate finding was worth more than the answer. Chasing a digest that would not reproduce
is what found that **`rm` did nothing** (E88) - every layer this engine had stored claimed nothing
was deleted. A content-addressed store whose contents do not hash to their own names has lost
something, and the only way to learn *what* was to keep asking after it stopped feeling productive.

The method that closed it is the reusable part: after two fixes the digest still moved, and instead
of guessing a third time, the **content digest the cache entry already records** separated
"timestamps" from "the tree" in one command with no instrumentation. The data needed to bisect a
question is often already on disk, written for another purpose.

## Four things the copy discarded, and the shape they share

| what                | how it was lost                                 | found in |
| ------------------- | ----------------------------------------------- | -------- |
| directory mtimes    | the walk's directory branch returned early      | E87      |
| whiteouts           | `default:` skipped devices as "rare"            | E88      |
| hard links          | every regular file copied independently         | E89      |
| extended attributes | carried for two names on directories, none else | E90      |

Every one was found by the same question - *what does this code discard?* - and every one had been
documented somewhere as a property the engine keeps. `copyTree`'s doc comment states the mtime
invariant it broke; `layer.Take`'s comment states the hardlink one; §3.3 lists xattrs. **The
description of a property and the code implementing it are maintained at different times by people
who are not reading each other**, and four for four is no longer a coincidence.

The recurring specific mistake is narrower than that and worth naming on its own: **a list where a
rule belongs.** Devices were skipped because they "rarely appear"; two xattr names were carried
because two were needed. Both are a general property replaced by the enumeration somebody could see
from where they stood, and both cost a silently wrong layer.

### The green paper says all four, and said them first

§3.3's list - mode, uid, gid, symlink target, xattrs, device numbers, hardlink identity - is exactly
the set. The specification was right and complete throughout, `layer.Take` implements it faithfully,
and the *copy* implemented a subset. A conformance test comparing what `Take` records against what a
copy reproduces would have found all four at once, and is the obvious thing to build next.

### A green tree with the code absent

`copyTree`'s symlink branch was meant to carry ownership from E84 and did not: a scripted edit whose
search text had the wrong indentation matched nothing and reported success, and the test that covers
it skips on a store that cannot carry ownership - which is every macOS host.

A silent no-op edit and a skipping test are each defensible alone. Together they are a green gate
over a missing feature, and the only thing that found it was reading the branch for an unrelated
reason. Two consequences taken here: every scripted edit asserts its match count, and where a
property cannot be checked on this host, a **different** test is written that can be - the new one
uses a secondary group, which macOS does allow.

## The conformance test, and what it cost not to have

`engine/guest/conformance_test.go` asserts one line: **what the digest records, the copy
reproduces.** Green paper §3.3 lists eight properties a layer carries; the test builds a fixture per
property and compares `layer.Take` of a tree against `layer.Take` of a copy of it.

It found a defect on its first run - a symlink's own mtime, which `os.Chtimes` cannot set because it
follows the link. That is five properties the copy did not reproduce, of the eight the specification
names.

**Four of them cost an iteration each.** Each was found by somebody noticing an odd digest and
following it, and one of those four turned out to be `rm` not working. The fifth cost one command.
The test is fifty lines and the specification sentence it checks has been there the whole time.

The lesson is not "write conformance tests", which everybody already agrees with. It is narrower and
more useful: **when a specification states a list of properties and two pieces of code implement it
independently, the cheapest test in the system is the one that compares them to each other.** Not
either against the specification - both against each other, which needs no oracle and no fixture
beyond one of each kind of file.

A second test names the properties §3.3 lists and fails if the table drifts from them, so a property
added to the specification and not to the test is red rather than silent.

### The guard had the fault it was written to catch

`lchtimes` is a second way to write an mtime, and the clamp guard greps for `os.Chtimes(`. A check
naming one function is the same shape as a `default:` branch skipping devices because they are rare -
in the check written to catch exactly that. Widened, and verified against a deliberate violation.

## The second implementation had the same three gaps

`copyTree` and `image/unpack.go` are two implementations of one idea - take a description of a layer
and write it to disk. E91 asked the first whether it reproduces what green paper §3.3 records. The
second had never been asked, and it writes **every base image**.

| property       | copyTree | unpack, before | unpack, now |
| -------------- | -------- | -------------- | ----------- |
| mode           | yes      | yes            | yes         |
| gid            | yes      | **no**         | yes         |
| symlink target | yes      | yes            | yes         |
| xattrs         | yes      | **no**         | yes         |
| special files  | yes      | **no**         | yes         |
| hardlink       | yes      | yes            | yes         |
| mtime          | yes      | yes            | yes         |

Every file of every base image was owned by whoever ran the build, and a `setcap` grant carried in a
PAX record reached the layer as an ordinary binary.

**The comment on the branch that dropped special files is the one from `copyTree` word for word** -
*"need privilege this may not have, and a base image rarely carries one. Skipped rather than failed,
and named here so the omission is deliberate."* One sentence containing two claims, only one of
which is true of a fifo, copied into two files and marked deliberate in both.

### Best effort is not the same as a silent skip, and the difference is worth stating

The three additions tolerate a *permission* failure. That resembles the pattern these experiments
keep removing, so the distinction has to be explicit:

* **a step's own work is never dropped** - a whiteout that cannot be written fails the build (E88),
  because the alternative is an image that silently still contains what the author deleted;

* **a property of somebody else's archive that this machine cannot reproduce is degraded, with the
  reason recorded** - an unpacked uid, because the alternative is refusing `alpine` on every
  unprivileged host.

Green paper A2 is the second case exactly. What is no longer tolerated in either is a failure that is
not about permission: those were silent too and are now errors naming the entry.

### Where this leaves ownership

Unpacking runs unprivileged on the invoking machine; the reference unpacks as root inside its daemon.
On a Linux host running as root the new code carries ownership faithfully. On a developer's machine
it cannot, and that is now the third place the same fact has surfaced - after `--keep-own` (E84) and
the layer digest (E89). It is a property of the deployment, not of the engine, and belongs in the
same decision as the shared store's other limits.

## Three implementations of one specification, and the test that compares them

| code              | what it does                  | properties it lost | found in |
| ----------------- | ----------------------------- | ------------------ | -------- |
| `guest/copyTree`  | delta into the layer store    | 5 of 8             | E87-E91  |
| `image/unpack.go` | a pulled image into the store | 3 of 8             | E92      |
| `image/pack.go`   | a layer into the tar it ships | 2 of 8             | E93      |

Green paper §3.3 has been right and complete throughout, and `layer.Take` implements it faithfully.
Three separate pieces of code implemented subsets, each with a comment saying the omission was
deliberate.

**The test that finds these compares two implementations to each other, not either to the
specification.** `Pack` and `Unpack` are inverses by construction, so composing them must be the
identity - one line, no oracle, no fixture beyond one of each kind of file, and it covers both
directions at once. That is the cheapest test in the system and it was the last one written.

### A reason that acquired a second caller

`Pack` serves `writeLayers`, which packs each **layer**, and `packimage`, which packs the OCI
**layout directory**. Its normalisation is argued from the second - *"a timestamp and an owner are
properties of the checkout, not of what was built"* - which is right for a directory of blobs this
engine just wrote and wrong for a layer, where ownership is what a `RUN chown` put there.

One function, two kinds of input, one set of rules argued from one of them. It is the same failure as
a comment copied between files, arriving by the other route: **the code did not move, the callers
did.**

Timestamps stay normalised, because two builds of one input must produce one image. Ownership is a
genuine trade-off between fidelity and cross-machine reproducibility, so it is now pinned by a test
that fails if somebody changes it and asks whether a fleet was considered - the question being easy
to answer for one machine and easy to forget for many.

### And a red test that was the fixture

Two of the round trip's three failures were macOS's `/var` → `/private/var` symlink tripping the
unpacker's escape check - correctly, against the test's own temporary directory.

After three iterations of finding real defects in this code, a red test here looks like a fourth. The
control is the one E86 needed and is the same one every time: **does it fail where nothing is
wrong?** It cost one line and would have cost an afternoon of chasing a defect that was not there.

## macOS can build an Earthfile that deletes something

The honest summary of this engine's completeness included the sentence *"usable on macOS for builds
that do not delete files"*, which is a strange thing to say about a build tool and was the largest
practical limitation it had.

E88 found the cause and left three options as a maintainer's decision. The decision turned out to
have been made already, in this repository, in the other direction: `image/whiteout.go` says that
writing the overlayfs form of a deletion *"needed CAP_MKNOD and CAP_SYS_ADMIN, which is why this
worked only on Linux and as root"*, and pulled images therefore use the `.wh.<name>` convention every
registry uses.

**Build layers now spell it the same way, and the materialiser translates.** A layer containing a
marker is copied onto VM-local storage - where `mknod` works - and the markers become the character
devices and opaque attributes overlayfs reads. Only layers with a deletion pay, and the translation
is remembered per layer.

### The measurement was written two iterations early

`TestAFileAStepDeletesStaysDeleted` was written in E88 and skipped on macOS with the reason. It now
passes; the test asserting the refusal is loud now skips instead. **A test moving from SKIP to PASS
is the whole result**, and neither test had to change to record it.

That is worth generalising: when a limitation is found, the test that will one day prove it fixed is
cheaper to write immediately - while the failure is in front of you - than to reconstruct later. Two
of this branch's skips are now doing that job.

### What remains of the shared store's costs

Two of the three are still there and both are now precisely stated: a layer's **ownership** is not
carried, so `--keep-own` refuses and a stored layer cannot re-digest on a macOS host. Those are one
fact with two faces and they need a store on a Linux filesystem, or a VM-local store with an explicit
export. Deletions are no longer on that list.

## Self-hosting, which is not the same as self-building

The engine has built this repository's `+earthly` target for some time - 81 steps, a Go toolchain,
caches, artifacts, a real `linux/arm64` ELF. What it had never built is **itself**: nothing in the
Earthfile named `earth-native` or `earth-guestd`, so the engine had never consumed its own output.

`+native-engine` builds both, and a test then runs an ordinary build using the `earth-guestd` that
came out. It passes.

**The distinction is load-bearing.** Every defect found in the last eight iterations produces a
plausible binary that does not work - a lost deletion, a flattened hardlink, a dropped capability -
and *not one of them would have failed a build*. A binary that exists proves the steps ran. A binary
that runs the next build proves the layers were right.

The probe deletes a file, deliberately: it is the longest chain in the system - a marker written at
commit, translated at materialise, read by the overlay - and a bootstrap test that built a binary and
ran `echo` would have passed throughout the eight iterations in which `rm` did nothing.

### What it does not yet establish

The binary is built for Linux and this machine is not, so what is verified here is that the *guest*
this engine produced can run the engine's builds. A full fixed point - `earth-native` rebuilding
`earth-native` and the two agreeing byte for byte - needs a Linux host, and is worth doing there
because it would also close the layer-digest question that a macOS store cannot answer (E89).

That is the next milestone worth naming, and it is now one target and one machine away rather than
an idea.

## The engine's suite now passes on Linux, which it had never been run on

E95 named a Linux host as the next milestone. The first thing to do there was not to build anything -
it was to run the tests, and that was the whole result:

```text
--- FAIL: TestExecReturnsTheExitCode
    mount /proc for the step: operation not permitted
```

Fourteen failures across two packages, every one of them uid 1000 without CAP_SYS_ADMIN. **Not one
was a defect.** On macOS these tests run inside a VM as root, so the engine's own suite had the fault
it has spent a fortnight removing from the engine: a check that fails where nothing is wrong.

They skip now, behind `guest.CanIsolate()` - a probe that *is* the operation, rather than a
capability list to get wrong or a `Getuid() == 0` that would refuse a machine granting CAP_SYS_ADMIN
to a normal user. Promoted to real API because two packages needed it and a rule written twice
drifts, and because the engine has a use for it: a step that cannot be confined is refused (A3), and
"operation not permitted" names no permission.

### What Linux settles

**All eight of green paper §3.3's properties are reproduced by a copy**, verified with real device
nodes, which a Mac cannot do. Five iterations were spent restoring those one at a time and this is
the first run that confirms the set.

Every package passes: `blob`, `cache`, `cli`, `core`, `exec`, `guest`, `image`, `interp`, `ir`,
`layer`, `mat/overlay`, `sim`.

### The cost of having only one machine

Mid-iteration the ownership probe was corrected for Linux and **broke macOS in the direction that
ships bad images** - it began allowing `--keep-own` on a store that discards ownership, so a build
would have delivered root-owned files and reported success. The differential caught it one commit
later.

The fix is a probe that tries a **uid** first and falls back to a group, because the two environments
fail in opposite directions: the guest is root and needs the uid question answered, an unprivileged
developer is not and needs the filesystem question answered.

**A probe that distinguishes two causes has to be tested against both**, and one of them existed only
on a machine this session had not used until today. That is an argument for the Linux box being part
of the loop rather than a milestone at the end of it.

## Rootless Linux is a stated gap, and the note above it was not

A build cannot run on an unprivileged Linux machine, and says so properly: the capability, the euid,
two remedies, and *"rootless operation is a known gap, not an oversight"*. That is the standard I10
asks for and there is nothing to do about it here - rootless needs a user-namespace path, which is a
milestone rather than a fix.

Printed above it was a note about a case-insensitive filesystem, on ext4. The probe writes into the
store, the store does not exist until a build creates it, the write failed with `ENOENT`, and the
function returned `false` - which its caller reads as *case-insensitive* rather than as *could not
tell*.

**A probe with two outcomes for three situations**, and the third rounded to whichever answer was
nearer. It is the same shape as an absent content digest read as agreement (E81), and it is now three
for three: every probe this branch has written wrong has been wrong by having too few outcomes.

Worth stating as a rule, since it keeps recurring: **a check that can fail to run needs a third
answer, and the caller has to handle it.** Silence is nearly always the right one - this note is now
printed only when the answer is known *and* bad.

### Reading it on a second platform is what found it

The remedy in that note is a `hdiutil` command, and `caseVolumeRecipe` has been platform-split from
the start, so it never printed on Linux. The half that had been thought about was fine; the half
nobody had questioned was the bug.

That is the argument for the Linux box being in the loop rather than a milestone: **the note had been
printed on every macOS build for weeks and read as correct, and one run on another machine made it
obviously wrong.**

## Rootless Linux works

"Rootless operation is a known gap, not an oversight" was the Linux blocker, and the first thing to
establish was whether it was a gap in this engine or in the kernel. Thirty seconds:

```text
unshare -Umr sh -c "mount -t overlay ... && rm m/a"
MOUNTED
c--------- 2 root root 0, 0 a
```

An unprivileged user mounted an overlay and `rm` wrote a whiteout device into it. **The capability
was there; only the implementation was missing** - which is worth knowing before writing any of it,
and is the difference between a milestone and a wish.

The mount happens in the guest, and the guest is a child this engine already spawns, so the user
namespace is part of spawning it - one `SysProcAttr`, no re-exec.

Four barriers, each a genuine rootless constraint and each moving the failure inward:

1. the availability check asked *who* rather than *where* - CAP_SYS_ADMIN is checked in the namespace
   the mount happens in;
2. procfs cannot be mounted for a PID namespace the caller does not own, so the guest needs one;
3. a read-only bind remount must carry the flags the mount already has, because a user namespace
   locks what its parent set and refuses a remount that clears them;
4. a test asserting the old refusal, which correctly said its claim had gone stale.

The result is a build on an unprivileged Linux machine, deletion and all. **A developer no longer
needs root to run this engine on Linux**, which was the single largest barrier to anyone trying it.

### What is still not established

E89 predicted a stored layer would re-digest to its name on Linux. It does not, and that is *not* a
fourth cause: the guest digests inside a namespace where it is uid 0, and the re-check reads from
outside where the same files are `1000 100`. The digest covers uid, so they cannot agree across that
boundary - the same field, in a third environment.

Answering it needs a **root** Linux host, and saying so is the honest position rather than adding a
cause that has not been demonstrated.

## Self-hosting closes, unprivileged

The bootstrap runs on an unprivileged Linux machine: 79 steps, a Go toolchain, cache mounts,
artifacts, and then the **engine it produced runs the next build with the guest it produced**.

That is the milestone E95 named and said needed a Linux host. On macOS only half of it is testable -
the binaries are cross-built for the VM's platform, so the guest can be exercised and the front end
cannot. Here the machine and the target are the same and both halves run.

It needs no privilege, which is what makes it worth having. A developer can clone this repository on
a Linux box, `go build`, and have the engine build itself.

### Two ordering mistakes worth remembering

The test asked whether the backend was available **before** building the guest it was about to
provide - and "cannot find earth-guestd" is one of that function's answers, so it skipped every time
and reported success. **A skip that fires on the setup the test performs two lines later is
indistinguishable from a machine that cannot run the test**, and the only thing that caught it was
reading the output rather than the exit code.

Then `t.TempDir` could not clean up: `go mod` makes its module cache read-only deliberately, the
build has a cache mount full of it, and every assertion passed before the cleanup failed the test.
The repair has to be registered *after* the TempDir it repairs, because cleanups run
last-registered-first.

Neither was an engine defect. Both looked like one.

### Where that leaves the stage table

S3 and S4 are real on both platforms now, and rootless on Linux. What remains untouched is S5 - the
observation source, which keeps Κ₂ gated - and S6, the fleet, which has no code. Those two are the
whole of what is left, and neither is a defect to find: they are features to build.

## S5: one candidate eliminated, a third one found

The stage table has said "FUSE or eBPF undecided" since the beginning. Rootless (E98) changed the
environment the answer depends on, so the candidates were **attempted** in the shape a step actually
runs in:

| mechanism                 | as the user | inside the engine's namespace |
| ------------------------- | ----------- | ----------------------------- |
| eBPF                      | EPERM       | **EPERM**                     |
| seccomp user notification | **works**   | **works**                     |
| FUSE                      | EPERM       | **works**                     |

**eBPF is out.** Program loading checks capabilities in the *initial* user namespace and a user
namespace grants none there, so it is unavailable by construction rather than by configuration. A
capture built on it would work for a root deployment and refuse the one a developer uses - and
rootless has just stopped being the special case.

**FUSE works exactly where the engine runs**, inside the namespace it already creates. The capability
arrives with the isolation rather than needing anything on top of it.

**seccomp user notification** was not on the list and works everywhere, with no privilege at all.

### What that does and does not settle

It eliminates one candidate on evidence and adds one, which is worth more than a paragraph of
reasoning about either. It does not choose: FUSE sees filesystem operations on the tree it serves,
which is what Ω is defined over (§4.7); seccomp sees syscalls, a wider and coarser net. That is a
design trade-off, and availability does not decide it.

The honest state is *narrowed*, and the table now says so rather than repeating a question that has
half an answer.

## The corpus on rootless Linux, and what it found in DO

Rootless Linux had built a three-step probe and the bootstrap - neither an Earthfile anybody wrote.
Sweeping `examples/`: **22 built, 11 did not**, most of the eleven environmental or correct refusals.

One was ours, and it was three hops from its symptom. `RUN --mount type=(none) is not supported`
named a construct that *is* supported, because the specification is
`--mount=$EARTHLY_RUST_CARGO_HOME_CACHE` and the variable was empty - set by `ENV` inside a
`FUNCTION` that `DO` had just discarded.

**`DO` inlines a function, and was throwing away half of what that means.** Both directions were
wrong and the second was found by fixing the first:

* what a function **sets** - ENV, WORKDIR, USER - now reaches the caller;
* what the caller has set is now visible **inside** the function.

Arguments still travel neither way, and that asymmetry is deliberate: an ARG is a function's
interface and is scoped to it, while ENV, WORKDIR and USER are properties of the filesystem being
built. Getting that boundary wrong in the other direction would make a function behave differently
depending on where it was called from.

This is not one example. **It is `earthly-lib`'s caching idiom** - rust, python and node all set
their cache mounts this way - so it is every Earthfile that caches through the published functions.

### Two wrong guesses, and why they were cheap

The first two hypotheses were the flag's spelling and whether flag values expand. Both were wrong and
both cost four minutes, because each arrived as a test rather than as a change. **A wrong hypothesis
that arrives as a passing test is cheap; the expensive kind arrives as an edit.**

### The next barrier

`cargo` now runs and fails with `Invalid cross-device link`. A cache mount is bound from the layer
store, the step's scratch is a different filesystem, and `rename()` does not cross devices - which
every tool that writes to a cache by renaming into it will hit.

That is a question about where a cache lives, not a defect in the mount, and it is the next thing to
decide rather than the next thing to patch.

## A divergence between this engine's own two platforms

`examples/rust` builds on macOS and fails on rootless Linux, with the same commit, and the reference
builds it on both. The cause is not the cache mount that five hypotheses were spent on - `cargo
build` fails with no mount at all - but overlayfs's oldest restriction: **a directory that exists
only in a lower layer cannot be renamed.**

| measured on the failing machine               | answer            |
| --------------------------------------------- | ----------------- |
| `/sys/module/overlay/parameters/redirect_dir` | `N`               |
| rename in a userns overlay                    | I/O error         |
| mount with `redirect_dir=on` in a userns      | permission denied |

The kernel refuses `redirect_dir` to an unprivileged mounter, so it cannot be enabled - rootless
overlayfs does not have the feature. macOS runs its guest as real root in a VM and is unaffected.

**This is the first divergence found between this engine on one platform and this engine on
another**, rather than against the reference. It was invisible until rootless Linux existed, four
iterations ago, and it affects `cargo`, `npm` and `maven` - every toolchain that renames a build
directory.

### Why nothing is being done about it yet

Three remedies, none chosen by evidence:

* **warn on every rootless build** - noise on the builds that never rename a directory, which is
  most of them, and this branch has spent a fortnight removing diagnostics that fire where nothing
  is wrong;

* **hint only when a step fails** - the right shape and the fuzziest signal, since attributing an
  `I/O error` inside a toolchain to this restriction is a guess;

* **refuse rootless outright when a build might rename** - unknowable in advance, and refuses builds
  that would have worked.

The measurement is done and the judgement is a maintainer's. It is pinned by a test so that a kernel
or policy change is noticed rather than assumed.

### The method note

E102 ended by writing down the *next probe* instead of the next guess, and that probe answered it in
one run after five wrong hypotheses. **The discipline that paid was recording the question at the
moment of giving up, while the shape of the problem was still in hand.**

## Rootless Linux: six of eleven corpus failures are one cause

The sweep found eleven failures and ten had not been read. Six carry one message - `apt` exiting
with code 112 - and six with one message is not six failures.

The control says it is not the machine: `docker run debian apt-get update` prints `APT-WORKS` there.
Inside our sandbox:

```text
RUN apt-get update                              -> error code 112
RUN apt-get -o APT::Sandbox::User=root update   -> works
```

That option is apt *not* dropping to the `_apt` user. **Our user namespace maps a single uid**, which
is all an unprivileged process may write to `/proc/pid/uid_map` on its own, so a step cannot become
any other user - six corpus examples, and every `USER` directive there will ever be.

### The fix is known, present, and was attempted the wrong way

`/etc/subuid` delegates 65536 ids and `newuidmap` is installed, so the range can be mapped. The
attempt - spawn unmapped, map from the parent, release the guest through a pipe - produced a guest
that could not mount its own overlay, and the range turned out to be innocent: the same mapping
applied by hand mounts fine.

**Capabilities are fixed at `exec`.** `newuidmap` needs a pid, so it runs after the child exists, by
which time the guest has already exec'd as `nobody` and gained nothing. This is precisely why runc
ships `nsexec` and podman re-executes itself: the mapping has to land before the exec that matters,
which needs a stage that clones, waits, and then execs.

Reverted rather than left staged - a half-finished capability that breaks the working case is worse
than the limitation it was addressing - and recorded with the shape of the real fix.

### The method note, which is the uncomfortable one

Every other finding this session was reached by measuring first and building second. This one was
built first, and the measurement that would have stopped it - *does a mapping applied after exec grant
anything?* - took four minutes once it was finally asked.

## Rootless became usable, and the suite proving it had never run

Three findings in sequence, each uncovered by acting on the last (E105-E107).

**The namespace holds a range now.** E104 diagnosed the one-uid limit and showed why the obvious fix
could not work: capabilities are computed at `exec`, so writing a range with `newuidmap` after the
guest has already exec'd grants it nothing. The guest now waits on a pipe, the parent maps
`0 -> euid` plus the whole delegated `/etc/subuid` block, and the guest re-executes itself so its
capabilities are computed with the mapping in place. `apt-get update` works unmodified, which was six
of eleven corpus failures, and the overlay still mounts - which the reverted attempt broke.

**The cross-backend suite compiled for one backend.** `engine/cli/e2e_sandbox_test.go` was
`//go:build darwin`, so the shared case table had never been asked of the Linux backend at all. It
now runs on both: 25/25 on each, and 2.85 s on Linux against minutes on macOS, because "boot" there
is a `clone` rather than an 8 GiB virtual machine. Twenty files in that package are still
`_darwin_test.go`; this was the one whose entire purpose was to be differential.

**`StoreDir()` answered `""` on both backends.** Not the same bug twice - the native backend resolved
its root in `Start`, and Apple's needed a guest binary that `Available()` never checks - but the same
missing invariant: *a sandbox that reports itself available can name its store, absolutely, and gives
the same answer twice.* The `""` was not an error but the working directory, so two tests filled
`engine/exec/layers/` in the source checkout and then failed with a message about the guest's
filesystem.

### What this changes about the stage table

| stage | before                                     | now                                              |
| ----- | ------------------------------------------ | ------------------------------------------------ |
| S3    | real on Linux, rootless with one uid       | real on Linux, rootless with the delegated range |
| S4    | real on macOS; Linux untested by the suite | real on both, both under the same case table     |

S5 and S6 are unchanged and remain the whole of the remainder. What changed is the confidence
underneath the stages already claimed: "real on Linux" was resting on ad-hoc probes and a
darwin-only suite, and now rests on the same twenty-five cases the macOS backend answers.

**Three failure classes, one shape.** The portable thing was made portable and its only consumer was
not (E106); the fix was reasoned out, commented, and applied to one of two implementations of the
same interface (E107). Both were found by asking a question of the *interface* rather than of an
implementation - which is also the only reason either test will catch the third backend.

## The corpus on Linux: 12 of 12, and what it took

Un-gating eighteen accidentally darwin-only test files (E108) ran the corpus sweep on Linux for the
first time. It found three defects in an afternoon, and all three had been reachable on macOS -
rootless simply removes the privilege that was hiding them.

| sweep                      | built | cause of the failures                         |
| -------------------------- | ----- | --------------------------------------------- |
| first ever, on Linux       | 8/12  | `dpkg` EXDEV renaming a lower-layer directory |
| after `userxattr` (E109)   | 10/12 | `chmod` on a device the unpacker had skipped  |
| after `makeSpecial` (E110) | 12/12 | -                                             |

E103's limitation is closed. It was recorded as kernel policy needing a maintainer's judgement, and
that was right about `redirect_dir=on` and wrong about there being no remedy: `userxattr` is a
one-word mount option and every rootless container runtime uses it. The judgement it *did* need was
about the pairing - `userxattr` moves overlayfs's opaque marker too, and a marker in the namespace
the mount is not reading is silently ignored, which turns a deletion back into a file.

### The stage table, and what "real" now rests on

| stage | state                                                                |
| ----- | -------------------------------------------------------------------- |
| S3    | real on Linux, rootless, with directory renames working              |
| S4    | real on both backends, one case table, twelve corpus targets on each |
| S5    | unchanged - simulated, FUSE or seccomp-unotify still undecided       |
| S6    | unchanged - not started                                              |

One honest platform difference is now recorded as a *capability* rather than a build tag: `WITH
DOCKER` needs a sandbox image carrying a daemon, which the native backend has no equivalent for. The
engine refuses clearly (I11), the test skips naming the gap, and it will start running by itself
when the gap closes - which a `_darwin_test.go` suffix would never have done.

### The shape worth carrying forward

Four findings, one failure class: **a rule established in one place and not applied at its sibling.**
The shared half always looked finished, because it was. What found each of them was asking a question
of the shared thing - the interface, the specification, the policy - rather than of one user of it,
and that is now three source guards rather than three lessons:

* `TestTheCrossBackendSuiteRunsOnEveryBackend` - a differential suite compiles for every backend
* `TestNoTestIsGatedToAPlatformWithoutNamingOne` - a build tag names a platform-specific thing
* `TestAStoreDirIsKnownBeforeAnythingStarts` - asked of `Sandbox`, so the next backend is asked too

Source guards are worth exactly what the existing ones claim: they prove somebody wired it up, never
that a build reaches it. Each is paired with a behavioural test, and the pairing is the point.

## S5: what has to be true before a source is written

The observation source is the last real feature and the one that can do the most damage, because its
failure mode is a false cache hit rather than a build error. Before building one, the Κ₂ path was
read for what it *assumes* of a source (E112), and it assumed too much:

* `Observed` and `Incomplete` are two booleans set by the source author from memory.
* `Consistent` returns true for every base when the observation is empty, so an empty-but-confident
  observation makes a result valid everywhere.

* The prediction side is symmetric, and at S6 the prediction arrives from another machine.

Both are now decided by the scheduler rather than asserted by the source: an exec step that reports
reading nothing is treated as not having been observed, on the publish side and on the lookup side
independently. A source can still lie; it can no longer get this wrong by omission.

**This changes what a candidate mechanism has to prove.** The question is no longer "can it see file
opens" - E100 established that seccomp-unotify and FUSE both can, and that eBPF cannot in a user
namespace. It is:

| requirement                       | why it decides the mechanism                                           |
| --------------------------------- | ---------------------------------------------------------------------- |
| sees the exec itself              | the executable is the step's first read; a late attach reports nothing |
| reports its own loss              | `Incomplete` is what makes a lossy source usable at all                |
| sees negative lookups             | 𝑁 is not a refinement of 𝑅; `[ -f /x ]` reads nothing (I3)             |
| survives a step that forks        | a build's real work happens in children of the shell                   |
| costs less than the rebuild saves | an L2 hit that costs more than a rebuild is a slower correct engine    |

The first row is the one that eliminates the easy implementations. Attaching a tracer after the guest
has already exec'd the step misses the executable and every library it loaded, and that observation
is *empty of exactly the things that differ between base images* - which is why the scheduler check
above had to exist before any source did.

## S5, half of it: the view exists now

L2 needs two things the engine did not have: a source that says what a step read, and a view that
says what a base holds. The second is buildable today and is a prerequisite whichever mechanism wins
the first, so it was built first (E114).

| piece                            | before                      | now                                             |
| -------------------------------- | --------------------------- | ----------------------------------------------- |
| `ViewSource`                     | declared, no implementation | `LayerStore.View` over the layer stack          |
| the digest 𝑅 records             | undefined                   | `layer.PathDigest`, one function for both sides |
| `Consistent` against a real base | never run                   | run, in tests, over real layers                 |
| an observation source            | none                        | still none - this does not change               |

Nothing is wired into `cli.go` yet, and deliberately: `Profiles` and `Views` are both required for
the L2 path and a profile store with no source to fill it would add a lookup that can never hit.
The stage stays **simulated** until a source exists. What changed is that when one does, it plugs
into machinery that has run rather than machinery that has only compiled.

The remaining decision is unchanged and now has a sharper test: a candidate mechanism must see the
exec itself, report its own loss, see negative lookups, survive a fork, and cost less than the
rebuild it saves. The first row still eliminates every implementation that attaches after the guest
has exec'd the step.

## The corpus number, and what is actually left

The full corpus on rootless Linux, native backend, no BuildKit anywhere in it:

| sweep               | attempted | built   | engine defects |
| ------------------- | --------- | ------- | -------------- |
| first, capped at 12 | 12        | 8       | 2              |
| after E109/E110     | 12        | 12      | 0              |
| **full corpus**     | **129**   | **115** | **0**          |

The fourteen that did not build are eleven `WITH DOCKER` targets, a tutorial whose `go.mod` requires
nothing, and a terraform example wanting AWS credentials. That is the honest reading of the number:
**the engine does not fail anything in the corpus on Linux**, and the largest remaining gap is one
feature rather than a tail of defects.

### What that changes about the sequencing

`WITH DOCKER` was filed under "a real platform difference, recorded as a capability". It is now
measurably the biggest single item between the native backend and parity - about 8% of the corpus -
and it is not a small feature: it needs a sandbox image carrying a daemon, and that daemon running,
inside a rootless namespace. Nested containers under a user namespace is its own project.

So the sequencing question is real rather than rhetorical: S5 (the observation source, which unlocks
Κ₂ and the cache behaviour the whole design is *for*) or `WITH DOCKER` (which unlocks a known 8% of
the corpus). Nothing here decides it; the measurement is recorded so that whoever decides is
deciding with a number.

### One thing found on the way

Green paper §A.3 specifies that prefetch masks drop entries after 𝑁 unused consultations, because
"extension alone is a ratchet that converges on the whole layer". The union was implemented and the
drop was not, so a project's mask accumulated every base image it had ever used. Fixed with 𝑁 = 3,
persisted across builds - the count has to survive the process *and* the file, and every unit test
passes without the second.

## Correction: WITH DOCKER is not its own project

The previous section said `WITH DOCKER` "needs a sandbox image carrying a daemon, and that daemon
running, inside a rootless namespace. Nested containers under a user namespace is its own project."
That was wrong (E117).

The native backend's sandbox filesystem is the invoking machine's. Its docker daemon is already
running, its socket is already reachable from inside the user namespace - supplementary groups
survive the mapping - and the only reason the engine could not see any of it was a path constant
naming where Apple's sandbox image keeps the client. There is no nested daemon to build.

What is actually there are two things the plan had not identified:

1. **A trust decision.** A step holding this machine's docker socket has root on this machine, and no
   namespace the engine sets up constrains that. A VM's daemon is disposable; this one is not. Now
   opt-in via `EARTH_ALLOW_HOST_DOCKER`, refused by default with the reason.
2. **A linkage problem.** The host's client is usually linked against the host's libc and the step's
   image usually is not, so the mounted binary fails on its interpreter and the kernel reports it as
   `docker: not found`. Checked before the mount is offered, so the diagnosis names the cause.

That reopens the sequencing question with better numbers: `WITH DOCKER` is days of work with a design
choice in it (host client, shipped client, or client-from-image), not a project. S5 remains the item
with genuine uncertainty in it, and it is now the only one.

### Where the estimate went wrong

The plan's claim was derived from what the *feature* does - run containers inside a build - rather
than from what the *engine* does for it, which is three bind mounts. Nobody read the code before
sizing it. That is worth naming because it is the same failure the last several findings share, from
the other side: those were rules stated in one place and not applied in another, and this was a
conclusion stated in one place and never checked against the code at all.

## S5 is real for one operation, and that operation is worth having

Κ₂ serves results on real builds (E125). The stage moves from **simulated** to **real for COPY**,
which is a smaller claim than it sounds and a larger benefit than it sounds.

Smaller, because a `RUN` step's reads still need a tracer and the mechanism is still undecided -
seccomp user notification and FUSE both work in the namespace (E100) and both need something outside
this branch's reach. Nothing here changes that.

Larger, because of which operation it is. A `COPY` sits above a `FROM`, so **every base-image bump
invalidates every copy above it** under chain keying alone - and a copy of an unchanged file into an
unchanged destination cannot produce a different layer however much the base moved. That is the most
common expensive miss a build system has, and it is now avoided:

```text
Earthfile:4    miss       FROM alpine:3.22
Earthfile:5    L2 hit     COPY src.txt /w/
cache          1 hit, 2 miss, 1 by observed inputs
```

### What makes it safe to have switched on

| property                                           | where it is held                          |
| -------------------------------------------------- | ----------------------------------------- |
| an observation of a base naming nothing is refused | `ObservesSomething`, both sides           |
| a lossy observation is declared and refused        | `Incomplete`, guest to host to scheduler  |
| the observer and the view compute one digest       | `layer.PathDigest`, asserted equal (E121) |
| a hit serves what a rebuild would produce          | two builds, real images, compared bytes   |
| a changed destination does not hit                 | `Consistent`, tested both ways            |

The fourth row is the one a newly-live cache tier owes and the one that cannot be faked: two builds
of one Earthfile produce the same bytes whether or not a cache exists, so the test asserts the hit
*happened* before comparing anything. Without that it would pass with the tier disabled - which is a
green gate over a feature that is not running.

### What was deliberately still off, and is not

This said `RUN` steps report no observation. That stopped being true and the paragraph did not
(E480). The tracer landed, `exec` asks for it on every non-interactive step, the guest records what
a step was seen to open, and `TestARunIsReusedOverABaseItDidNotRunOn` builds the same command over
two bases that differ in a file it never opens and asserts the second is served by observed inputs.

What remains off is one exclusion, and it is a decision rather than a gap: **an interactive step is
not traced.** Nobody at a prompt is producing a layer anybody will reuse, and every keystroke's
worth of shell completion would trap - measured at 8x on a path operation, 8.4µs against 1.0µs
(E213).

The claim and its evidence, so this paragraph cannot go stale quietly again:

| claim                                      | where it is held                                            |
| ------------------------------------------ | ----------------------------------------------------------- |
| a non-interactive step asks to be observed | `TestAStepAsksToBeObservedUnlessItIsInteractive`            |
| an interactive one does not                | the same test, other half                                   |
| a traced RUN is reused over a moved base   | `TestARunIsReusedOverABaseItDidNotRunOn` (green since E494) |
| an unobservable step declares itself so    | `Incomplete`, guest to host to scheduler                    |

**The third row was red for one increment**, and is the only row in this plan to have been. It is
kept in the history below because what it cost to find is the useful part. E480 could not run
it - the guest binary was missing, which the case-insensitivity note obscured (E490, E491) - and said
so. With a working sandbox it runs, and fails the same way twice:

```text
the RUN was not served by observed inputs, so its reads did not carry it over the moved base
  cache   3 hit, 2 miss, 1 of 2 predictions stale (/bin/cat changed in the base)
```

The cause was ownership, and the fix is below. Κ₂ for RUN steps now delivers what this section
claims, on the machine where it never had.

The first two rows had **no test at all** until E480. `Trace: !n.Op.Interactive` is one line carrying two
claims, and deleting it left every test in this repository green while the tier quietly lost the
only source a `RUN` has - which is *a rule that cannot fire is indistinguishable from one that is
satisfied*, in the place it costs most.

## S6 needs less specification than the plan said

The stage table said *"Appendix C of the Green Paper is a **[GAP]**"*, and it is not. Appendix C has
five sections - identity and rendezvous, protocols, assignments, transfer, failure - and exactly one
paragraph of C.5 is marked as a gap:

> **[GAP]** Claim arbitration under concurrent claims, and the back-pressure protocol when a worker
> is saturated, are not yet specified.

The engine already cites the appendix as settled law. `ir.go` says a step assignment is what crosses
a wire (C.3) and that `OpHost` is *"absent from the wire vocabulary entirely (C.3)"*; `schedule.go`
says *"a delegate is not the invoker and must refuse rather than execute (C.3)"*. Those citations
resolve, and they resolve to text that says what they claim.

**So S6 is not blocked on writing a specification.** It is blocked on two named questions inside one
section, and on the transport itself. That is a materially different piece of work from the one the
stage table described, and the table has been wrong about it for as long as the table has existed -
the same way it was wrong about `WITH DOCKER` being its own project (E117), and for the same reason:
the row was written from what the *feature* sounds like rather than from what the *document* says.

A citation guard now checks that every `§n.m` and `(n.m)` in the engine names a section or equation
that exists (E128). It cannot check a sentence like the one above, which is the residue: **a
mechanical check finds a reference that points nowhere, and a claim that points somewhere and
describes it wrongly is still only found by reading.**

## Appendix C against the engine: what already holds

C.5 is closed (E144), so Appendix C now asserts a complete protocol and it is worth saying which of
it the engine already does. The answer is more than expected, because most of C describes properties
of *types* rather than of code to be written.

| C's claim                                         | engine                                     |
| ------------------------------------------------- | ------------------------------------------ |
| a worker is sent an assignment, never a graph     | held - the executor takes a base and an op |
| `host` is not in the wire vocabulary              | held **by construction**, now guarded      |
| a delegate refuses a `host` step                  | held - `eligibleFor`, tested (E130)        |
| placement is legal per 4.7.1                      | held, tested with several workers (E130)   |
| concurrent claims are not arbitrated              | held - the store's insert-only rule (E142) |
| a worker that disappears re-queues its step       | **no code** - nothing can disappear yet    |
| a saturated worker refuses and the step re-places | **no code** - nothing can refuse yet       |

The last two are the specification being ahead of the engine, and that is the right way round:
neither can be exercised until a transport exists, and building a mechanism nothing can trigger is
how this branch has produced six separate written-and-unreachable defects (E49, E114, E125, E130,
E135, E136). **A specification may describe what does not exist; code may not.**

### The one worth guarding now

*"`host` is not in the wire vocabulary. A `host` op cannot be expressed in an assignment, so a
malicious peer cannot request one. **This is a property of the type, not a check that could be
forgotten.**"*

A property of the type is exactly the kind that dies quietly: somebody adds a request kind for a good
local reason and the sentence stops being true without anything failing. It is a security claim, and
it is now a register - every request kind the protocol declares, with what it is allowed to mean, and
a kind in one and not the other fails. Adding a request is then a deliberate act with a sentence
attached, which is the most a test can ask of a design property.

`Server.Unconfined` is the other half and is not reachable from the wire at all: it is a field set by
the process that starts the guest. A peer cannot ask for it, which is why it is a field and not a
request.

## The third attempt: why the second was not faster

Two attempts at a distributed EarthBuild came before this one. The second - `gilescope/rebuck`
PR 10 - reached the point of working and **was never faster than one machine**, on workloads that
were embarrassingly parallel rather than critical-path-bound. That is the result this attempt has to
beat, and it is worth being precise about why a correct fleet can be slower than no fleet.

A step on a worker costs `transfer + compute` where a step at home costs `compute`. A fleet wins only
when the transfer is amortised - paid once for many steps - and loses whenever it is paid per step.
Four ways to pay per step, and what this engine does about each:

| how a fleet pays per step                                                    | what this engine does                                                                                                         |
| ---------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| every worker fetches from the driver, so N workers queue on one uplink       | `Fetch` takes an ordered list of sources and a peer serves from its own store (C.4) - **the topology is a mesh, not a star**  |
| the base is shipped again for every step                                     | content-addressed layers and a store that outlives the step: `Provision` fetches only what this machine lacks (E258)          |
| transfer and compute are serial                                              | prediction (Κ₂) and prefetch exist for exactly this - **not yet wired to the fleet path**, and named here rather than claimed |
| the worker's keys differ from the driver's, so nothing it produces is reused | canonical keys and a byte-identical schedule (§4.7.3), tested against a real corpus                                           |

Three of those four are mechanisms this engine has. The fourth is the one to build next, and the
honest position on all of them is that **none has been measured against a single machine**.

### The advantage this attempt has

The second attempt was split across two codebases - EarthBuild driving, BuildKit executing - so the
scheduler could not see what the executor knew, and the transfer could not be planned against the
schedule. This one is a single engine: placement (§4.7.1), the key (§4.5), the observation set (§3.4)
and the transfer (C.4) are all in one process and can be reasoned about together. That is wiggle
room, not a result.

### The kill criterion

A fleet of *n* machines on the adversarial corpus must beat one machine on wall-clock, by enough to
be worth the complexity, and the measurement must attribute the time - transfer, wait, compute - so
that a loss is diagnosable rather than merely disappointing. **That measurement does not exist yet**,
and until it does this section is a plan and not a finding.

The next increments, in order:

1. ~~an accounting of where a fleet build's wall-clock goes~~ - done (E259). Transfer, compute and
   overhead are counted apart, and the report names which of the three dominated;
2. ~~peers as sources on the worker path~~ - done (E260). A worker announces where it can be
   reached, the driver remembers who produced what, and the next step needing a layer is told where
   to look. Advisory by construction (I5), so an unverified address cannot make a wrong build;
3. ~~a layer codec~~ - done (E262). `layer.Pack`/`layer.Unpack` round-trip a tree to the same
   identity, deterministically, refusing paths that escape the root, and `fleet.Layers` uses it as
   both a source and a destination (E263), and the wire carries packs (E264) - a layer crosses a
   real connection and `earth-worker` serves and fetches them. **A fleet of two machines can move a
   base.** And placement now keeps a chain on the machine that holds its base (E265), which was
   measured at three base transfers per four-step chain before and none after - the arrangement in
   which adding machines makes a build slower - and that affinity is load-aware (E266), or a
   fan-out from one common base would run the whole build on whoever held it. The same measurement
   found a worker fetching one base five times concurrently where twice would do. What remains is to
   point the accounting at two real machines - which needed a driver that knows what its workers can
   build (E267), since a fleet whose platforms were unknown was both unusable for `--platform` steps
   and unsafe for the rest;
4. ~~a worker announcing its capacity~~ - done (E272), and it took the measured speedup from 2.01×
   to 2.88× of an ideal 3×, and `EARTH_FLEET_CAPACITY` tells a worker to take fewer cores than the
   machine has (E273);
5. ~~bringing a worker's layer back so a step that must run on the invoker can~~ - done (E274);
6. ~~overlap: provision while waiting for a slot~~ - done (E275), measured at 1.204s to 0.902s on
   two steps of one slot;
7. a forecast, so a fleet arrangement can be judged before it is run (E268) - `fleet.Predict` uses
   the engine's own placement and is checked against what a real fleet moves, in the same tests that
   measure it.

Only then is the comparison worth running, because only then does a loss say why.

## Lazy transfer: fetch the fragment, not the layer

A base image is mostly never read. Container runtimes learned this and answered it with seekable
layer formats - stargz and eStargz, and SOCI's separate index - so a container can start on a
fraction of what it nominally needs and pull the rest on demand.

The same argument applies here with more force, because a build **repeats**: the same base is
materialised for step after step, and each step reads a different handful of files out of it.

### This engine has better information than a snapshotter

A lazy snapshotter is guessing. It fetches on page fault, which means it learns what a workload
needed only by watching it need it, and it pays a round trip for each discovery.

This engine already computes the read set. §3.4's observation records exactly which paths a step
looked at, S5's tracer produces it for `RUN` as well as `COPY`, and Κ₂ turns it into a *prediction*
of what the next run of that step will read - which is the thing a snapshotter cannot have.
`Hints.ReadsPredicted` exists on the wire and carries it today, advisory and unused.

So the shape is available now:

1. the driver knows what a step read last time;
2. it sends that prediction with the assignment, where it is already allowed to be wrong (I5);
3. the worker fetches **those paths** rather than the layer, and materialises a base that is complete
   for the paths that were predicted and absent elsewhere;
4. anything the step reads that was *not* predicted is a fault the worker resolves by fetching that
   path - the snapshotter's mechanism, kept as the fallback rather than the plan.

### What makes it safe

A prediction that is wrong makes a build slower and never wrong: the read set is a hint, the layer's
digest is unchanged, and a path fetched late is the same bytes as a path fetched early. That is I5
holding a much larger mechanism than it was written for.

What it is **not** allowed to become is a base whose *identity* depends on what was fetched. The layer
is still named by its whole tree (§3.2); a partially materialised base is a materialisation strategy,
not a different layer, or two workers would key the same step differently.

### Cost, honestly

A per-path fetch is a round trip, and a build that mispredicts pays one per miss - which is why the
prediction matters more here than in a snapshotter, and why the fallback has to batch. The measurement that decides it has now been taken (E283): a step names **tens to low hundreds of
paths** against a tree of twenty thousand files - under one percent. Sending a whole base to run a
step that reads a hundred files moves three orders of magnitude more than it needs.

**Status: the codec exists** (E281). `layer.PackPaths` sends the part of a layer that was asked for,
with the boundary that matters asserted rather than assumed: a fragment captures to a different
identity and can never be filed as the layer. `fleet.Fragments` is where one lives (E282), on a
different shelf from layers so the two cannot be confused.

~~Before this goes further there is a decision to take.~~ There is not (E284). A layer is hashed over
metadata and per-file content digests, never file bytes, so the sequence the digest covers *is* a
manifest - two megabytes for a base of twenty thousand files. Send it, hash it, compare it to the
layer's name, and every content digest in it is authenticated; `layer.VerifyFragment` then checks a
fragment file by file. No change to §3.2.

`fleet.Fragments.PutVerified` is the only way a fragment enters a store, and it checks both halves
(E285), and the wire carries a fragment with its proof (E286) - measured at 2.8% of a layer,
manifest included. The driver now sends a step's predicted read set (E287), taken from the profile
store Κ₂ already keeps, and a worker can fetch one (E288) - measured at 2.8% of a layer.

The fault-in exists (E289): `trace.Tracer.Fill` fetches a path before the syscall that wants it is
allowed to proceed, and a fetch that fails is fatal rather than an ENOENT the step would believe.

The two ends are joined (E290): `fleet.Filler` turns a path into a fetch, with the three outcomes the
safety depends on - placed, honestly absent, or unreachable and therefore fatal.

The channel between them exists too (E291): a fault-in travels guest to host, which is the direction
nothing else in that protocol goes, because the tracer is inside the confinement and the peers are
outside it.

`Filler.Prime` and `Filler.Fill` are a base between them (E292), measured at 39 of 40 files never
moving.

**What remains is `engine/exec`**: a step's base is assembled there, from layer directories, and a
lazy base is assembled differently.

The obstacle in that seam is now known and answered (E293). Overlayfs cannot host a lazy base - a
lowerdir may not change under a live mount, and a fault-in is exactly that - so a faulted-in file
lands in the upper directory with the step's writes, and `layer.TakeExcluding` leaves it out by name
*and* digest. A lazily materialised step therefore produces the same layer an eagerly materialised
one does, which is the property the whole idea stands or falls on.

One hole is known and loud rather than silent (E294): a step that *deletes* a file from a lazy base is
refused, because the deletion marker is a character device this engine cannot write there. A lazy base
therefore serves steps that read and write, and not ones that remove - which is most of them, and not
all.

The guest side is joined (E296): `EARTH_GUEST_FILLS` names a descriptor, the server hands the tracer a
filler, and the capture leaves out what arrived. Four nils deep, absent configuration means the
identical behaviour every build has today.

The host end exists too (E297): `exec.Native.Fill`, nil everywhere, and when set it opens the channel
and serves it.

Measuring before flipping it was worth it (E298, E299). The manifest, not the fragment, was the
dominant cost of a small read set; a proof now crosses once per layer, and a second fragment of the
same layer costs 14% of the first.

**Warm, a lazy base moves 0.2% to 2% of the layer** for read sets the shape E283 measured. That is
the number the decision rests on.

The guest accepts a base that arrives assembled (E300), which is the seam a primed base needed:
`Request.Prepared`, refused alongside a stack rather than resolved by precedence.

The prediction now reaches the node the executor is handed (E301), on `ir.Meta`, which is not in the
identity - asserted rather than assumed.

The executor chooses (E302): with a primer, a prediction and a base, it primes and materialises a
prepared root; without any of the three it stacks layers as it always has, and a primer that fails
falls back rather than failing the build.

Wiring a worker to it found one hole (E303): a fault-in named a path and not which base, so a host
serving two steps at once would have guessed - and served one step a file out of another's. Every
request now carries the handle, and what was faulted in is remembered against it.

**And it is on** (E305): `cmd/earth-worker` sets `Prime`, `Fetch` and the sandbox's `Fill`, so a step
delegated to a worker gets a base primed with what it was predicted to read and faults in the rest.
A sandbox that cannot fault in says so and gets whole layers.

Everywhere else `Prime` and `Fill` are nil, so a local build takes the branches it always has.

The whole has now been run once (E306): a real command, on a real lazily materialised base, under the
real tracer, producing **the same layer it produces against a whole base**. It found one thing no
amount of mechanism had - the directories priming leaves behind, which an overlay would never have put
in a delta.

That gap is closed (E307): every directory between a step's root and a faulted-in path is base, and
the root is what bounds the walk - which is the difference between the fix that was reverted and the
one that works.

The measurement on two machines has been attempted and has not produced a number (E308): every step
was refused and run locally, and the reason was being discarded. With it printed, the fault is
specific and then more specific again (E309), and then the hop turned out to be fine (E310): holders
do reach a worker across a real connection, tested as one thing for the first time. **The fault is in
the probe's own configuration** - two of them, both fixed (E311) - and one that was not: a peer that
stopped answering was being reported as a peer with nothing.

The measurement is now explained (E313). The base *does* cross the wire; it captures on the far side
under a **different digest**, because a layer's identity includes file ownership and an unprivileged
unpack cannot restore it. Two machines with the same OS and the same user transfer fine, which is why
every in-repo test is green and why five experiments went past it.

Next: `Layers.Put` captures against the ownership the pack declares rather than what landed on disk.
`TakeIn`'s `IDMap` is the shape that already expresses this.

Six boundaries in that one path were discarding the reason the next needed; five are now fixed, and
the sixth was the bug.

**Fixed, and measured (E315).** A layer is captured, packed and proved against the ownership the
stream declares rather than what the receiving filesystem accepted, through one `layer.declared`.
A darwin driver and a linux worker now share a base: 4 of 4 steps delegated, 1.6 MiB in 223ms.

Next, in order:

* ~~the cost model predicts 0 bytes for a run that moved 1.6 MiB~~ - fixed (E316). `Predict` counts
  a base from the driver, and keeps it apart as `FromOrigin` because the two costs have different
  remedies. Forecast and measurement now agree to the byte;

* ~~four transfers for a four-step chain~~ - there was one transfer of one base; the earlier reading
  divided the right number by the wrong denominator (E316);

* the fragment path's receiving side records no declaration, so a lazy base still relays wrongly -
  it fails safe (the fragment is refused) and is off by default;

* ~~placement does not yet use the size of what it would ship~~ - fixed (E317). A fetch is priced
  from what this fleet has been measured to do, so a base worth three hundred steps is no longer
  priced like one worth half a step. The model prices identically, through `PredictAt`;

* the arrangement that demonstrates it needs **two** workers and a base worth more than a queued
  step - one worker with no local alternative has nothing for a price to decide;

* a driver can now decline to delegate at all when the inputs cost more to ship than the step is
  worth and it already holds them (E318). That is the lever the second attempt lacked entirely;

* ~~E318 has no view of the whole build, so several individually-worthwhile steps can saturate one
  worker~~ - fixed (E320). A step does not queue behind a full fleet when this machine is free and
  already holds its inputs. **1.8x** on the arrangement that was already healthy, by using the
  driver;

* ~~a whole wave decides before any reply exists~~ - fixed (E319). One step goes out to find out what
  the fleet costs and the rest wait for it, bounded. **6x faster** on the arrangement that was
  pathological (32 MB base against 30ms steps), unchanged on the one that was not.

Four levers now exist and are measured: price a fetch (E317), decline to ship one (E318), find out
the price before committing a wave to it (E319), and stop queueing behind a fleet that is full
(E320). Together, on one worker over a real LAN: **1.8x** on a healthy arrangement and **6x** on a
pathological one, against the same engine a week ago.

**Against one machine over a real LAN: 1.43x typical and 1.57x at best** (E322), against a ceiling of
about 1.6x for two machines of two slots on eight steps.

The three thresholds of E318, E320 and E321 are now one comparison in terms of **waves** - which side
finishes this step sooner, counting the transfer. Every test of the three passed unchanged against
the one, which is the strongest evidence available that they were one rule badly factored.

What remains:

* ~~more than one worker has never been measured over a real network~~ - done. Three machines,
  twelve steps: **2.0x** on a 1.6 MB base (E323);

* ~~lazy transfer has never run between machines~~ - done, and it is the difference between a fleet
  that helps and one that hurts. On a 16 MB base where each step reads ten of 2000 files: whole
  layers 3.588s (**slower than one machine**), lazy **1.243s**, 1.8% of the bytes (E323);

* a wrong prediction no longer switches the fleet off (E327), and no longer costs the whole base: the
  worker fetches **the file the step asked for** and tries again, falling back to the whole layer only
  for a hint that is wrong repeatedly (E328). At one step in two mispredicting, 7.059s -> 2.198s and
  63.6 MiB -> 1.7 MiB;

* **the fleet is transfer-bound, not overhead-bound** (E336). The ~500ms a step was the uplink queue,
  invisible because both provisioning paths started their clocks after taking the lock, and a driver
  attributes everything a worker does not report to the network. Wire time is 106ms a step. The open
  question is now answered (E337): **the transport contributes nothing**. Serving a fragment walks
  the layer twice and hashed every file to send one. Both halves are fixed (E337, E338): 26.1ms to
  **2.9ms** a fetch, and **2.01x against one machine** at four workers, up from 1.57x;

* what remains is **not** the stat walk (E339). The proof a fragment carries is 2.6x the fragment -
  213 KB to authenticate 83 KB - and it crosses once per worker per layer. It cannot be shrunk where
  it is felt: a layer's manifest is the pre-image of its digest, and a flat hash admits no subset
  proof. Making layer identity a **Merkle root over sorted entries** (§3.2, §3.3) would, and that is
  an identity change with the cache and B.2 downstream of it - its own iteration, not a footnote;

* **the headline is 1.26x on levels, median of five cold rounds** (E349), and the interval is the
  result as much as the number: the best fleet round beat one machine by 1.5x and the worst by 1.03x.
  A single machine repeats itself to 0.6%; the fleet varies by 47%, so **the variance is the fleet's
  and not the harness's** - and every "no measurable effect" recorded before this was measured
  against an instrument nobody had characterised;

* **the spread is a warm-up, and it is the engine's own knowledge** (E350). The first round delegates
  everything and keeps nothing, because an unmeasured fleet prices a transfer at zero; later rounds
  keep two or three steps and run 25% faster. A real build is always round one, so **1.13x is the
  honest figure and 1.51x is what the same fleet does once it knows what it costs**;

* ~~persisting what the fleet costs between builds~~ - done (E351). Kept beside the layers it is
  about, loaded on start, written on exit, and never load-bearing: **16% by the third build across
  process boundaries**, 1.39x against one machine where a cold build gets 1.17x. The second build
  over-corrects - and that is **correct inference from cold evidence**, not an estimator fault: a
  decaying mean was written as a test first and the test came back green, so it was not built (E352);

* ~~the honest headline is 1.14x, on levels~~ (E345, superseded) - the shape a build actually has. A fan-out
  gives 2.31x and a chain 0.85x, so a fleet's value is almost entirely a property of the graph, and
  the two shapes measured for forty experiments were both corner cases;

* `tools/mutate` restores the file it is mutating when it is killed (E348). It did not, and three
  interrupted sweeps in one session left mutants in the tree;

* ~~on levels the driver sits out the whole build~~ - fixed (E347). What this machine would have to
  fetch is a term, not a veto: **a quarter off the saturated case** (2.008s to 1.521s on one worker,
  7 of 16 steps kept), and the roomy cases unchanged. It uncovered two latent faults - a TOCTOU in
  `Layers.Put` and a driver *dialling* workers, which E279 says cannot work;

* a transfer now costs something before it moves a byte (E346), which is right as arithmetic and
  moved no measurement at this scale;

* **2.31x against one machine** on a fan-out (E344), the best measured: a transfer already paid for
  is no longer charged again, which is what makes an expensive base placeable at all;

* a **default rate** would fix the chain's one blind transfer and is not safe until one step is
  delegated regardless, to learn - otherwise nothing is measured and the default is never corrected
  (E344). The pilot gate is almost that rule and gates waiting rather than deciding;

* **a chain is 17% slower with a fleet than without one** (E343), and it costs exactly one base
  transfer: the first step is delegated blind, and after it the work lives on the worker. Avoiding
  that needs the **shape of the graph**, which the scheduler has and the driver is never told - the
  next change is to what a driver knows, not to how it decides;

* ~~the probe measures one shape and it is the forgiving one~~ - it now runs chains too (E342, E343). A fan-out from a single base
  saturates every worker immediately, so three correct changes in a row bought nothing measurable
  (E335, E340, E342). The next thing to build is a **chain** in the probe - each step standing on the
  last - which is where per-step latency, prefetch and a cold worker actually appear, and is the shape
  of every real build's critical path;

* **2.26x against one machine** at four workers (E341), the best measured. The remaining cost has a
  shape: **one fetch per worker per build, about 300ms**, with that worker's other steps waiting
  behind it while three machines idle. The answer is to prefetch on join, not to make the fetch
  faster - `Hints.Images` exists for it and nothing uses it;

* a Merkle layer identity (E339) is **not** worth doing: the proof crosses once per worker per layer
  and compresses to about 20 KB, so the change would save 17 KB once, on a link where E340 showed
  bytes cost no time at all;

* the proof now crosses compressed, 16x smaller (E340). **This bought no time on a gigabit LAN** -
  bytes were never the constraint here - and is for links slower than the one it was measured on;

* the reply vocabulary is specified too (C.3.1, E334), including refusal-versus-exit, which the
  engine has enforced since E232 and never stated;

* the hint vocabulary is specified and mechanically guarded in both directions (E333) - it had grown
  two load-bearing fields the document never heard of;

* partial transfer is specified (green paper C.4.1) and its seal is an invariant (I13). It had no
  normative definition at all, while being the mechanism that decides whether a fleet helps (E332);

* one test now reads the source of what people run and checks that every mechanism this project
  measured is actually called there (E331). Five had been built, tested, measured and left unwired;

* the driver states its own capacity and knows how big its layers are (E330) - without them E321 and
  E317 were inert in every build that was not the probe;

* `earth-worker` can now prime and fault in between machines at all (E329) - it had been dialling
  the driver's control endpoint with a blob protocol since the day it was written;

* `Predict` still is not consulted by the driver. It prices identically and could answer questions
  a per-step comparison cannot - which steps are about to become ready, and whether this wave should
  be split at all;

* a fragment is now sealed on every §3.3 field its receiver can reproduce, not on contents alone
  (E324) - which mattered the moment lazy became the winning configuration;

* ~~a worker cannot serve on a layer it holds only in fragments~~ - done (E325). `Parts` serves whole
  layers and parts of layers, the server no longer gates the fragment path on `Has`, and a worker is
  named as holding what its steps stood on;

* **the premise, reproduced and cured at four workers** (E326). Whole-layer transfer makes a fleet of
  four **2.8x slower than one machine**; lazy transfer on the same arrangement is **1.57x faster**,
  and 4.35x faster than shipping whole layers - up from 2.9x at two workers;

* a wrong prediction no longer switches the fleet off (E327), and no longer costs the whole base: the
  worker fetches **the file the step asked for** and tries again, falling back to the whole layer only
  for a hint that is wrong repeatedly (E328). At one step in two mispredicting, 7.059s -> 2.198s and
  63.6 MiB -> 1.7 MiB;

* **the fleet is transfer-bound, not overhead-bound** (E336). The ~500ms a step was the uplink queue,
  invisible because both provisioning paths started their clocks after taking the lock, and a driver
  attributes everything a worker does not report to the network. Wire time is 106ms a step. The open
  question is now answered (E337): **the transport contributes nothing**. Serving a fragment walks
  the layer twice and hashed every file to send one. Both halves are fixed (E337, E338): 26.1ms to
  **2.9ms** a fetch, and **2.01x against one machine** at four workers, up from 1.57x;

* what remains is **not** the stat walk (E339). The proof a fragment carries is 2.6x the fragment -
  213 KB to authenticate 83 KB - and it crosses once per worker per layer. It cannot be shrunk where
  it is felt: a layer's manifest is the pre-image of its digest, and a flat hash admits no subset
  proof. Making layer identity a **Merkle root over sorted entries** (§3.2, §3.3) would, and that is
  an identity change with the cache and B.2 downstream of it - its own iteration, not a footnote;

* **the headline is 1.26x on levels, median of five cold rounds** (E349), and the interval is the
  result as much as the number: the best fleet round beat one machine by 1.5x and the worst by 1.03x.
  A single machine repeats itself to 0.6%; the fleet varies by 47%, so **the variance is the fleet's
  and not the harness's** - and every "no measurable effect" recorded before this was measured
  against an instrument nobody had characterised;

* **the spread is a warm-up, and it is the engine's own knowledge** (E350). The first round delegates
  everything and keeps nothing, because an unmeasured fleet prices a transfer at zero; later rounds
  keep two or three steps and run 25% faster. A real build is always round one, so **1.13x is the
  honest figure and 1.51x is what the same fleet does once it knows what it costs**;

* ~~persisting what the fleet costs between builds~~ - done (E351). Kept beside the layers it is
  about, loaded on start, written on exit, and never load-bearing: **16% by the third build across
  process boundaries**, 1.39x against one machine where a cold build gets 1.17x. The second build
  over-corrects - and that is **correct inference from cold evidence**, not an estimator fault: a
  decaying mean was written as a test first and the test came back green, so it was not built (E352);

* ~~the honest headline is 1.14x, on levels~~ (E345, superseded) - the shape a build actually has. A fan-out
  gives 2.31x and a chain 0.85x, so a fleet's value is almost entirely a property of the graph, and
  the two shapes measured for forty experiments were both corner cases;

* `tools/mutate` restores the file it is mutating when it is killed (E348). It did not, and three
  interrupted sweeps in one session left mutants in the tree;

* ~~on levels the driver sits out the whole build~~ - fixed (E347). What this machine would have to
  fetch is a term, not a veto: **a quarter off the saturated case** (2.008s to 1.521s on one worker,
  7 of 16 steps kept), and the roomy cases unchanged. It uncovered two latent faults - a TOCTOU in
  `Layers.Put` and a driver *dialling* workers, which E279 says cannot work;

* a transfer now costs something before it moves a byte (E346), which is right as arithmetic and
  moved no measurement at this scale;

* **2.31x against one machine** on a fan-out (E344), the best measured: a transfer already paid for
  is no longer charged again, which is what makes an expensive base placeable at all;

* a **default rate** would fix the chain's one blind transfer and is not safe until one step is
  delegated regardless, to learn - otherwise nothing is measured and the default is never corrected
  (E344). The pilot gate is almost that rule and gates waiting rather than deciding;

* **a chain is 17% slower with a fleet than without one** (E343), and it costs exactly one base
  transfer: the first step is delegated blind, and after it the work lives on the worker. Avoiding
  that needs the **shape of the graph**, which the scheduler has and the driver is never told - the
  next change is to what a driver knows, not to how it decides;

* ~~the probe measures one shape and it is the forgiving one~~ - it now runs chains too (E342, E343). A fan-out from a single base
  saturates every worker immediately, so three correct changes in a row bought nothing measurable
  (E335, E340, E342). The next thing to build is a **chain** in the probe - each step standing on the
  last - which is where per-step latency, prefetch and a cold worker actually appear, and is the shape
  of every real build's critical path;

* **2.26x against one machine** at four workers (E341), the best measured. The remaining cost has a
  shape: **one fetch per worker per build, about 300ms**, with that worker's other steps waiting
  behind it while three machines idle. The answer is to prefetch on join, not to make the fetch
  faster - `Hints.Images` exists for it and nothing uses it;

* a Merkle layer identity (E339) is **not** worth doing: the proof crosses once per worker per layer
  and compresses to about 20 KB, so the change would save 17 KB once, on a link where E340 showed
  bytes cost no time at all;

* the proof now crosses compressed, 16x smaller (E340). **This bought no time on a gigabit LAN** -
  bytes were never the constraint here - and is for links slower than the one it was measured on;

* the reply vocabulary is specified too (C.3.1, E334), including refusal-versus-exit, which the
  engine has enforced since E232 and never stated;

* the hint vocabulary is specified and mechanically guarded in both directions (E333) - it had grown
  two load-bearing fields the document never heard of;

* partial transfer is specified (green paper C.4.1) and its seal is an invariant (I13). It had no
  normative definition at all, while being the mechanism that decides whether a fleet helps (E332);

* one test now reads the source of what people run and checks that every mechanism this project
  measured is actually called there (E331). Five had been built, tested, measured and left unwired;

* the driver states its own capacity and knows how big its layers are (E330) - without them E321 and
  E317 were inert in every build that was not the probe;

* `earth-worker` can now prime and fault in between machines at all (E329) - it had been dialling
  the driver's control endpoint with a blob protocol since the day it was written;

* `Predict` still is not consulted by the driver. It prices identically to the engine and could
  answer what a per-step comparison cannot - which steps are about to become ready, and whether this
  wave should be split at all.

Until then, **a build still moves whole layers** - and every mechanism it would need is built,
tested, connected end to end, and off.

**WITH DOCKER nesting (inception) - design decision, 2026-08-19.**

**The question.** Is it fine to share caches with the inner daemon instance in a nested
`WITH DOCKER` build? And how does a test that requires guaranteed cache misses get structural
isolation rather than a configured one?

**Answer, decided 2026-08-19: sharing is the default; isolation is `WITH DOCKER --isolate`.**

The panel argued the other way - that isolation is the only mode that can be made *structural*, so
it should be the one you get without asking. The decision went against it, and the reason is the
requirement: sharing is what an author wants almost every time, and a default most builds must
override is a default chosen for the minority.

**What the panel was right about is kept as a refusal instead.** Isolation is still structural when
it is asked for - an isolated block's daemon writes into the step's own overlay and dies with it,
because nothing is mounted (E365) - and the failure the panel feared, a cache-miss test silently
handed hits, is prevented by cacheability rather than by the default:

| the block says | daemon                            | cacheable                                      |
| -------------- | --------------------------------- | ---------------------------------------------- |
| nothing        | the outer one, where there is one | **no** - it may have shared                    |
| `--isolate`    | its own, storage dies with it     | **yes** - a function of its inputs             |
| `--cache-id=x` | its own, storage in that cache    | no - it was given storage something else wrote |

So a test looking for cache misses is not relying on a flag it might forget: a shared block is never
cached at all, and the only block whose result is reused is the one that could not have shared. The
ergonomics go to the common case and the correctness is not spent buying them.

A bare `WITH DOCKER` inside an inception build starts its own `dockerd`, writing to a
`--data-root` inside the step's own overlay. That overlay is discarded when the step ends.
No prior-build image is visible; no image built during the step survives the step. The guarantee
is not a matter of flags or configuration - it is that `ownDaemonMounts("")` returns nil mounts,
so no external directory is ever bound, and the daemon's storage cannot outlive the overlay it
lives in. A test of this engine's own caching behaviour writes exactly this form and gets a cold
daemon on every run by construction.

Sharing is declared in the inner Earthfile with `WITH DOCKER --isolate`. The executor stats
`/var/run/docker.sock`; if the outer step's executor already bound the outer daemon's socket
there, it is found and returned as a Sandbox mount. The inner step gets the outer daemon's socket
bound into its chroot before `isolate()` sets the root - the same mechanism and ordering every
other Sandbox mount uses. No environment variable is involved, which is the reason this works
through `docker run`: the socket is in the container's filesystem already, not in its environment.

**Why not environment-variable forwarding.** The alternative design (inject
`EARTHBUILD_DOCKER_HOST` into the step's shell env; inner `earth` reads it via `os.Getenv`)
was scored highest on mechanism composability but has a fatal flaw in the primary inception
scenario. `docker run` does not propagate the parent shell's environment to spawned containers;
an `earth` process running inside `docker run ... earth` never sees the injected var unless
the user passes `-e` explicitly - which contradicts the design's stated claim of requiring no
Earthfile change. The `--isolate` approach is exempt: it probes the filesystem, not the
environment, and the socket is already there.

**Four defects identified in review that must be fixed before any code is wired.**

First: `withDaemon` must wrap only the `isolate`+`runStep` tail in `execRequest`, not the
`bindMounts` block. The `--cache-id` bind mount must be in place before `dockerd` starts, so
`--data-root` writes land on the cache directory rather than the step's ephemeral overlay. If
`withDaemon` wraps `bindMounts`, a named-cache block silently discards everything it wrote -
it behaves identically to an uncached block with no error and no diagnostic.

Second: `Op.IsolateDocker` must be stamped on IR nodes by the interpreter, not just
declared. The loop at `loop.go:449-468` currently only stamps `Op.NoCache=true` when
`opts.CacheID != ""`. A `--isolate` block has `opts.CacheID=""` so the branch is never
taken, and every `RUN` inside the block gets `NoCache=false`, `IsolateDocker=false` - the
same key as a plain ephemeral block, making the outer daemon's results cacheable as if the step
had run against an empty one. The fix is a `p.shareOuter bool` field on Plan, saved and
restored in `withStatement` exactly as `p.dockerCache` is, with the loop stamping
`Op.IsolateDocker` and, for everything that is *not* isolated, `Op.NoCache=true`, and `dockerStep`
computing `NoCache: p.dockerCache != "" || p.shareOuter`.

Third: `Op.IsolateDocker` must appear in both hash sites - `ir.Node.ID()` at
`ir.go:488-492` and `hashOperation` at `key.go:104-108` - at the same position in both. The
two blocks are byte-for-byte mirrors. Missing either one lets a step switch between shared and
isolated modes without producing a new cache key. The test is written before the hash line is
added: assert that two `Op` structs differing only in `IsolateDocker` produce distinct
`Node.ID()` and `hashOperation()` values. That test is red until both sites are updated.

Fourth: `schedule.go:1160` contains `host := n.Op.Kind == ir.OpHost || n.Op.NoCache ||
n.Op.Docker`. The `n.Op.Docker` term makes every `WITH DOCKER` step uncacheable regardless
of `DockerIsolated` or isolation mode. An own-daemon step with no `--cache-id` IS
structurally isolated and its result is a function of its inputs, but the gate prevents it from
being cached. Narrowing the gate to `n.Op.Docker && n.Op.NoCache`
is the change that makes own-daemon steps cacheable, and it is treated as one atomic commit
with any new hash fields that discriminate those modes - otherwise a future narrowing of the
gate without the hash change turns the scheduler's permission into a false-hit source. The
scheduler's existing comment already names this as a stopgap with a known ending; the narrowing
is that ending, deferred to increment 3.

**What is refused rather than silently degraded.**

`WITH DOCKER --isolate --cache-id=X` is refused at the interpreter: `--isolate` uses
the outer daemon's storage, which `--cache-id` would falsely claim to name.

`WITH DOCKER --isolate` when `/var/run/docker.sock` does not exist is refused by the stat
check in `shareOuterDockerMounts` with a message naming the missing path and explaining the
flag is for a step already inside a container. A missing socket presenting as an unreachable
daemon would let the step fail ninety seconds later with a Docker connection error rather than
this engine declining clearly (I10).

`WITH DOCKER --isolate` outside a container (no `/.dockerenv`) without
`EARTH_ALLOW_HOST_DOCKER=1` is refused: the socket would be the host's daemon, which is root
on the host (E145).

`WITH DOCKER` with no flags and no `--cache-id` on Linux currently refuses (E354, the existing
`sharedDockerFor` path). That refusal lifts in increment 2 when `ownDaemonMounts` and
`step.Daemon` are wired.

**Three increments, in order.**

*Increment 1 is done, and one prescription in it was already met.* `Op.IsolateDocker` exists and
is hashed at both mirrors - the two reflective guards turned red on their own the moment the field
was added, so the test the panel asked for had been written years before the field was (E372).
Hashing it then exposed a scheduler defect that had nothing to do with WITH DOCKER: a cancellation
outranking the failure that caused it, so a build that failed on `exit status 3` reported `context
canceled`. `worseFailure` ranks kind before graph order now.

*The increments, in the decided polarity.*

**1 - done.** `Op.IsolateDocker` exists and is hashed at both mirrors; the daemon starts, waits,
serves a step, and stops (E364-E379).

**2 - done.** `--isolate` parses, is scoped like the cache name (saved and restored, because
`--load` opens another target's blocks inside this one), stamps the body's nodes *and* the steps the
block generates, refuses `--cache-id` alongside it, and is refused outright by the buildkit engine -
which shares the options struct and would otherwise accept it and do nothing. One old test was
reversed on purpose and one old mechanism removed: the `--cache-id` branch's own `NoCache` had
become redundant, and the sweep found it by noticing that deleting it broke nothing (E382).

**2 as originally written - the interpreter.** `--isolate` on `WITH DOCKER`, scoped like `p.dockerCache` is: saved and
restored, because a `--load` opens another target's blocks inside this one (E356). Stamp
`Op.IsolateDocker` on the body's nodes, and stamp `Op.NoCache` on everything that is **not**
isolated - a block that may have shared is not a function of its inputs, whichever daemon it
happened to find. `--isolate --cache-id=x` is refused: the flag says the storage dies with the step
and the option names storage that outlives it.

**3a - done, the executor.** `dockerPlanFor` decides from the block *and* its surroundings: bare
shares where there is an outer step's daemon, starts its own where there is nothing or where the only
candidate is the machine's own (E145), and `--isolate` or `--cache-id` take their own regardless. The
step is told which it got, because on this path the difference is invisible in the Earthfile. E354's
refusal is retired - there is a third answer now. One near-miss on the way: the check for an
inheritable socket used `exec.LookPath`, which asks whether something is *executable*, and would have
answered no on every machine forever (E383).

**3a completed** (E385): deciding to share is not sharing. The Linux plan returned `Inherit: true`
and no mounts, so a step told it was sharing would have found no socket - the design's whole default
as a comment. `withSocket` attaches the consequence, and refuses to attach it to a step that has a
daemon of its own, because two things at one path are resolved by mount order and isolation that
depends on mount order is not isolation.

**And a step reaches a daemon it did not start** (E386), on Linux, confined and chrooted, through
the socket bind `withSocket` arranges - the one mechanism in this design that only macOS had ever
exercised. Nesting therefore works in both directions now: a step can be given a daemon of its own,
and a step can be given one that is already running.

**What CI will need is now known rather than guessed** (E387): a **privileged** container - plain
fails, privileged passes - a `CGO_ENABLED=0` test binary, and no Go toolchain, because the tests now
use the running binary as their prober instead of building one. The capability message a nested build
will hit was in the wrong place and the unprivileged container proved it: the refusal arrives at
`clone`, not at `mount`, so the hint lived in a shim that never starts.

**And CI runs them** - `+engine-daemon`, privileged, wired into `ci.yml` beside `+engine-race`. It
refuses to be green if fewer than three tests passed, because these skip themselves where there is no
`dockerd` and an image that quietly lost its docker package would otherwise turn the target green
while verifying nothing. The recipe was run in a privileged container before it was written; what was
left unverified was Earthly syntax, which `earth debug ast` and the engine's own corpus sweep both
check.

**And it is documented** (E388). `--isolate` is in the language reference, described by what it is
for rather than what it does, and a reflective guard now demands that every option
`cmdopts.WithDocker` accepts appears there - a weak check that catches the failure that actually
happens, which is an option added to the parser and to nothing else.

**The corpus watches this construct in particular** (E389). 192 of the 489 targets that plan are in
Earthfiles using `WITH DOCKER`, from 27 files - a fifth of the corpus by file and two fifths by
target - and this work changed how every one of them is planned. A regression confined to them moves
the total by about a percent, so the slice has a ratchet of its own: `darwin-docker 192`,
`linux-docker 186`.

**And the specification now knows about it** (E390). The green paper said nothing about container
daemons - not a marked gap, silence - while the engine refused cache entries on a rule no document
stated. §3.4b defines δ, a daemon's provenance, with I14: it is in the key or the step is not
cached, and an engine that cannot provide the δ asked for refuses rather than substituting another.

**And writing it found a bug** (E391). The macOS backend gave `--isolate` the sandbox VM's daemon,
arguing the flag was unnecessary because that daemon dies with the build. True of earlier builds and
false within one: the blocks of a single build share it, an isolated block is cached, and block two
would have been served from a key claiming an empty daemon after block one loaded an image into it.
One Earthfile with two blocks reaches it. That backend now refuses `--isolate` and names where the
feature lives.

**Which daemon a block got is now reported, through the channel that was already there** (E393).
Routing it through the client warning made a build warn about a client that was fine (E392); the
right home was `UncacheableAt`, which already answers a per-step question with a source location -
and for every block that reaches it, which daemon it got *is* why it was not cached. A block that may
share is told so and told that `--isolate` is cacheable; a block that named a cache is told the cache;
an isolated block is told nothing about daemons, because if it is uncacheable the reason is
something else.

**And a backend that cannot isolate says so before it boots** (E394). The executor's refusal is the
guarantee; on a VM backend it arrives after an image has been chosen and a machine started, so the
plan is checked at the top of `executorFor` where nothing has cost anything yet. Two checks reading
different things at different boundaries - the graph and the step - which is E384's shape rather than
E382's redundancy.

**The first real build through the CLI found two things the seams could not** (E395, E396). It hung:
`awaitDaemon` waits on the caller's context, the step's context is the build's, and the build's has
no deadline - every unit test passed because each had supplied the bound the caller lacks. With the
wait bounded at 90 seconds it fails with the daemon's own complaint instead, and the complaint is
`sun_path` overflowing again: the daemon's listening socket is under the step's root, and a real
store path exceeds 104 bytes long before `/var/run/docker.sock` is appended. The daemon now listens somewhere short and the socket is bound into the
step once it exists, at the path the image's own `/var/run -> ../run` symlink leads to - resolved
with `COPY`'s resolver so a link cannot choose where this engine binds a live docker socket (E397).
**And the real build found the design's own error** (E398): an isolated daemon's storage was going
*into the image*. E365 reasoned that mounting nothing leaves the storage in the step's overlay, to be
discarded with the step - but a step's overlay is precisely what the capture turns into a layer, so
every isolated block shipped its whole `vfs` store, and the `docker.pid` in it made the next step
refuse to start a daemon that was "already running". "Discarded with the step" and "not captured from
the step" are different properties and the design used one word for both. The daemon's root must be a
mount either way; what differs is only whether the directory outlives the step.

**Built**: an ephemeral mount, protocol version 12 - a directory the guest makes for this step and
removes with it. Nothing new was needed inside the guest, because a secret is already staged that way
and for the same stated reason; the two cases now differ in one word, `Ephemeral`, and a named cache
is simply the one that is kept.

**And the build runs** (E399): `WITH DOCKER --isolate` through `cli.Run`, a daemon started for the
step, `docker info` answering `29.4.3`, in 3.74 seconds. Four defects stood between the seams working
and the build working - an unbounded wait, a socket path past the kernel's limit, an image's symlink,
and storage that went into the image - and not one was findable from the seam it lived in.

Checking the CI recipe in a privileged container then found the gate itself was miscounting: the
floor compared against every `--- PASS` in a binary holding the whole unit suite, so it would have
cleared without a daemon ever starting (E400). It now counts the four tests that need one, by name.
The end-to-end build test is in the gate too (E401): it failed in a container because a container's
root is overlayfs and overlayfs cannot stack on overlayfs, which the engine says in as many words
along with the remedy, so `TMPDIR` goes on a cache mount. Pointing *every* test there instead made
two of the guest's skip - their isolation probe is root in a user namespace, which is nobody on a
shared directory - so only the build test is redirected. Five tests must pass, named individually.

**Overlayfs has a price now** (E404). E4 measured the capture side and settled the choice; nothing
had measured the mount side. It is **9.3 ms per step**, of which **9.0 ms is the unmount** - against a
20 ms per-step floor, so a teardown is nearly half the budget of every step in every build. Stack
depth is nearly free (25 µs per lower), so there is no case here for flattening. The experiment then killed both the remedy and the
diagnosis (E405): `MNT_DETACH` costs the same to the microsecond, and the syscall is not overlayfs's

* the same unmount is **41 µs on tmpfs against 13 ms on ext4**, a factor of 316. A step's teardown
cost is a property of *where the scratch lives*, and the engine already has `tmpfs()`, reached only
as a last resort when overlay cannot stack (E69). One caveat keeps it honest: tmpfs is memory a step's output would have to fit in. The other - that
the machine's disk was full when this was measured - was tested and retired: with ten times the free
space the ratio is 302 rather than 316, which is the same answer (E431).

**On a real build it is a quarter of the wall clock** (E406): 21 cold steps take 1715 ms with the
store on ext4 and 1289 ms with it on tmpfs, 20 ms a step. Measured with the namespace held constant,
because the first attempt varied it alongside the filesystem. It is now available as `EARTH_SCRATCH_TMPFS=4g`
(E407), which takes the same build from 1711 ms to 1315 ms - **opt-in**, because tmpfs is memory and
an engine that took this by default would make every build faster until it made one impossible. A
misspelt size is refused rather than silently ignored, a percentage is refused although the kernel
allows one, and an ENOSPC caused by it says so.

**And the engine's settings are now written down** (E408). Twenty-seven environment variables changed
what a build did and none appeared in any document - including `EARTH_ALLOW_HOST_DOCKER`, which hands
a step root on the machine. `docs/native/settings.md` covers the six an operator sets; a guard
requires every `EARTH_*` the engine reads to be in a reference or in an explicit internal list with a
reason. Running it found three *builtin ARGs* missing from the language reference too, one of them
the scrubbed origin URL that exists so a token does not reach a layer. Reflink materialisation
remains unmeasured - the machine this project measures on is ext4, which has none.

**Inception is asserted where it applies** (E402): run inside a real container with the daemon's
socket bound in, a bare block shares that daemon and the socket travels with the decision, and
`--isolate` still starts its own. Those two are deliberately *not* in the CI floor - an Earthly `RUN`
is a container without `/.dockerenv` and without a socket, so they would always skip there, and a
gate counting a test that always skips counts a number that cannot change.

**3b - done, the scheduler.** The gate narrowed from "any docker step" to "any docker step that did
not ask for a daemon of its own", which is the ending its own comment had promised (E384). It reads
`IsolateDocker` rather than `NoCache` on purpose: the interpreter sets both, and checking the same
decision twice is not a check - checking a different field keeps the scheduler's guarantee
independent of the interpreter's.

**3b as originally written - the scheduler.** Dispatch on `Op.IsolateDocker`: isolated gets
`ownDaemonMounts` and a `step.Daemon`; the default reaches the outer socket when
`outerDaemonUsable` says it may, and starts its own when there is no outer one to reach - nesting
by not nesting (E377, E380). Then narrow `schedule.go:1160` from `n.Op.Docker` to
`n.Op.Docker && n.Op.NoCache`, in one commit with the test asserting a shared block is never
served from cache. Until then every WITH DOCKER step re-runs, which is the honest price the
comment there already states.

**Cache sharing across nesting levels.** The outer daemon's `--data-root` is in the outer step's
overlay (or on a cache-mount if `--cache-id` was given). The inner earth's step overlay is
nested inside the outer step's overlay. They are disjoint filesystems. An inner `--cache-id=X`
and the outer `--cache-id=X` name directories under different stores and do not alias.
`WITH DOCKER --isolate` in the inner Earthfile is the only mechanism that connects the two
levels, and it is explicit in the source.

`CACHE --sharing` is implemented in all three modes (E432): `locked` (the default) queues steps on the
named directory, `shared` admits several at once, `private` gives each step its own. Node identity is
now walked by a reflective guard as the chain key already was - the two hashes over `ir.Op` had one
guard between them.

A step whose only mounts are private caches is now delegable (E433): the wire carries the targets
(`Op.Scratch`, version 2) and the worker rebuilds the mounts, so the delegated step is the same step.
Named caches still pin to the invoker - moving those is the data-locality work, and it is next.

`--sharing=locked` is now enforced by the scheduler before a step takes a build slot, not by the
guest after it (E434). The guest keeps its own lock; the two are checked against each other rather
than trusted to agree.

`RUN --mount` now honours `sharing`, `mode`/`chmod` and the bare `readonly`/`ro`, and refuses any
field it does not provide instead of dropping it (E435). Mount modes are in the key and applied to
the staged source, where the step can see them.

Every flag on every command's option struct is now swept for whether anything reads it (E436):
honoured, refused by name, or on a short named list with a reason. `CACHE --chmod` is honoured and
`RUN --push` is planned away rather than run on every build.

The flag sweep's templates now refer to a real target, so `FROM` and `COPY` flags are measured rather
than miscounted, and its fingerprint no longer contains pointer addresses - which had been reporting
every artefact-producing command's flags as honoured (E437). Fourteen flags reach nothing; each is
annotated as deliberate or as a limit of the sweep, and none is unconsidered.

The execution gate now groups its failures by diagnostic and names the files under each, so a run
produces a work list rather than a count (E438). The first two entries are fixed: `ARG` declares a
default rather than assigning, and a base-recipe argument reaches a target only when it is
`--global`.

A `LOCALLY` in a fetched Earthfile is refused, naming the repository it came from (E439, I16). The
rule follows provenance rather than path: through that repository's functions and its other
directories, and not onto a local file that merely refers to one.

`IMPORT` reads its flags before its path (E440), so `IMPORT --allow-privileged <ref>` registers a
name again. The execution gate builds each corpus file inside a copy of `tests/` rather than alone in
an empty directory, which is what `FROM ../+base` needs: 4 of 9 targets build, and the three files
that declare no target are named rather than silently counted against the engine.

A COPY source containing a `+` that cannot be a reference - no artifact path after it - is a filename
when the build context has one (E441). The execution gate attempts 40 files. What blocked that was an unbounded wait: releasing a step's
filesystem, and the handshake, each waited on the guest with no deadline, so a guest that stopped
answering stopped the build (E442). Both are bounded now. Why that guest stopped answering is still
open, as are three `earth-guestd` processes found outliving the tests that started them.

`EARTHLY_CI` and `EARTHLY_SOURCE_DATE_EPOCH` are supplied (E443). The corpus ratchet moved *down*, from
489 to 487 on darwin and 481 to 479 on Linux: two targets branch on `EARTHLY_CI` and, now that it says
`false`, reach an `ARG --required` this caller does not pass. That is the reference's behaviour, and
the number is written down beside the reason.

A target reference splits at the last `+` before the artifact path, which the grammar makes provable
rather than heuristic: a target name cannot contain one (E444). That is what
`COPY ./dir-with-\+-in-it+test/file.txt` needs, and it needs no escape to get it.

The execution gate builds each corpus file's `all` or `test` target where it has one, rather than
whichever target is written first (E445) - several files declare a helper first and the target that
drives it second.

A file's ownership was lost across a capture (E446): committing a layer copies the delta into the
store, and the copy did not ask to keep ownership, so every uid a step set was flattened to the
invoking user. One option on one call; `tests/copy-keep-own.earth` builds now, and the three
boundaries are pinned by a test that names which one would break.

The execution gate reaches 21 of 37 targets. Targets that spend their per-target deadline are counted
and named separately from targets that failed (E447): a cold image pull shares that budget, and
folding the two together had the gate reporting its own clock as an engine defect.

`EARTHLY_VERSION` and `EARTHLY_BUILD_SHA` are supplied, with a real answer in an unstamped build
rather than an empty one (E448) - so `tests/builtin-args.earth` builds to its last assertion.

A step's output no longer loses its last line when that line has no trailing newline (E449) - which
also fixes `ARG v=$(...)` over a command like `cat` or `printf`, since the argument's value comes from
that stream. Next: an argument's value is spliced into the command text, so one containing a quote
changes how the shell parses the line; the reference passes arguments as environment instead.

Argument substitution knows what it is inside (E450): single-quoted text is left alone, as a shell
leaves it, and a value substituted inside double quotes is escaped so it cannot end the author's
string. `$` is deliberately not escaped, which keeps this engine's existing rule that an undeclared
name belongs to the step's shell.

The execution gate builds the whole `tests/` tree, four targets at a time: **49 of 113 build, from
116 files** (E453). It no longer guesses which target a file means - `tests/Earthfile` drives the
corpus with 285 invocations of its own `RUN_EARTH`, naming the file, the target and the arguments,
and all 285 are read (E454). Two things surfaced on the way: an export that looped for ever on a
symlink to an ancestor (E452), and seven sandbox agents outliving the builds that started them.

Five of the ten targets the gate found this engine building against the tree's own `--should_fail`
are fixed: an argument declared twice (E456), the engine's own label namespace and builtin arguments
written by the author (E457), and two constructs used without the VERSION feature that enables them
(E458). Every planning ratchet moved *down* as a result, which is the number doing its job.

Seven of the ten targets the gate found this engine building against `--should_fail` are fixed; the
dialect rules (E458, E459) closed four of them. Two intermittent problems are recorded rather than
fixed (E460): a whole-suite hang in `engine/exec` that will not reproduce alone, and sandbox agents
orphaned by killed test runs - neither of which a deliberate reproduction has yet triggered. Suite
runs now pass an explicit `-timeout` shorter than the harness's, because both observations of the
hang were Go's own timeout masked by the tool's.

All ten targets the gate found this engine building against the tree's own `--should_fail` are fixed
(E456-E461): an argument declared twice, the engine's own label namespace and builtin arguments,
`SET` and `COMMAND`/`FUNCTION` and `PROJECT` used outside the dialects that have them, and a global
declared inside a target. Every planning ratchet moved down as a result - eight times in six
increments - which is what a ratchet is for.

A project's `.arg` and `.secret` files are read, under whatever the invocation was given (E465), with
`--arg-file-path` and `--secret-file` naming them elsewhere. The gate's unattempted list is down from
26 to 15 across three increments (E462-E465), and what remains in it is `--push`, which this engine
refuses everywhere, and preconditions the gate does not reproduce.

`RUN --ssh` mounts the invoking user's agent into a step that asks for one (E466). The operation
carries a bool and the executor finds the socket, because its path is per-invocation and would
otherwise reach the key.

A build can say what it spent (E467): `--exec-stats` reports total CPU across steps and the largest
peak any one reached, measured in the guest because the kernel reports usage to the parent at wait.
Guest protocol 16.

`FROM scratch` is the empty base rather than an image to fetch (E468) - its own opcode, appended
because an opcode's number reaches the key, refused for delegation and speculated on freely.

`--secret-file NAME=path` and `--secret-file-path path` are two options rather than one (E469): a
single secret from a file, and where the project keeps many. Tildes in the first are expanded.

`FROM scratch` clears the working directory as well as the base (E471): its configuration is empty,
so a relative path after it is refused rather than resolved against whatever the recipe had. The
general form - an image's own `WorkingDir` replacing the recipe's - needs the image config at
planning time and is not done.

## Planning that depends on a build result

`FROM DOCKERFILE +gen/` names a target's output as the build context, and - with no `-f` - as the
place the Dockerfile itself comes from. Five corpus targets are written that way and this engine
refuses them by name (E478): it parses the Dockerfile while planning, and planning happens before
anything is built.

Implementing it is not a matter of finding the file. It changes what a plan *is*:

* **A new capability.** The interpreter would need to build a target and read a file out of its
  output, mid-plan. The seam exists in shape - `WithCommands` already lets planning run something
  to resolve a value it cannot compute - but this one has to reach the scheduler, not a shell.

* **A question for the specification first.** A plan derived from an artifact is reproducible only
  if the artifact's own key is part of the derived plan's key. Otherwise two builds with different
  producing targets can key the same, which is a cache that returns another build's answer -
  the failure I1 exists to prevent. §4.4 says what a chain key covers; this adds a term to it.

* **A bound worth stating.** The Dockerfile has to be *materialised* to be parsed, so this
  construct cannot be planned without executing part of the build. Any engine that offers it gives
  up "plan fully, then run" as an invariant. That is a real trade and it should be written down as
  one rather than discovered by a reader wondering why planning started a container.

Sequenced after the observation work rather than before it: this is five targets and a
specification change, and S5 is the milestone the plan is actually on.

### The specification change turned out not to be one (E487)

The worry above is that a plan derived from an artifact is reproducible only if the artifact's own
key is part of the derived plan's key. **It is stronger than that and needs nothing added.** The
Dockerfile's *content* is parsed into the nodes it describes, so every derived node's key covers it
directly - a different Dockerfile is a different graph, not the same graph with a different
provenance term. §4.4 stands as written.

What is real is the other half: **planning stops being a pure function of the source.** That
boundary already exists and is already named - `WithCommands` crosses it for a condition the plan
cannot decide, `WithRemotes` for a repository it cannot reach - and this is a third capability of
the same kind rather than a new category. A caller who supplies nothing is refused with
`ErrNotProvided`, which says the capability was withheld rather than that the engine lacks one.

Done: the seam, the reclassification, and the tests. **Not done: the caller.** No caller supplies
one yet, so the six corpus targets are still refused - by a message naming what to pass rather than
one naming a gap. Wiring `cli.Run` to build a sub-target and export its artifacts is the next step,
and it is ordinary work: plan, schedule, `Executor.Export` to a temporary directory.

## Bind mounts: refused, and why that is a position rather than a gap

`RUN --mount=type=bind-experimental,source=<host path>,target=/x` gives a step a writable window
onto the machine running the build. `tests/host-bind.earth` writes through one.

This engine refuses it **on purpose**. Two decisions already made say the same thing in other words:

* a step's writes are held to its own layer (green paper A3);
* `SAVE ARTIFACT --force` is refused because this engine never writes outside the project - checked
  in the interpreter, in the CLI, and again at the point of writing, where symlinks are resolved so
  the position cannot be walked around.

A bind is that hazard by a different door, and the door is wider: the step decides what to write and
when, with no artifact declaration and nothing in the plan to say it happened.

**What it costs.** One corpus target, and a real capability: mounting a large source tree read-only
is faster than copying it. That is worth revisiting, and the revisit is narrower than the flag -
a *read-only* bind whose source is inside the project changes nothing about what a build produces,
because the project directory is already an input. What cannot be allowed is the writable case and
the outside-the-project case, and the current refusal covers all of them because the corpus only
exercises the one that must be refused.

**Why the label matters.** It was refused as *unimplemented*, so both sweeps counted it as work
somebody should do - and the work would be reversing a position. The three sentinels are how the
engine says which kind of refusal it is making, and a decision filed as a gap is an invitation
(E485).

## Why Κ₂ never hits for a RUN on darwin

Confirmed to the byte (E493, E494). The two digests `WhyStale` now prints are

```text
/bin/cat changed in the base (observed 5c99e44af20e, base has fa1e4829c3de)
```

and digesting the layer's own `/bin/cat` on the host reproduces them exactly:

| how the owner is read      | digest         |
| -------------------------- | -------------- |
| as stored - uid 501, gid 0 | `fa1e4829c3de` |
| **uid 501 read as 0**      | `5c99e44af20e` |

The store's files are owned by the invoking user. The sandbox shares that store into the VM with
everything owned by **root**, and the guest's `OwnIDMaps()` reads `/proc/self/uid_map` - which
inside the VM is the identity, because the shift was done by the sharing mechanism and not by a user
namespace. So the guest hashes uid 0 where the host hashes uid 501, for **every file in the base**,
and the L2 tier can never agree with an observation on darwin.

This is E133's failure class arriving through a door E133 could not see: there, the mapping existed
and was not applied; here, the mapping is real and has no `uid_map` to be read from.

### The fix, and why the smaller one may be the right one

Two shapes:

* **Tell the guest.** The host sends its own uid in the handshake and the guest treats "0 as seen"
  as "that uid in the store". A protocol change, and a lie for any file the *step itself* created as
  root - the mapping is about the shared store and would be applied to everything the guest reads.

* **Digest the view as the guest sees it.** `stackView.Digest` is compared against nothing but guest
  observations, so both sides can use the guest's convention. No protocol change, no claim about
  files the guest did not get from the store - but the host must know what the sandbox does to
  ownership, which is a property of the sandbox and not of the store.

The second is smaller and says what the comparison is actually about: **Κ₂ compares what a step saw
with what a rebuilt step would see**, and both of those are inside the sandbox.

**Done** (E494). `LayerStore.SeenAsRoot` reads the store the way a sandbox sharing it as root does,
and the darwin sandbox says that it does through an optional interface. The build that had never
served a RUN from the tier now reports

```text
cache   3 hit, 1 miss, 1 by observed inputs, 1 unpredicted
```

And the corpus sweep, which had never run a step (E496), now measures what the tier is worth on real
targets:

```text
8 targets, 8 with at least one step reused by observed inputs, 28 such steps out of 65 attempted
```

Only this view moved. A layer's own identity is still hashed with the store's ownership, which is
right: that is a fact about what was stored, and this is a question about what a step saw.

## Workers on macOS and Windows, and what LOCALLY means then

`cmd/earth-worker` has no backend but Linux: `sandbox_other.go` refuses with "this platform has no
worker backend yet". A fleet is therefore Linux-only, and that is a smaller decision than it looks.

**It should not be.** A worker on macOS and one on Windows are reasonable, and the reason is
`LOCALLY`: a step that runs on the invoking machine outside any sandbox can only be run by a machine
of that kind. A fleet of one OS can run `LOCALLY` for that OS and refuse it for the others, which is
the same build failing for a reason about the fleet rather than about the Earthfile.

### What is already in place

* The Apple sandbox is the local engine's own backend, so a darwin worker is `exec.New(appleSandbox)`
  with a store of its own - the wiring `sandbox_linux.go` already does with `exec.NewNative()`.

* **Placement by platform already refuses the wrong machine.** The scheduler's affinity rule declines
  a node whose platform a worker does not declare (§4.7.1), so a `linux/arm64` step cannot land on a
  darwin worker by accident. The mechanism that makes a mixed fleet safe is the one that already
  makes a single-platform fleet correct.

* A worker is an engine (C.3), so every invariant in §5 binds it as it binds the parent. Nothing
  about that is platform-specific.

### What has to be decided

1. **What a darwin worker offers.** Two different things wear the same word. A darwin *VM* runs
   `linux/arm64` steps exactly as the local engine does - so a darwin worker could take ordinary
   Linux work, and that is the easy half. A darwin *host* runs `LOCALLY` steps as macOS, which is the
   half that needs the platform to reach placement as something other than `linux/*`.
2. **Windows has no third option.** There is no Linux sandbox to fall back on without WSL2 or a VM,
   so a Windows worker is a `LOCALLY`-only worker until one exists. That is worth having on its own -
   a Windows build step that must run on Windows has nowhere else to go - and it means `Confines()`
   is false for it, which the scheduler already understands.
3. **A `LOCALLY` step is not cacheable across machines**, and this is where the mixed fleet earns a
   specification sentence rather than a code change: what a host step reads is not a base this engine
   assembled, so Κ₂ has nothing to compare and Κ₁ describes a machine rather than a filesystem. The
   honest position is that a `LOCALLY` step is placed by platform, run, and not reused - which is
   what a single-machine build already does with it.

Sequenced before the fleet speedup work, because a multi-worker measurement on macOS cannot be taken
without a macOS worker.

## Privileged steps on a privileged fleet

`RUN --privileged` is refused on purpose (E420), and so is `--mount=type=bind` (E485), on the grounds
that a step's writes are held to its own layer and a step does not reach the host.

**That should be conditional on how the engine was started, not absolute.** An operator who runs the
driver and the workers in privileged mode has said what they are prepared to allow; refusing anyway
is refusing a capability the machine has been deliberately given. The reference has the same shape:
`--allow-privileged` is a *permission the invocation grants*, and this engine accepts it and grants
nothing (E476).

The design that keeps the invariants:

* **The worker declares it, not the Earthfile.** A worker started privileged advertises that it will
  run privileged steps. A step asking for one is then a placement constraint like any other - the
  affinity rule already refuses a node no worker can satisfy, and the diagnostic §4.7.1 requires
  already says which constraint could not be met.

* **A fetched Earthfile still may not have it.** I16 is not about the machine's capabilities, it is
  about who chose the command: a step from a repository somebody else can push to must not run
  privileged because a worker happens to allow it. `--allow-privileged` is the caller's grant and has
  to be present for a *remote* target, exactly as the reference has it.

* **Privileged results are cacheable, and that is the uncomfortable part.** A privileged step can
  read the host in ways Κ₁ does not describe, so its result is not a function of its inputs (I3).
  Either such a step is not cached, or the fact that it ran privileged enters ω - and the second is
  weaker than it sounds, because the *capability* is in the key while what it did with it is not.
  Not caching them is the honest default and matches how `own(c)` daemons are handled (§3.4b).

The engine's current refusal is not wrong today - nothing declares the permission, so there is
nothing to honour - and it is written as a decision rather than a gap, which is what makes reversing
it a design change rather than a bug fix.

## A worker announces what it is when it joins

Placement refuses a worker that has not declared a platform, and a worker declares one by echoing
the platform of an assignment it has run (E503). A fresh worker can therefore never be given a first
step, and the echo means the declaration proves nothing anyway.

Both are fixed by the same change: **a worker announces its platform and capacity when it joins**,
before it has run anything, and the announcement is about the worker rather than about a question it
was asked.

* The worker opens one stream on arrival carrying a hello - platform, capacity, and where it serves
  layers - which is the information the driver currently learns from a reply, at a moment when it is
  too late to be useful.

* `Rendezvous.add` records it, so `Inventory()` names a worker that can be placed on immediately.
* `Reply.Platform` stops being an echo. Either it is dropped, or it becomes the worker's own answer
  and disagreeing with the announcement is a worker that has changed under the driver's feet -
  which is a refusal, not a correction.

* A worker that announces nothing is a worker that gets nothing, which is today's behaviour and
  stays: **refusing to guess costs a slower build; guessing costs a wrong one**, and that reasoning
  is right even though the mechanism it protects has never fired.

The protocol gains a message, so this is a version bump. It is the last thing between the current
engine and a fleet that does any work, and it is a prerequisite for measuring whether a fleet is
faster - a question this plan has answered so far only for the scheduler.

## Conformance: a layer travels under its own digest

Not an open decision. §2.2, §3.2 and §3.3a already settle it, and the implementation does not do
what they say - so this is a defect to be worked off rather than a design to be chosen (E507).

The specification's shape is the one every distributed build converges on: a content-addressed store
keyed by digest, and a separate map from cache key to the digest of the result. `𝔅 : 𝔻 ⇀ 𝔹` with
(2.2) `ℋ(𝔅[𝑑]) = 𝑑` is the first; `𝔄 : 𝕂 ⇀ 𝔸` is the second. This engine collapsed them into one
directory named by the cache key, which is why a peer cannot check what arrives.

| defect                                                               | violates          |
| -------------------------------------------------------------------- | ----------------- |
| layer directories named by node id rather than `ℓ_id`                | §2.2, §3.2, §3.3a |
| the image cache describes itself as content-addressed *by reference* | §3.2              |
| ~~a mutable reference is never pinned~~ - done, E508                 | §3.4d, I3, I17    |

**What the store change costs.** Layer lookup gains one indirection (key to digest, then digest to
tree) and gains deduplication for free, since two derivations producing identical output become one
entry. Existing stores are named the old way, so it wants a store-layout version rather than a
rename in place: an old store is not wrong, it is a different shape, and the safe migration is to
let it age out.

**What it buys.** A fleet can share a base. Today every machine fetches its own, which is the cost
E507 measured and the reason a fleet's second machine is worth less than it should be.

## Open question: the store as a directory, or as a disk

The layer store is a host directory shared into the sandbox. Everything below follows from that one
choice, and it is worth asking once, deliberately, whether it is the right one.

**What it costs, measured.** A build of `FROM golang:1.26.5-alpine3.24` and `RUN go version` - which
reads perhaps a dozen files of its base - leaves the sandbox holding **10,813 file descriptors**
against a store of 15,252 files. The count tracks what is *in the store*, not what the step used. A
cold `+deps` reaches about 40,000. Nothing reaps a sandbox whose build was killed, so a dozen
interrupted builds exhaust a machine's system-wide limit, and the failure surfaces as an unrelated
step reporting `too many open files in system` or hanging at no CPU (E510).

**What else it costs, already recorded and not previously connected:**

| symptom                                                      | recorded as              |
| ------------------------------------------------------------ | ------------------------ |
| uid and gid lost, so `--keep-own` cannot work                | E84                      |
| a whiteout is a character device and `mknod` returns `EPERM` | E88, E94                 |
| a stored layer never re-digests to its own name on macOS     | E89, reopened 2026-08-21 |
| one descriptor per file in the store, held by the sandbox    | E510                     |

Four unrelated-looking defects with one cause: **the store's contract is "a filesystem that can hold
a Linux layer", and a host directory shared into a VM is not one.** Each was found the hard way, by
implementing something that then could not work.

**The alternative is a disk image.** A block device attached to the sandbox, with a Linux filesystem
the guest owns. The host holds one descriptor regardless of how many files the store has; uids, gids,
device nodes and mtimes are native, so a layer digests to its own name; whiteouts need no
translation.

**What that costs, and it is not nothing.** The host can currently read the store directly, and two
things depend on it: `placeCaptured` captures a materialised tree host-side, and `Layers.Get` packs a
layer host-side to serve it to a fleet peer. Both would have to go through the guest, which turns a
filesystem walk into a protocol. The image also needs a size, and a size is a thing to get wrong -
either wasted or exhausted, with growth to implement either way.

**Measured, 2026-08-22 (E541).** The transport is not the constraint. The guest streams to the host at
375-386 MB/s, against the 307 MB/s at which the host currently hashes a placed image - faster than the
work it would feed. And the walk a disk would replace is not free today: reading 5,000 small files
costs the *guest* 219µs each over virtiofs against 64µs on `/dev/vdc`, plus a host descriptor per entry
it looks up.

So the shape of the decision has changed. It was framed as a trade - lose direct host reads, gain
descriptors and metadata - and most of it is not a trade: the cost was already being paid on the other
side of the boundary, where nobody had measured it. What remains genuinely open is sizing the image
and growing it, and moving `placeCaptured` and `Layers.Get` behind the protocol.

*A cost that appears in four places is usually one cost.* Each of the four was investigated on its
own terms and none of the investigations found this, because each stopped when its own symptom was
explained.

## Cache mounts: two changes, in this order

They are usually discussed as one thing and they are not. The first is measured and costs nothing
semantically; the second is a design change with a stated regression. Doing them in the wrong order
means arguing about the second while the first is what everyone is feeling.

### 1. Move the storage off the host share

A `--mount type=cache` lives in the shared store because it has to outlive the build, and that is
the whole of E511: the same `go mod download` takes 5.3s on the host, 30s in the sandbox writing to
guest-local scratch, and over 380s writing to a cache mount on the host store. Metadata operations
through a host directory share cost an order of magnitude more than the writes they accompany, and
Go's module cache is rename- and chmod-heavy.

**A cache mount does not need the host to see it.** It needs to outlive the build, which a block
device attached to the sandbox does equally well. Nothing in §3.3c is about storage, so `locked`,
`shared` and `private` all keep their meanings exactly.

This is the same question as "the store as a directory, or as a disk", reaching the same answer from
a different direction and with a larger number attached.

### 2. Then model a cache mount as layers with a pointer to the top

A stack of content-addressed layers with a mutable `latest`, materialised by overlay like any base,
with a step's writes landing in its own upper.

**What it buys is the fleet, and the argument is §0.0 rather than speed.** Today every worker
downloads its own 450MB module cache: N times the bytes, N times the energy, for a byte-identical
result. As layers it is fetched the way a base is - once, and then shared. It also gives a build a
consistent snapshot rather than whatever half-written state another build is mid-way through, which
is a correctness improvement obtained as a side effect.

Two mechanisms this needs are already specified, which is some evidence it fits: Φ (§4.6) for
flattening a stack that otherwise grows a layer per build until overlayfs objects, and I7's
last-writer-wins for "manifest or tag update", which is what `latest` is.

**What it costs is `shared`, specifically.** §3.3c says μ enters Κ₁ *because it changes what the step
sees*:

| mode      | under layering                                                           |
| --------- | ------------------------------------------------------------------------ |
| `private` | unchanged - a fresh upper, discarded with the step                       |
| `locked`  | equivalent - one step at a time, so a snapshot *is* the live state       |
| `shared`  | **changed** - concurrent steps see each other today; snapshots would not |

Two steps in one build would each download the same module rather than one benefiting from the
other. Not incorrect - cache contents bound no key by §4.4 - but it is a regression in exactly the
case `shared` exists for, and §3.3c would have to say so rather than leave a reader to find out.

**Not decided.** The first is a measurement waiting to be acted on; the second is a trade that wants
somebody to decide whether intra-build sharing or cross-machine sharing is worth more, and the answer
probably differs between a laptop and a fleet.

## Decided: a host share is not the default storage

**Decision, 2026-08-21.** The layer store and cache mounts stop defaulting to a directory shared from
the host. Guest-owned storage - a block device attached to the sandbox - becomes the default, with a
host share available for the cases that need the host to see the bytes.

The case for it is cumulative rather than any single number, which is why it survived one of those
numbers being wrong:

| what the share costs                                                          | where         |
| ----------------------------------------------------------------------------- | ------------- |
| uid and gid lost, so `--keep-own` cannot work                                 | E84           |
| a whiteout is a character device; `mknod` returns `EPERM`                     | E88, E94      |
| a stored layer never re-digests to its own name on macOS                      | E89, reopened |
| the sandbox holds one descriptor per file in the store - 40,000 for one build | E510          |
| 1.5x to 6.5x on file operations, by operation class                           | E511          |

The last one was first reported as twelvefold and corrected; the decision does not rest on it. Four
of the five are correctness costs that no amount of tuning removes, and three of them have already
been worked around once each, in three different places, by three different mechanisms.

**What it does not fix.** The host currently reads the store directly, and two things rely on it:
`placeCaptured` captures a materialised tree host-side, and `Layers.Get` packs a layer host-side to
serve it to a fleet peer. Both become guest-mediated, which turns a filesystem walk into a protocol.
That is the work this decision buys, and it is not small.

**What to keep from the share.** Nothing about the *image cache* needs to move: it is written by the
host, read by the host, and only its materialised output ever reaches a guest.

Sequenced after the two cache-mount entries above, which this subsumes: moving cache mounts off the
share *is* this change, applied to the storage that showed the cost first.

### What the prior art says, and it agrees

Researched rather than assumed, because "host shares are slow" is the sort of thing everyone repeats
and nobody sources.

**Nobody has made a host share fast for metadata; everyone has worked around it.**

| project        | what they did                                                                          |
| -------------- | -------------------------------------------------------------------------------------- |
| Docker Desktop | osxfs (~10x native for metadata) then gRPC-FUSE (still ~10x) then VirtioFS (~3-4x)     |
| Docker again   | Synchronized File Shares: a Mutagen replica *inside* the VM. A cache, not a transport  |
| Lima / Colima  | `vmType: vz` + `mountType: virtiofs`, and documented advice to move caches into the VM |
| OrbStack       | custom VirtioFS with its own caching layer                                             |

Two things follow. First, our measured 1.5x to 6.5x is *ordinary* for virtiofs rather than evidence
of something being held wrongly - which is a second, independent reason the twelvefold claim in E511
was suspect. Second, the escape hatch everyone converges on is the decision above: put the bytes
where the guest owns them.

### What `go mod download` actually does, which is more than expected

From the Go source, per module version, on a cold cache:

* `.info`, `.mod` and `.zip` each written to a temporary name and renamed, under a `flock`;
* the zip is read back **twice** - once to validate, once to hash;
* `rewriteVersionList` does an `os.ReadDir` of the `@v/` directory *per module*;
* extraction opens every file `O_EXCL` with mode `0444`;
* and then `makeDirsReadOnly` walks the whole extracted tree **again**, chmod-ing every directory.

For `golang.org/x/sys` alone that is 549 file creations across 17 directories, then a second full
traversal of the same tree. On a filesystem where a metadata operation costs a VM boundary crossing,
the second traversal is not free.

**A knob worth testing: `GOFLAGS=-modcacherw`.** The final walk is gated on `!ModCacheRW`, so the flag
skips it entirely. **[UNVERIFIED]** - the machine failed its own quietness gate before this could be
measured, and an unmeasured optimisation is a rumour. It is cheap to test and it applies whatever is
decided about storage.

No `fsync` is called anywhere in that sequence, which rules out the other obvious hypothesis.

## Direction: the store says where, not what

The store holds bytes and every consumer reaches into it. The alternative is that it holds an
*index* - digest to where a copy can be found - and whoever needs the bytes fetches them to where
they are needed. Popular content is promoted to somewhere central; the rest lives wherever it landed.

**Most of this exists.** A fleet worker already fetches layers by digest from peers (Appendix C.4),
verifies them on arrival, and serves what it holds. What is missing is that the *local* guest is not
a peer: it reads bytes through a directory shared from the host, which is the one arrangement that
needs neither an index nor a fetch and is also the slowest thing measured this week.

**It is safe because the store is content-addressed, and only because of that.** A location is a
hint in the sense I5 means: it may be absent, stale, or wrong in either direction, and the build is
unaffected, because (2.2) says the bytes are checked against the digest that named them. An index
that lies costs a wasted fetch. An index that is empty costs a slower one. Neither can produce a
wrong artefact, and that is what makes the whole shape affordable.

**It resolves the objection to moving the store.** The cost recorded above was that the host loses
direct access, so `placeCaptured` and `Layers.Get` become guest-mediated - work with no benefit
attached. Under this model that mediation is not a cost being paid, it is the mechanism: the guest
holds what it fetched and serves it like any other peer, and the host asks for it the same way a
worker on another machine would. One path instead of two.

**And it takes E513 as its caching rule rather than contradicting it.** Copying a whole base image
to fast storage before a step reads a few hundred of its files was measured and is a loss. "Fetch
what is needed, promote what is used repeatedly" is that finding as a policy: a copy has to earn its
place, and the thing that earns it is being asked for more than once.

### What it would cost, honestly

* **A cold build depends on somebody having the bytes.** Today a local store is a floor: if it is
  there, the build runs. An index that resolves to nowhere is a build that fetches from a registry,
  which is slower and needs the network. The floor has to be rebuilt as a policy - what is always
  kept locally, and why.
* **Garbage collection gets harder.** Deleting the last copy of something the index still names is a
  dangling reference, and the index cannot be authoritative about liveness without becoming state
  that must be correct - which is exactly what §2.3 says derived state must not be.
* **Promotion needs a counter**, and a counter is derived state. It must be able to be wrong, lost or
  reset without changing a result, like 𝔐 and 𝔇 (I5). That is a constraint on the design, not an
  afterthought.
* **Fetching per step rather than per build** changes the scheduling picture: a step's first read may
  block on a transfer, which is exactly what the fleet's prefetch and placement machinery (§4.7.1)
  already exists to hide. It would need to serve local steps too.

Not a decision. It is the direction the fleet work has been walking towards from the other end, and
the thing that makes the storage question a design rather than a chore.

### Six handlings of one image, and which are avoidable

"Move the data less" is a principle (§0.0); this is the arithmetic behind it for one cold build of a
267MB base.

| #   | handling                                                    | avoidable?                                    |
| --- | ----------------------------------------------------------- | --------------------------------------------- |
| 1   | registry to image cache - a write                           | no, it has to arrive                          |
| 2   | image cache to layer store - a clone, 0.26s                 | yes, if they were one store                   |
| 3   | **capture: a full read and hash, to learn its name**        | **yes - the name was knowable as it arrived** |
| 4   | the guest reads it through the share, per file              | yes, if it landed where the guest reads       |
| 5   | a fleet transfer packs it and sends it                      | no, it has to cross                           |
| 6   | **the receiver unpacks and re-hashes it to learn its name** | **yes - the sender already knew**             |

Three and six are the same mistake: a name recovered by reading bytes that had already been read.
Content addressing makes the cheap version possible - a digest taken *while* the bytes stream past
costs nothing, and the same digest taken afterwards costs a full pass.

E509 added handling 3 deliberately, to fix filing a layer under a name that described its derivation
rather than its contents, and that was the right fix for that problem. The cheaper fix is to compute
the digest during the unpack that is already happening rather than in a walk afterwards, and the
`.layer` sidecar beside the image-cache entry is already most of the way there: it remembers the
answer so the second build does not re-derive it. What it does not yet do is avoid deriving it the
first time.

Two and four are the storage question. Six is a protocol question and it is the one with a fleet
attached: every worker that receives a base pays a full hash of it, and there may be many.

### Unpacking only what is read: mostly built, and off

Packing and unpacking whole layers is the dominant handling, and the remedy is the one every
lazy-pull format implements: place a layer without its contents and fetch a file when something opens
it. The specification already anticipates the formats (§3.2 names eStargz, zstd:chunked and nydus as
*encodings* rather than identities) and already specifies the thing that makes lazy placement pay -
masks, which record which files a step actually reads (Appendix A).

**The machinery is here.** A fleet worker fetches fragments rather than layers today: `WithFragments`
"makes a worker fetch only what a step is predicted to read", the guest has a fills channel, and
`ServeFills` answers faults from the host.

**It is off where it would help most.**

| path                  | lazy fault-in                                 |
| --------------------- | --------------------------------------------- |
| fleet worker          | yes - fragments, predicted from masks         |
| Linux native sandbox  | channel wired, fills served                   |
| **darwin VM sandbox** | **not wired** - no `EARTH_GUEST_FILLS` at all |

So every measurement in this document taken on macOS materialised each base image whole, eagerly,
through the slowest transport available, while the code to avoid that sat one environment variable
away from being reachable.

*A capability built for the hard case and never turned on for the easy one.* Fault-in was written for
a worker fetching across a network, where the saving is obvious. The local guest reading a shared
directory is the same problem with a shorter wire, and it was never connected - the comment in
`guest.go` says "nil for every build today" and has been right for every build since.

**What is actually missing**, as opposed to unwired:

* nothing *decides* to place a layer sparsely on a local build - the materialiser always places the
  whole tree, so there is nothing for the fills channel to answer;
* the prediction that makes it worth doing is 𝔐, and a mask has to exist before the first build that
  benefits from it - the first build of anything pays full price and learns;
* and a fault-in on the local path must be cheaper than the read it replaces, which over a shared
  mount it plainly is and on a local disk it plainly is not. It is a property of the transport, not
  of the engine, so it wants to be a decision the sandbox makes rather than a constant.

This is the cheapest of the open storage questions, because the answer is mostly wiring rather than
design, and it is the one that most directly serves *move the data less*: a base image that is never
read is never moved.

## The store as a block device is a correctness item, not a speed one

It has been in this plan as a way to make the store faster. E539 makes it a way to make builds work.

The store is shared into the VM as a *directory*, so the host holds one descriptor per file the guest
touches, for as long as the VM runs - 65,331 of them after a few builds, against a `kern.maxfiles` of
491,520 and a `kern.maxfilesperproc` of 245,760. One VM may legitimately take half the machine. Two
that have each touched a whole store exhaust it. A build then fails in an unrelated place with a
message that names a file in `node_modules` and nothing about the cause.

A block device removes the whole class: the host holds one descriptor for a disk image. Lazy
placement attacks the same number from the other end, since the count is exactly how much of a base
was materialised.

Neither is scheduled here. What changes is why they are worth doing: this is no longer an argument
about milliseconds.

## A stopped sandbox is restarted, not rebuilt

A VM this engine can reuse is found by name, and the name is a digest of its mounts. When the named
VM exists but is *stopped* - which the idle timeout makes routine - `Start` calls `container run`
anyway, lets it fail on the name collision, then removes the VM and boots a fresh one. So the common
case of coming back to a machine after lunch pays a failed call, a removal, and a full boot, and
throws away a warm volume to do it.

`container start <name>` exists and does the obvious thing. The change is small; what it needs is a
measurement, because a boot is 620-700ms (E19) and a restart is only worth wiring if it is materially
under that.

**The related question is deliberately not being answered here.** Content-named VMs are never reaped
(`TestAContentNamedVMIsNeverReaped`), on the ground that a stopped VM may be another project's warm
sandbox and reaping it takes that project's cache away. The consequence is unbounded: 140 stopped
records and 55 volumes totalling 32GB accumulated on the development machine, because a name is a
digest and every configuration change mints a fresh one. Bounding it means an age policy - a stopped
VM untouched for longer than some period is dead weight whoever owns it - and that is a decision with
a real trade-off rather than a bug to fix, so it belongs here rather than in a hurried commit.

## A declaration is a stack element (§3.2a)

A worker running `RUN go version` on a golang base fails with `/bin/sh: go: not found`, holding every
byte of the toolchain. What it does not hold is `PATH`, which the image declares and which reaches a
step through `Executor.baseConfig` - a read of `layers/<base[0]>.config.json` in the local store. The
fleet moves layer trees. Nothing moves that file, so on a worker the read fails, `baseConfig` returns
an empty configuration, and the step runs with the engine's floor `PATH` alone.

Three fixes were considered and two rejected.

**Carrying the environment in the assignment** is what buildkit does - `applyFromImage` folds the
image's `Env` into the LLB state at conversion (`earthfile2llb/converter.go:3175`), so a worker is
told rather than deriving. Rejected here because a delegate is an engine (C.3): it materialises from
its own store and never learns how the base was built, and an environment supplied by the driver is
unverified, persists if filed, and is not covered by the digest that makes everything else here
checkable.

**Making the declaration part of a layer's identity** - `id(ℓ) = ℋ(declaration ‖ manifest)` - is
correct and too expensive: it invalidates every digest this engine has computed, and it makes two
images with identical filesystems and different declarations two copies of one tree.

**A declaration is its own stack element**, which is what §3.2a now says. Everything falls out of the
existing machinery: it travels because stack elements travel, it is keyed because ids(𝑏) is keyed, it
is tiny so it materialises whole while the tree behind it may still arrive lazily, and two images
that differ only in what they declare share their filesystem and stay distinct.

**What it needs:**

* a store object that is a declaration rather than a directory, and a `Has` that knows the
  difference;
* a materialiser that folds a declaration into the environment instead of into the lower stack -
  where the hazard is `MkdirAll` in `Materialise`, which today creates an empty directory for any
  stack element the store does not hold. A missing layer therefore materialises as one that
  contributes nothing, which is the shape of the bug being fixed. I18 exists for this;
* `Θ` to yield the declaration alongside the digest, since resolution is where an image's
  configuration is already fetched;
* `baseConfig` to read the stack rather than `base[0]`'s sidecar, after which driver and worker run
  the same code over the same inputs.

**What it costs:** base stacks change shape, so every image-based key changes and those steps re-run
once. Layers keep their identity, so nothing is re-fetched - the cost is a re-key, not a re-download.
Fragments are unaffected: a declaration is small enough that a partial one is not worth expressing.

**And an Earthfile's own `ENV` is a declaration too.** One mechanism, not one for what an image
declares and another for what a build declares: they say the same kind of thing about the same step,
and composing them by two different rules is how the two came to disagree in the first place. It is
not required to fix delegation, and it is the reason to do this rather than the smaller thing.

The specification asked for it. §4.4 names ε its weakest point - what is ambient must be enumerated
correctly or a key is silently wrong - and lists environment variables first among what ε must
carry. A declaration is not ambient: it is an input, named by its content, and it reaches every key
derived from the stack whether or not anybody remembered to enumerate it. §3.2a now says so and §4.4
no longer lists environment variables.

Three things the implementation must not get wrong, each now an invariant or an equation:

* a declaration is stored **as written, before expansion** (3.10). `ENV MYPATH=hello:$PATH` expanded
  at the point it is written down names its own base, so the same line on two bases would be two
  elements; expanded in the fold it is one element that means the right thing on both - and the fold
  is the only place the value of `$PATH` is known anyway;
* a secret is never a declaration (I19). Declarations are stored, content-addressed and shared, so a
  secret value in one is published to every machine that materialises the stack. ε keeps secrets by
  identity and keeps them;
  **I19 was weakened deliberately, and here is the weakening.** It used to say a secret is never
  written down. It now says a secret's *value* is never written down, and permits one thing derived
  from a value into a cache key: a MAC over the secret's name and value under a fleet key, and only
  where the invocation supplies such a key. Absent one, nothing changes and a step holding a secret
  is uncacheable, as before. The reason for the concession is that the old rule made every
  authenticating build pay for its credential on every run; the reason it is a MAC and not a hash is
  that an unkeyed digest of a credential is an oracle against a shared cache (E742). What is not
  conceded: the interpreter is still never handed a value, so a credential in the graph stays
  unrepresentable rather than merely unwritten, which is the level-1 half of the invariant and the
  half worth keeping;
* an element that declares is not an element that is missing (I18).

Sequencing, once the model is settled: declarations in the store and the materialiser first, because
everything else needs somewhere to put them; then `Θ` yielding an image's declaration with its digest,
which fixes delegation; then the interpreter emitting `ENV` as an element, which is what makes it one
mechanism and re-keys every build that sets one.

## Parked: keying on the declarations a step actually reads

Once declarations are elements, a step's key covers every one of them, so an `ENV` nobody reads
invalidates everything above it. The obvious next move is to key on what was *read* rather than on
what was in scope - Κ₂ for declarations, exactly as it already works for paths.

**The model needs nothing new.** An environment is a namespace, and the observation set already has
the right three shapes for one: 𝑅 for what was read, 𝑁 for a name looked up and absent, and 𝐷 for an
enumeration, keyed by the digest of the whole listing. A program that scans for `CARGO_*` enumerates,
so it lands in 𝐷 and depends on the entire set including which names are *not* there - which is
correct rather than a defect. The awkward case is the one the vocabulary was built for.

**The blocker is mechanical and specific.** Reading an environment variable makes no system call:
`environ` is memory the kernel populated at `execve`, and `getenv` is a library walk over it. The
tracer here sees system calls, so it sees nothing at all - unlike a path, which cannot be read
without asking the kernel. Worse, Go's runtime copies the whole environment at startup before `main`,
so any shim at the `getenv` level would report "everything, immediately" for every Go program, which
is most of what this engine builds.

Routes, if it is ever worth revisiting: an `LD_PRELOAD` shim (blind to static binaries, to Go, and to
anything walking `environ` directly), eBPF uprobes on `getenv` (the same blind spots), or protecting
the pages holding the environment and catching the faults (the kernel chooses where they land, and
Go's startup copy would touch all of them anyway).

**Unverified:** the intended check - `strace` a Go binary reading a variable and confirm no system
call names it - did not run, because the machine was unreachable. The reasoning above is from the
mechanism rather than from a measurement, and should be confirmed before anybody spends a week on it.

**Differential testing does not substitute for observation, and the reason is worth keeping.** The
idea is obvious and good: run the step once with the whole environment and once with a minimal one,
and if the results agree, the variables that differed did not matter. It needs no tracer at all.

It cannot key a cache, because it proves the wrong statement. Observation of a read is a claim about
what the execution *did*: a step that never read `CARGO_HOME` cannot have been affected by it, and
that holds for every value it might have had. Two runs that agree are a claim about the two values
tried. The first is universally quantified and the second is not, and a cache needs the first - it is
about to reuse this output against an environment nobody has run yet.

**The absence case is where it fails outright**, and it is the common one. A step that behaves one way
when `CI` is unset and another way when it is set is ordinary. Run it with a full environment and a
minimal one and `CI` is unset in both, so the results agree and the conclusion is "`CI` does not
matter" - which is exactly wrong, and wrong in the direction that serves a stale output. The engine
would then reuse that layer on a machine where `CI` *is* set.

Observation has an answer to that and this does not: a lookup that found nothing is recorded in 𝑁
(§3.4), so "I asked for `CI` and it was absent" is part of the key and setting it later is a miss. A
differential run cannot record what it never varied, and varying every name is not a thing anyone can
enumerate.

So the technique has a home, and it is the one this specification already built for claims that
cannot be trusted: 𝔇, determinism beliefs, hint-only by §2 and droppable by I5. Differential runs
could say "this step looks insensitive to its environment" as advice - worth having for screening, a
warning, or deciding what to try next - and must never say it to a key.

## The store as a disk: how to get there without a broken fortnight

E541 settled the argument. This is the route, and the ordering is the whole of it: **the abstraction
moves first and the storage second**, so that at no point is there a tree where half the engine reads
a directory and half reads a disk.

Six capabilities reach into the store host-side today. Each assumes it can join a path and walk:

| capability                | where                | what it does                          |
| ------------------------- | -------------------- | ------------------------------------- |
| `Has`, `Verify`           | `exec/layerstore.go` | is this layer here, and is it whole   |
| `placeCaptured`           | `exec/exec.go`       | file a captured tree under its digest |
| declarations and sidecars | `exec/exec.go`       | read what an image declared           |
| unpack and link           | `exec/imagecache.go` | put a pulled image into the store     |
| `Squash`                  | `exec/squash.go`     | merge a range of layers               |
| `packImage`               | `exec/packimage.go`  | write an OCI layout out of layers     |
| `Layers.Get`/`Put`        | `fleet/layers.go`    | serve a layer to a peer               |

### Phase 1 - name the operations

An interface with exactly those methods, and a directory-backed implementation that is the code that
exists now, moved. Nothing changes behaviour; every test that passes today passes after. The value is
that the store stops being "a path everybody knows" and becomes a thing with a surface, which is what
makes the next two phases reviewable rather than a rewrite.

The measure of this phase: `grep -r 'StoreDir()' engine/` returns the store implementation and nothing
else.

**Progress, 2026-08-22.** `core.Store` is the port - `Has`, `LayerPath`, `Declaration`, `Place`,
`Squash`, `Staging` - and `exec.DirStore` is the directory implementation that exists today. Inside
`engine/exec` the direct path-joins went from thirteen to one, and that one is the sandbox boot
creating `layers/`, which a disk supplies instead.

Two things fell out rather than being aimed at: `baseConfig` was dead once declarations moved to the
stack, and the eleven lines of "make a temp dir, and if that fails because the store is cold, create
the store and try again" turned out to be one operation. Naming things tends to do that.

**`fleet.Layers` needs nothing here, which took a wrong turn to establish.** It serves layers to peers
out of the same store and reaches for paths to do it, so the first conclusion was that it wanted the
same treatment - and that it could not have it without importing `engine/exec`, so the implementation
would have to move to a neutral package. Both halves were wrong. `Has`, `Get` and `Put` are *already*
named operations; only their implementation joins a path, and that is precisely what phase 2 replaces.
An interface in front of an interface would have been ceremony.

The test for whether something needs phase 1 is not "does it touch a path" but **"does a caller
outside it have to know the store is a filesystem"**. For `fleet.Layers` the answer is no: its callers
ask for a layer and get bytes.

`LayerPath` had five users. It has one: `packImage`, which reads several layers to assemble an OCI
layout, and therefore wants bytes rather than a name - which is phase 2's work rather than more of
this. The other four were questions wearing a path (`Populated`, `NoteUnmarked`, `Has`) or renames
the store should own (`AdoptConfig`, `PutNamed`).

**Phase 1 found two defects on its own**, which is the argument for doing it as a phase rather than
as a preamble to phase 3:

* `baseConfig` had no callers once declarations moved to the stack, and was a second way to read what
  an image declares;
* a build context was copied straight into its final directory, so a copy that failed half way left a
  tree `Has` reports as present, and a later build would stand on it. Everywhere else here a transfer
  leaving nothing beats one leaving half; the context path was outside that rule and nobody had
  noticed, because the failure needs an interrupt at the wrong moment.

Neither was visible while the store was a path everybody could join. Naming the operations is what
made them look wrong.

### Phase 2 - a guest-backed implementation

The same interface, implemented over the protocol. The guest already materialises, captures and packs
(the same operations, seen from the other side of the boundary), so this is mostly wiring existing
guest code to new request kinds.

Measured in E541: the transport carries 382 MB/s against the 307 MB/s at which the host hashes, so it
is faster than the work it feeds. This phase is where that gets confirmed on real layers rather than
on `/dev/zero`.

The first operation across - `store-has` - found a step this plan did not have (E542). `core.Lookup`
verifies every L2 hit with a stat, during scheduling, before any VM boots; a build whose every step is
cached boots nothing at all, and that is the 0.66s no-op build. Route that question over the wire and
the fastest path this quarter bought pays a VM start to be told what it already believed.

So Phase 2 has a second half, and it is the one Phase 3 actually depends on:

* **an index of what the store holds, written by the guest and read by the host.** The stat is not
  asking whether the cache is honest - it is checking that a layer directory on a shared filesystem
  has not been deleted by a GC, a half-finished copy, or a user with `rm`. A disk only the guest
  mounts has no such hole, so an index the guest maintains is exactly as trustworthy as the stat was.
  The check does not weaken; what it checks becomes unforgeable by construction.

  Built, and checked in shadow (E543). Five places filed a layer and none could be indexed until
  they shared one; `store.Publish` is that seam, and `Index.Disagrees` asserts the index and the
  store agree after a real build. What remains for Phase 3 is moving the index to the host's own
  directory - it lives inside the store today, because that is still where the store is.

### Phase 3 - the disk

Worth doing, and not for the build E550 measured. A no-op build is a registry round trip and the disk
cannot help it; a build with a real base spends 3 to 13 seconds per cold step in the store's
transport, and nothing else addresses that (E551). Both readings are about different builds and the
engine has both kinds of user.

Attach a second block device, put ext4 on it, mount it where the store is, and select the guest-backed
implementation. **The attaching is one command**: `container volume create -s` yields a sized,
ext4-formatted `/dev/vd*` at a target, so the premise needs no new plumbing (E571). A raw
`--mount type=block` is refused by `container` 0.9.0, which is worth knowing only so nobody spends a
day on it.

The open questions belonged here and nowhere earlier. They are answered, and not as expected:

* **sizing.** A disk has one, a directory does not. Too small fails a build; too large wastes a
  developer's disk. Growth is *not* implementable after all - there is no resize subcommand, so ext4
  resizing online is unreachable from here. It also turns out not to be needed: the image is sparse,
  and a 1GiB volume costs 2.3MB, so the answer is to over-provision and treat the declared size as a
  ceiling that migration alone can raise.

  **The cost of that answer is a filesystem that lies about space.** A sparse image reports free
  space the host cannot supply, and a build then fails mid-write with a filesystem error for a
  condition that is not one. The store has to check the host and say so itself (I11), because space
  is the one property a filesystem is believed about (E571).
* **not copy-on-use.** The smaller design - keep a copy of each layer on the guest's filesystem and
  mount from there - costs 8.96s to copy a 267MB base and saves about 5s per read-heavy step, because
  the copy reads through the very transport it exists to avoid (E552). The disk *writes* the layer
  where it will be read, once, instead of writing it to the host's filesystem instead; the bytes
  cross the boundary exactly as often as they do now. Written down because copy-on-use is what
  somebody reaches for first and can be built in an afternoon.
* **who owns the bytes on the host.** The image is a file the host must not corrupt, and the sandbox
  that has it mounted is the only writer. A second sandbox wanting the same store has to wait or be
  refused, where today two builds share a directory happily.

  Refused, it turns out, and by the hypervisor rather than by anything written here: a second
  attachment fails with `VZErrorDomain Code=2, "The storage device attachment is invalid."` So the
  rule costs nothing to enforce and everything to explain - that message names neither the store, nor
  the build already holding it, nor what to do. **The work in this bullet is the diagnostic, not the
  exclusion** (E571).

* **a collector, which nothing above needed and this does.** Nothing in the store prunes, evicts or
  has a ceiling: one project grew a store to 13GB and 38 of them filled a 3.7TB disk in a day. A
  directory that only grows is somebody's disk-space problem; a *disk* that only grows fails every
  build against it, so a size makes the missing collector sharper rather than softer, and it has to
  land with the device rather than after it.

### What this is worth, from E540 and E541

Per-file reads 219µs to 64µs; host descriptors from one per entry walked to one in total; uid, gid,
device nodes and mtimes native rather than lost, which closes E84, E88, E94 and E89 as a side effect.
Four defects with one cause, fixed by one change - which is the argument for spending a fortnight on
it rather than an afternoon on each.

## Parked: one directory per image layer, assembled by mount

`engine/image` unpacks every layer of an image into **one** directory, oldest
first, applying `.wh.` markers by deleting (`engine/image/whiteout.go`). That is
why unpacking has to be ordered, and it is a choice of this puller rather than a
property of images: overlayfs exists to stack layers that were never merged.

If each OCI layer became its own store entry, then:

* **unpacking is unordered and parallel** - nothing about one layer depends on
  another, and assembly is a mount rather than a copy. Ordering moves into the
  option string: "The specified lower directories will be stacked beginning from
  the rightmost one and going left" (`Documentation/filesystems/overlayfs.rst`).
* **layers dedupe across images.** Today `ImageCacheKey(ref, platform)` names one
  flattened rootfs per image, so `alpine:3.22` and `golang:1.26-alpine` share
  nothing on disk.

### What the kernel actually allows, read rather than recalled

* `#define OVL_MAX_STACK 500` - `fs/overlayfs/params.h:20`. Exceeding it is
  `"too many lower directories, limit is %d"` and `-EINVAL`.
* **500 is not the binding limit here.** `mount(2)` copies its options through
  `copy_mount_options()`, which `kmalloc(PAGE_SIZE, ...)` and copies at most
  that (`fs/namespace.c:4046`) - so every lowerdir path is charged against 4096
  bytes. `farm.go` already says so: a layer named by digest costs 98 bytes, and
  41 of them overflow. Hence the symlink farm and `/proc/self/fd/N` shortening.
* **The escape exists and is not taken.** The new mount API accepts one lower
  per call: `fsparam_file_or_string("lowerdir+", Opt_lowerdir_add)`
  (`fs/overlayfs/params.c:163`) - by string *or by file descriptor*, so neither
  a page nor a path length applies. This engine mounts with classic
  `unix.Mount("overlay", ...)` (`overlay_linux.go:279`).

### What it would cost

* **Whiteouts must be converted, not applied.** A deleted path becomes "a
  character device with 0/0 device number or ... a zero-size regular file with
  the xattr `trusted.overlay.whiteout`" - the second form matters because
  `mknod` needs privilege the guest does not have. `userxattr` moves the
  namespace to `user.overlay.*`, which this engine already uses
  (`lowerhint.go`), and `.wh..wh..opq` becomes the opaque xattr, already
  handled.
* **Every `FROM` gets deeper.** A five-layer image makes a step's base five
  elements instead of one, and depth has a measured per-step cost (E635-E641).
  Cheaper since the delta fix, not free.

Unmeasured. Worth a prototype before it is worth an argument.

### What overlayfs has gained, and what this engine may use

Read from `fs/overlayfs` history rather than recalled:

* **Data-only lower layers** (2023-04: `ovl: introduce data-only lower layers`,
  `implement lookup in data-only layers`, `implement lazy lookup of lowerdata`)
  with **fs-verity** alongside them (2023-04/06). Spelled `lowerdir=/l1:/l2::/do1`
  * a double colon - and via `datadir+` under the new mount API since v6.8. The
  data-only layers are invisible in the merged tree; a `metacopy` file above
  them carries a `redirect` to the data. **This is the pre-assembly mechanism**:
  a metadata layer plus content-addressed data, mounted rather than unpacked,
  and it is what `composefs` is built on.
* **The new mount API** (2023-06: `ovl: port to new mount api`), which is what
  makes `lowerdir+`/`datadir+` possible at all.
* **Idmapped overlay mounts** (2026-06: `ovl: allow idmapping overlay mounts`,
  plus `ovl_permission`/`getattr`/`setattr`/`set_acl` handling). A layer can be
  presented with shifted ownership without being rewritten - which is the
  problem E446 is about.
* **Case-folding layers** (2025-06/08), which is the Linux-side shape of the
  case-insensitivity note this engine already prints on macOS.

**The catch, and it is a hard one.** `fs/overlayfs/params.c:988` reads
"Resolve userxattr -> !redirect && !metacopy dependency": `userxattr` turns
both off. Data-only layers are built on metacopy and redirect, so **the
pre-assembled path is unavailable to an unprivileged overlay mount**. This
engine already probes which world it is in - `needsUserXattr` tries to set
`trusted.overlay.*` on the scratch filesystem and falls back to `user.overlay.*`

* so the answer is "only where the probe says trusted xattrs work", which in
the VM sandbox it may well.

### Which world the sandbox is in, and why it matters

Measured rather than assumed. Setting `trusted.overlay.opaque` fails with
`operation not permitted` for *root in a default container* - the capability,
not the uid, is what `trusted.*` needs - and succeeds with `--cap-add
SYS_ADMIN` or `--privileged`. The guest holds `CAP_SYS_ADMIN`, because it mounts
overlayfs and `/proc`, so **in the sandbox `needsUserXattr` says false and the
metacopy/redirect/data-only path is open**. It is closed in rootless and
in-container use, which is where `userxattr` earns its place.

### Why the option set is bare, and must stay bare on the write side

**The upper directory is the artefact.** `commit` takes `h.Delta()` - the
overlay's upperdir - and stores it under the step's digest. Every option that
makes an upper entry a *reference* into the layers below it therefore breaks
the layer, silently and under a digest that claims otherwise:

* `metacopy=on` copies metadata only, leaving "data from a file in another lower
  layer (further below)" reachable through a `redirect` xattr. Committed as a
  standalone layer, that file has no contents.
* `redirect_dir=on` does the same for a renamed directory.

So these are not options this engine forgot; they are options its capture model
forbids. **They belong to the read side** - how a *base* is represented and
mounted - and not to the write side, which is exactly where data-only layers
would sit: an image assembled from content-addressed data, mounted rather than
unpacked, with steps still writing plain uppers on top. Reasoned from the
documented semantics and the commit path, not measured; anyone enabling
metacopy should first make `commit` resolve a metacopy entry through the merged
view rather than reading the upper.

### Measured and not taken

`volatile` omits "all forms of sync calls to the upper filesystem", and a build
step's upper is the definition of recreatable. On a write-heavy step (240 MB of
`dd` plus three thousand small files) it was 5.15s and 5.21s against 5.31s and
5.40s - about 3%, and inside the noise of a VM whose disk is a host file. Not
worth the durability argument, and worth recording so nobody re-derives it.

Also not taken, for the same reason: the mount itself is no longer a cost.
`mat:stack` measures 0.1ms per step, so option tuning aimed at the *mount* is
aimed at the wrong thing. What remains expensive is what happens *through* the
mount - copy-up, and reading a directory across every lower layer - which is
where E639-E641 went.

## CLI compatibility: what is missing, ordered by what the corpus asks for

The engine implements the language; the command line around it is a separate
surface and has been filled in as each piece was needed. That is how
`./dir+target` came to be refused at the front door while the interpreter
resolved it happily for every `BUILD` in a build - and it is why nothing under
`tests/` could be driven by naming a target in it.

Counting is cheap and settles the order. `cmd/earth/flag/global.go` declares 44
global flags; `earth-native` has 12. Forty-two are missing, and most of them do
not matter: the question is which the corpus actually passes.

| Flag                       | Corpus uses | State   | Note                                                      |
| -------------------------- | ----------- | ------- | --------------------------------------------------------- |
| `--no-output`              | 29          | present | added with `--ci`                                         |
| `--allow-privileged`       | 16          | present | and the per-reference form, which is the one that gates   |
| `--build-arg`              | 15          | present |                                                           |
| `--secret`                 | 13          | present |                                                           |
| `--version-flag-overrides` | 7           | present |                                                           |
| `--push`                   | 5           | present | `RUN --push` runs; an *image* push still needs a registry |
| `--with_docker_ignore`     | 4           | n/a     | not a flag - see below                                    |
| `--arg-file-path`          | 4           | present |                                                           |
| `--secret-file`            | 2           | present |                                                           |
| `--no-cache`               | 2           | present |                                                           |
| `--env-file-path`          | 1           | present | with `.env`, which supplies settings and not build args   |
| `--verbose`                | 1           | missing | per-file context transfer: `sent data for a.txt (1 B)`    |
| `--exec-stats`             | 1           | missing | `total CPU: … total memory: …` across steps               |

`--env-file-path` was missing from this table as well as from the engine. It is
neither a feature nor a flag on its own: `.env` stopped supplying *build
arguments* in v0.7.0 and never stopped supplying *settings*, and the engine read
the file only to warn about it - so `EARTHLY_PUSH=1` in a `.env` did nothing.
Reading it is now the general rule that `--arg-file-path` already followed by
hand, a flag's name upper-cased with `-` written `_`.

**The two that are left are features, not flags**, which is why they are last
rather than next. `--verbose` reports what the *context transfer* moved, file by
file, in buildkit's vocabulary for a transfer this engine does not perform.
`--exec-stats` needs per-step CPU and peak memory, which means reading `cpu.stat`
and `memory.peak` from the step's cgroup in the guest and reporting them back -
there is no accounting of any kind today. Accepting either flag without the
work behind it would be a lie the corpus would catch and a user would not.

One row was a mistake and is worth keeping visible. `--with_docker_ignore` is
not a flag: it appears inside `--target="+create-files
--with_docker_ignore=\"true\""`, which is a *build argument written after the
target*. Counting every `--word` as a flag invented it. What the corpus was
actually asking for was `+target --ARG=value`, the language's ordinary way to
pass an argument - which the engine did not accept at all, and which is a larger
gap than anything else on this list.

The order to fill them is that column. `--allow-privileged` is worth four of the
next three put together, and `--no-cache` is worth two - a ratio no amount of
reasoning about which flags feel important would have produced.

### What is missing and does not matter

Thirty of the forty-two are buildkit's, and this engine has no buildkit:
`--buildkit-host`, `--buildkit-image`, `--buildkit-container-name`,
`--buildkit-volume-name`, `--no-buildkit-update`, `--ticktock`,
`--remote-cache`, `--use-inline-cache`, `--save-inline-cache`,
`--max-remote-cache`, `--disable-remote-registry-proxy`, `--logstream-*`,
`--server-conn-timeout`. Accepting them silently would be worse than refusing
them: a flag that does nothing is a build that did not do what was asked and
said nothing about it (I10).

The rest are cloud, auth or environment - `--auto-skip`, `--global-wait-end`,
`--git-username`, `--installation-name` - and belong with whatever answers those,
not with the engine.

### The shape of the gap, not just its size

Two of the flags above are not flags at all but refusals with a reason.
`--push` and `--allow-privileged` both name things this engine declines to do:
`RUN --push` is refused, and `RUN --privileged` is refused on the grounds that a
step already has every capability inside its namespace. So accepting the flag
means deciding what it now means, which is a language question rather than a
parsing one, and those two want settling before they are typed in.

## Where the run gate stands, and what the last twenty-six are

Running every corpus invocation the way `tests/Earthfile` drives it - 250 of
them, `DO +RUN_EARTH` reproduced faithfully - the engine is at **223 ok**, with
no timeouts. `build-arg.earth+all` passes too but only against a cold cache, for
a reason worth keeping: the fix for it (E724) changes a *note* beside a layer
rather than the layer, so every key matched and a warm sweep went on serving the
answers computed while deletions were being lost. A fix that changes only a
sidecar is invisible to a warm sweep.

The twenty-six that remain are not a work list. Sorted by what they actually
are:

| what                                 | count | where it stands                        |
| ------------------------------------ | ----- | -------------------------------------- |
| documented refusals (`diverges`)     | 8     | positions, already cited               |
| macOS case-insensitivity (`dind`)    | 4     | the host filesystem, not the engine    |
| ownership the host store cannot hold | 3     | the `--keep-own` feature, see the nits |
| unjudgeable / unmodelled             | 5     | the harness, not the engine            |
| `RUN --aws`                          | 2     | **a decision** - E726                  |
| `--verbose`, `--exec-stats`          | 2     | features, not flags - see below        |
| `LOCALLY` prefix under a probe       | 1     | a real gap, `--engine=buildkit` today  |
| `LOCALLY` in a fetched Earthfile     | 1     | a position spelled as a plain error    |

Two of those deserve a sentence each, because they are the ones a reader will
otherwise put on a list and try to do.

`RUN --aws` is E726: the corpus asserts the credentials appear **in the build
output**, which is the opposite of a position this engine already holds and
enforces with a secret scanner. It is neither built nor refused, and "not yet
built" is currently false in both directions.

The `LOCALLY`-in-a-fetched-Earthfile refusal is a position - it is about a
repository's commands running on your machine as you - and it is spelled with a
plain `fmt.Errorf` rather than `refusedOnPurpose`, so the corpus counts it as a
gap. Converting it is right and was deliberately not done here: it would not
move the count (a refusal is not an `ok`), and changing a security refusal's
error identity can change control flow in callers that test for `ErrRefused` -
`--if-exists` already swallows refusals.

**The one thing in the way is not on this list.** E723, the deadlock in
`seccomp_do_user_notification`, costs one to three invocations a sweep at random
and is what makes any two sweeps hard to compare. It is characterised, it is not
concurrency-gated, and it is open.

## Decisions waiting, as of the E749-E780 sweep

Eight things this sweep found that are choices rather than defects. Each is
evidenced where it is named; none should be settled by whoever next reads the
code, because each could reasonably go the other way.

| what                              | the choice                                                                                                                                                                                                       | where               |
| --------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------- |
| `WITH DOCKER` on a podman machine | serve the docker API from podman, run a daemon from the step's image, or declare it unsupported                                                                                                                  | E767, and 5 CI jobs |
| `--load` and the daemon's storage | load engine-side, key the mount to the block, or capture the storage                                                                                                                                             | nits                |
| more than one `RUN` in a block    | refuse as earthly does, or share the daemon across the block                                                                                                                                                     | nits                |
| `EARTH_PIN_TTL`'s default         | 420ms a build against a tag that may have moved                                                                                                                                                                  | E766                |
| the sandbox's name                | `buildkitsandbox` kept, or renamed and the corpus updated                                                                                                                                                        | E758                |
| artifact mtimes                   | the real time, or earthly's fixed 2020 epoch - note `--keep-ts` is a no-op until this is settled (E789)                                                                                                          | E775                |
| `../run/` in the completion test  | the expectation encodes earthly's own directories                                                                                                                                                                | E780                |
| a CHANGELOG line for the engine   | one paragraph, at merge rather than at release                                                                                                                                                                   | pr-blockers         |
| build arguments in a `RUN`        | expand into the command text as now, or hand them to the step as environment as earthly and docker do - the value is currently re-parsed by the shell, so a supplied argument carrying `$( )` is executed (E790) | E790                |
| a docker client inside a step     | inject a dynamically-linked one with its interpreter, ship a static one, or require the image to carry one                                                                                                       | E767, group2        |

**The last row is the one that keeps reappearing.** A step with no docker client
cannot run `docker inspect`, which is what E779's test does, and an *inner*
earth build in such a step cannot autodetect a frontend at all - `auto frontend
initialization failed due to failed to autodetect a supported frontend`, which
is what `+test-no-qemu-group2` fails on now that the MTU is fixed. The engine
refuses to inject the machine's client when it is dynamically linked, for the
reason E117 records: mounted into an alpine step it fails on its *interpreter*,
and the shell reports `docker: not found` about a file that is demonstrably
present. Every way out costs something, which is why it is here rather than
done.

**What is not on this list is anything that was fixed.** Eleven of the fifteen
Native failures this sweep classified have fixes in flight; these eight are what
is left when the defects are taken out, and every one of them is a sentence
somebody has to write rather than a bug somebody has to find.

The pattern worth carrying forward: seven of the findings behind this table came
from running the same Earthfile through `earthly` and comparing the *output* -
tar listings, `docker inspect`, `/run` - rather than from reading either
implementation. Where the two engines disagree, the disagreement is a fact and
which side is right is a decision; conflating those two is what makes a
comparison feel like a bug report.
