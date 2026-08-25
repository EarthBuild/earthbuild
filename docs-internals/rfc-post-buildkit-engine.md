# RFC: a native EarthBuild engine (post-BuildKit) with a distributed worker fleet

Status: draft for discussion. Nothing here is committed to.

Goal (from the ask):

1. Remove BuildKit. Today we hand it many independent solves; one process that owns
   the whole build should beat that.
2. Let a build spread over a fleet of workers (e.g. a `ubuntu-latest` matrix), using the
   same class of networking stack as [rebuck2](https://github.com/gilescope/rebuck).
3. All Go.
4. Reuse containerd as a component rather than writing a runtime.

## 1. Where we are

* We already ship a **fork** of BuildKit and of fsutil - `go.mod:147-150`
  (`github.com/earthbuild/buildkit`, `github.com/earthbuild/fsutil`).
* BuildKit is imported from **28 packages**; the concentrations are `earthfile2llb` (10
  files), `cmd/earthly/subcmd` (6), `util/llbutil/secretprovider` (5), `buildcontext` (5).
* The build runs *inside* BuildKit: `builder` calls `bkClient.Build` with a gateway
  `BuildFunc`, and the Earthfile interpreter executes as a gateway client
  (`docs-internals/build-steps.md`).
* Every point where the interpreter needs a **fact** about the world becomes a full
  gateway `Solve` via `util/llbutil/statetoref.go:42`. Call sites:

| Site                                        | Trigger                                               |
| ------------------------------------------- | ----------------------------------------------------- |
| `earthfile2llb/converter.go:907`            | `RunExitCode` - `IF`, `ELSE IF`, conditions           |
| `earthfile2llb/converter.go:1032`           | `runCommand` - `ARG x = $(...)`, `$(...)` expressions |
| `earthfile2llb/converter.go:3044`           | `forceExecution` - un-lazy a state                    |
| `earthfile2llb/converter.go:3075`           | `readArtifact` - read a produced artifact             |
| `earthfile2llb/wait_block.go:179`           | one per `SAVE IMAGE` in a wait block                  |
| `earthfile2llb/wait_block.go:370`           | one per `SAVE ARTIFACT ... AS LOCAL`                  |
| `earthfile2llb/with_docker_run_base.go:190` | `WITH DOCKER` image loading                           |
| `buildcontext/git.go:174,337`               | git clone + git metadata                              |
| `builder/builder.go:371,380,425,456,591`    | legacy (VERSION 0.5/0.6) and remote-cache paths       |

* `util/llbutil/pllb` exists only because `llb.State` is not goroutine-safe; it serialises
  **all** LLB construction in the process behind one global mutex (`pllb/state.go`, `gmu`).
* `inputgraph/` (1851 lines) already computes a target-level cache key *without evaluating
  anything* - the basis of auto-skip. This is the seed of a native scheduler.
* containerd is already in the dependency graph: `go-runc` and `platforms` directly,
  `containerd`, `continuity`, `cgroups`, `stargz-snapshotter`, `nydus-snapshotter`,
  `ttrpc`, `typeurl` indirectly.

### The strongest argument for the rewrite is our own fork list

`docs-internals/buildkit-fork.md` lists 12 fork features. Read it again with an eye for
*why* each exists:

| Fork feature                                                            | Root cause                  |
| ----------------------------------------------------------------------- | --------------------------- |
| pass host sockets into the container (debugger)                         | process boundary            |
| host bind-mounts for `WITH DOCKER`                                      | process boundary            |
| Earthly exporter, pull-ping, embedded registry, cache storage driver    | process boundary            |
| `Export` on the gateway client (WAIT/END)                               | process boundary            |
| verbose logging of files sent to BuildKit                               | process boundary            |
| `llbsolver/ops/exec.go` patch for `LOCALLY`                             | LLB has no "run on host" op |
| healthcheck overrides, `StopIfIdle`, session reaping, op-load in `Info` | daemon lifecycle            |
| GC analytics                                                            | daemon lifecycle            |

Nine of twelve are damage from the IPC boundary or the daemon lifecycle, not from missing
build features. In one process, most of them stop being code and start being function calls.
`LOCALLY` becomes an ordinary step. The embedded-registry export dance disappears.

### But do not start by rewriting

The claim "many solves is slow" was plausible and unmeasured. It has now been measured -
experiments E2 and E2b - and **it is wrong on Linux**. Per solve, on a fully cached no-op
rebuild where there is no work to do at all:

| Platform              | narrow warm | wide warm | per solve |
| --------------------- | ----------- | --------- | --------- |
| macOS, Docker Desktop | 29.6%       | 45.7%     | 7.7 ms    |
| Linux, Docker 28.5    | 1.7%        | 2.1%      | 0.44 ms   |

Eighteen times cheaper on Linux. The *cause* of the macOS figure is unknown: the obvious
suspect, Docker Desktop's TCP port forwarder, was measured at 0.23 ms per round trip (E2c) and
cannot account for it. Note also that this table compares a laptop VM against a 16-core desktop,
so it varies hardware as well as platform - the Linux number stands, the comparison explains
nothing by itself.

Two claims this section used to make, both now retracted:

* *"Repeated `state.Marshal` under the global `pllb` mutex is the prime suspect."* Measured at
  0.2-0.7% of wall clock, with lock-wait effectively zero even at 8-way parallelism.
* *"If marshalling dominates, a week of memoising it buys most of the win with none of the
  risk."* It would buy under 1%.

**So the performance argument for the native engine is withdrawn on Linux** - which is all of
CI and all of the fleet. Something real remains on macOS, but until its cause is identified it
cannot be claimed as an argument for a new engine: an unexplained 9 ms is a bug to find, not a
rewrite to justify.

What remains, and what this RFC now rests on: the dev loop and watch mode, error fidelity,
distribution, nanosecond timestamps, and the deletion budget in §1b. Those are sufficient. They
are also *different* arguments, and it would be dishonest to keep quoting a number that holds
on one platform for one class of workload.

The number that survives as a target is the fixed per-invocation overhead: a warm wide rebuild
on Linux takes 2.4 s wall containing only 51 ms of solves. E10 measures the same thing directly
at ~1.4 s. That is what watch mode addresses, and it is platform-independent.

## 1z. Measured against the thing it replaces

The case below was argued before either engine could build the same Earthfile. Both can now, so it
is measured. `+earthly` is this repository's own target: 91 steps, ending in the `earthly` binary.

| target                       | state | earthly (BuildKit) | native       |
| ---------------------------- | ----- | ------------------ | ------------ |
| `+earthly`, 91 steps         | cold  | 46.4s              | 63.8s        |
| `+earthly`, 91 steps         | warm  | 20.3s  20.9s       | 0.65s  0.63s |
| 41 Go files, COPY + go build | cold  | 7.52s  7.49s       | 9.48s  9.26s |
| `FROM` + one `RUN`           | cold  | 4.68s  4.70s       | 4.10s  4.21s |
| `FROM` + one `RUN`           | warm  | 1.29s  1.33s       | 0.26s  0.34s |

Alternated, so the machine's own load fell on both arms; it was not quiet, and the absolute figures
are worse than a quiet machine would give.

**Warm is the case the argument was about, and it is thirty-two times.** 0.64s against 20.6s, with
90 of 91 steps hitting. That is the daemon, the LLB solve and the round trips per operation - §1c's
hop count - and not a faster unpacker.

**Cold is slower, by about a third, and that is not noise.** The native engine pays for its own VM
boot and re-fetches what a pruned BuildKit also re-fetches, so the gap is in what happens after the
bytes land: the unpack, the placing and the step execution. E682 and E686 have chipped at it;
E683's fetch is bandwidth-bound and cannot be. Whether cold matters is a question about where builds
run - a CI runner is always cold and a developer's machine almost never is.

**The trivial case flatters the native engine and should be ignored.** `FROM` plus one `RUN` says
almost nothing except that starting a VM costs about what starting buildkitd costs.

## 1a. What one process unlocks

Not "the same thing, faster" - things the process boundary made impossible or dishonest.
Roughly in order of value per unit of work:

1. **Export stops existing.** Four mechanisms (embedded registry, pull-ping, tar exporter,
   the Earthly exporter) exist solely to move bytes we already produced across a socket. In
   process, a result *is* a directory we own: `SAVE ARTIFACT` becomes a reflink or hardlink,
   not tar plus registry push plus `docker pull`. On APFS/btrfs/xfs, `COPY` between steps can
   be a clone rather than a copy.
2. **A real watch mode.** Keep the graph, snapshots and local-file fingerprints resident and
   rebuild only invalidated nodes when a file changes. Today every invocation rebuilds and
   re-marshals the graph from nothing, and the daemon has no concept of "this build again".
   This is the biggest user-visible win: it turns EarthBuild into a dev-loop tool rather than
   a CI tool you also run locally.
3. **Diagnostics that survive.** Errors currently cross gRPC and are recovered by
   *re-parsing the message string* - `builder/solver.go:74` does
   `earthfile2llb.FromError(errors.New(grpcErr.Message()))`. In process they stay error
   values, with the Earthfile position, the step's mounts and the exit status intact.
4. **Explainable cache misses.** We own the hasher, so `earth explain +target` can say which
   input changed. BuildKit can tell you the key differed, not why. It also collapses the two
   hashing schemes we maintain today (`inputgraph` for auto-skip, BuildKit's for the real
   cache) which can silently disagree.
5. **`LOCALLY` stops being a lie.** It is currently a patch to `llbsolver/ops/exec.go`.
   Natively, host steps and container steps are work items for the same scheduler, with the
   same cache keys, and can interleave.
6. **Interactive debug without socket smuggling.** Attach a PTY to a running step; open a
   shell at the failing step with exactly its mounts; re-enter any node, because we own the
   snapshots. Fork feature #1 disappears.
7. **Secrets never serialise.** Today they cross a session attachable into another process's
   memory. In process they are a byte slice handed to the exec.
8. **One binary, no daemon.** No buildkitd container, no Docker needed merely to *start*
   building, no CLI/daemon version skew, and no `StopIfIdle`/healthcheck/session-reaping
   machinery. Also the precondition for cheap fleet workers: a worker that needs Docker
   installed is a worker that costs 30 seconds to start.
9. **One resource budget.** Two processes currently contend for the machine with opaque
   parallelism. One scheduler means real admission control and backpressure, and
   `--parallelism` that means something.
10. **Distribution at all.** Handing a step to another machine requires step-level scheduling
    and portable, content-addressed results. BuildKit's solver owns its snapshots; that is
    the end of the conversation.
11. **Timestamp fidelity.** We control the layer writer, so nanosecond mtimes survive - see
    the plan's §2c. Today every layer boundary floors them to whole seconds.

Items 1, 3, 5, 6 and 8 are mostly *deletion*: the fork exists to work around the boundary,
and removing the boundary removes the workaround. The deletion budget in this repo, excluding
tests:

| Package                        | Lines | Fate                                  |
| ------------------------------ | ----- | ------------------------------------- |
| `buildkitd/`                   | 1717  | gone - daemon lifecycle               |
| `regproxy/`                    | 373   | gone - registry proxy for export      |
| `util/gatewaycrafter/`         | 338   | gone - ref/meta marshalling           |
| `debugger/`                    | 387   | shrinks hard - no socket smuggling    |
| `util/llbutil/secretprovider/` | 398   | shrinks - a byte slice, not a session |
| `buildcontext/provider/`       | 270   | shrinks - a path, not a filesync      |

Roughly 3.5k lines, of which the first three are outright deletions. The fork's own delta
against upstream BuildKit is additional and unmeasured. The largest single package in the
build tool is currently *how to start and supervise another program*.

## 1b. The wider deletion budget

A full-repo audit (six lenses, each adversarially refuted, 96 claims raised, 9 refuted by the
skeptics and a further 3 corrected on review - see "corrections" below) puts the total at
approximately **7,200 lines that delete outright** and **3,700 lines removed from files that
survive in shrunken form**. Shrink is not delete: the surviving portions are in neither tally.
The fork delta against upstream BuildKit is additional and still unmeasured.

### Daemon lifecycle and container management

| What                                                                 | Where                                           | LOC  | Fate   |
| -------------------------------------------------------------------- | ----------------------------------------------- | ---- | ------ |
| daemon client, health poll, mTLS certs, settings hash, startup flock | `buildkitd/`                                    | 1717 | delete |
| entrypoint, DinD wrapper, OOM adjust, CNI and TOML templates         | `buildkitd/*.sh`, `*.template`                  | 1115 | delete |
| Darwin socat bridge (VM to host registry)                            | `regproxy/`                                     | 373  | delete |
| daemon image build, SHA pin, update targets                          | `buildkitd/Earthfile`                           | 133  | delete |
| daemon settings assembly, client wrapper                             | `cmd/earthly/base/{init_frontend,buildkit}.go`  | 117  | delete |
| gRPC ALPN workaround `init()`                                        | `cmd/earthly/disable_alpn/`                     | 8    | delete |
| crash-log fetch and connection-failure triage                        | `cmd/earthly/app/run.go`                        | ~86  | delete |
| Docker/Podman shell frontend                                         | `util/containerutil/`                           | 1414 | shrink |
| bootstrap cert-gen, container pull and start                         | `cmd/earthly/subcmd/bootstrap_cmds.go`          | ~280 | shrink |
| daemon flags, config fields, frontend parsing                        | `cmd/earthly/flag/`, `config/`, `app/before.go` | ~200 | shrink |

### `WITH DOCKER` image transport

The two-axis split - tar vs embedded-registry, crossed with containerised vs `LOCALLY` -
produces four implementations of one idea. Natively, image staging is one containerd snapshot
export, so the product collapses.

| What                                                                      | Where                                                            | LOC  | Fate   |
| ------------------------------------------------------------------------- | ---------------------------------------------------------------- | ---- | ------ |
| four `WITH DOCKER` implementations                                        | `earthfile2llb/with_docker_run_{tar,reg,local_tar,local_reg}.go` | 1212 | delete |
| tar image solver + embedded-registry pull-ping solver                     | `builder/image_solver.go`                                        | 428  | delete |
| solver bridge interfaces, result channels                                 | `states/builderfun.go`, `states/solvecache.go`                   | 82   | delete |
| image tar manifest reader (stable session id for BK's local-source cache) | `dockertar/`                                                     | 59   | delete |

### The LLB solve model

| What                                                                        | Where                                     | LOC  | Fate   |
| --------------------------------------------------------------------------- | ----------------------------------------- | ---- | ------ |
| the solve barrier itself                                                    | `util/llbutil/statetoref.go`              | 56   | delete |
| phantom `COPY` of an impossible wildcard, purely to create an ordering edge | `util/llbutil/fakedep.go`                 | 65   | delete |
| metadata smuggled as base64 JSON inside LLB vertex *name strings*           | `util/vertexmeta/`                        | 163  | delete |
| dedup cache for gRPC image-config lookups                                   | `earthfile2llb/cachedmetaresolver.go`     | 73   | delete |
| dedup to avoid duplicate fsutil transfer sessions                           | `earthfile2llb/local_state_cache.go`      | 99   | delete |
| gRPC session plumbing, solve opts, metadata key protocol                    | `builder/solver.go`                       | 229  | delete |
| deprecated `bf` path, alive only for remote caching                         | `builder/builder.go:359-764`              | 280  | delete |
| semaphore throttling concurrent blocking solves                             | `earthfile2llb/`, `wait_block.go`         | ~110 | delete |
| wait-block export sections and `forceExecution` loop                        | `earthfile2llb/wait_block.go` + WaitItems | ~453 | shrink |
| a second, independent cache-key hasher (auto-skip)                          | `inputgraph/`                             | ~950 | shrink |

### Error fidelity

Not a saving - a capability. Three separate mechanisms exist to smuggle structured data
through a gRPC error *string* and reconstruct it by regex on the far side.

| What                                                | Where                                      | LOC | Fate   |
| --------------------------------------------------- | ------------------------------------------ | --- | ------ |
| regex re-parser for the flattened interpreter error | `earthfile2llb/interpretererror.go:97-143` | ~47 | delete |
| `:Hint:` sentinel string and its re-parser          | `util/hint/hinterror.go`                   | 28  | delete |
| git stderr base64-embedded into an error message    | `util/errutil/earthly_git_stderr.go`       | 29  | delete |

The last one required *a change to our BuildKit fork* (`git_cli.go`) to encode the field at
the far end. Two repos, to round-trip one string.

### Observability

| What                                                                             | Where                                          | LOC | Fate   |
| -------------------------------------------------------------------------------- | ---------------------------------------------- | --- | ------ |
| verbose gRPC protocol logger                                                     | `util/gwclientlogger/`                         | 83  | delete |
| BuildKit stats muxed on a side channel, with its own length-prefixed JSON parser | `util/statsstreamparser/`, `logbus/solvermon/` | ~98 | delete |
| status-channel backpressure buffer and progress-cancellation timeout             | `builder/solver.go`, `logbus/solvermon/`       | 20  | delete |

### Git metadata

`buildcontext/git.go` extracts commit metadata by running fifteen shell commands in an
`alpine/git` container, solving it, and reading the files back (~170 LOC); `gitlookup.go`
bundles known-hosts into `llb.KnownSSHHosts()` at graph-construction time and probes SSH
eagerly (~300 LOC). Both become `exec.Command("git", ...)` and reading `~/.ssh/known_hosts`
at execution time. Shrink, not delete.

### CI and release machinery

Approximately 325 lines delete outright - the Podman-on-macOS workflow, the
default-image assertion, the backwards-compatibility test, `tests/remote-buildkit/`, the
`earthly-next` fork pointer, the two `go.mod` replace directives, the standalone-daemon
DockerHub README - and roughly 850 more come out of workflows that survive: daemon image
build and promotion, the PR image artefact that gates every downstream job, the log-drain
step repeated across sixteen reusable workflows, and the `DEFAULT_BUILDKITD_IMAGE` ldflags
threaded through seven platform targets.

### Five things the audit found that §1a missed

1. **`util/vertexmeta/` (163 LOC) smuggles our metadata through LLB vertex *name strings*** -
   base64 of JSON holding command id, target id, source location, platform, secrets flags -
   and parses it back out in `solvermon`. LLB has no typed metadata channel; the display name
   is the only user-controlled field that survives the wire. Natively this is a struct field.
2. **`util/llbutil/fakedep.go` creates dependency edges by copying a file that cannot exist** -
   a UUID-prefixed impossible wildcard - because LLB has no way to say "after this".
3. **`conversion_parallelism` is a daemon-saturation dial, not a parallelism knob.** Each
   goroutine blocked on a solve holds an open stream; too many saturate or OOM the daemon.
   Anyone who tuned it was compensating for the second process. It has no meaning in one.
4. **The deprecated `bf` path (280 LOC, `DO NOT ADD CODE` in the source) is kept alive solely
   for remote caching** (`earthly/earthly#2178`). It can go the moment `wait_block.go` is the
   only export path - no native engine required.
5. **Error smuggling reaches into the fork.** `EARTHLY_GIT_STDERR` is base64-encoded into an
   error string by our BuildKit patch so it can be decoded here.

### Corrections applied to the audit

* `util/containerutil/` was reported as an outright deletion. It is not even a plain shrink -
  it *transmutes*. The daemon-supervision half goes, but the frontend abstraction becomes the
  **executor backend** interface (see §2.3), and PR #614 is already extending it with a third
  backend. `SAVE IMAGE` also still has to load images into the user's Docker or Podman.
* `socketprovider` and `localhostprovider` are BuildKit's own packages, not ours; only our
  wiring to them is ours to delete.
* `logstream/` plus `util/deltautil/` (2857 LOC, mostly generated protobuf, now used purely as
  an *internal* event-bus format with no cloud consumer left in this fork) is real dead weight,
  but it is inherited from the Earthly-cloud lineage, **not** caused by the process boundary.
  It is excluded from the totals above and deserves its own cleanup.

### Sequencing

Deletable **early**, during the engine-seam work and before native is the default: the
`buildkitd/` package and its shell scripts, `disable_alpn`, the startup flock and settings-hash
label, and the deprecated `bf` path. None depend on the native scheduler.

Deletable **late**, only once native is the default: the session attachables, the four
`WITH DOCKER` transports, everything in the LLB-model table, the error round-trips, and the CI
machinery - which goes last, since each workflow step can only adapt after the suite is green
on the native engine.

## 1c. Stack shape, before and after

First, a correction to the usual framing. There is **no containerd daemon in the path today**.
`buildkitd/buildkitd.toml.template` configures `[worker.oci]` with `snapshotter = "auto"`, so
BuildKit links containerd's snapshotter and content-store *libraries* and drives `runc`
itself. The stack is not `earth -> buildkit -> containerd`; it is:

```text
  earth  ->  dockerd  ->  buildkitd (container)  ->  [containerd libs + runc]
           (only to start                          (linked in, not a daemon)
            and supervise it)
```

So the native engine does not remove a daemon hop to containerd. It links the same libraries
one process earlier. **The bottom of the stack does not change** - same runc, same overlay
snapshotter, same content store - which is the main reason this is tractable at all.

### Today

```text
 host
 ┌──────────────────────────────────────────────────────────────────────┐
 │  earth (CLI)                                                         │
 │    │                                                                 │
 │    │ 1. docker run buildkitd  ──────────────►  dockerd ──► containerd│
 │    │ 2. Build(BuildFunc) ═══ gRPC ═══╗                               │
 │    │                                 ║                               │
 │    │  ┌ buildkitd container ═════════╩═══════════════════════════┐   │
 │    │  │  gateway frontend  ── runs OUR interpreter, remotely     │   │
 │    │  │      │                                                   │   │
 │    │  │      │ Solve(marshalled graph)  per IF / $() / SAVE      │   │
 │    │  │      │ ── barrier: nothing proceeds until it lands       │   │
 │    │  │      ▼                                                   │   │
 │    │  │  llbsolver ─► cache manager ─► snapshotter (ctrd lib)    │   │
 │    │  │                             └► executor ─► runc          │   │
 │    │  └──────────────────────────────────────────────────────────┘   │
 │    │      ▲                                                          │
 │    │      ╚═ session reverse-channel (gRPC, other direction):        │
 │    │         filesync, secrets, ssh, registry auth, host sockets     │
 │    │                                                                 │
 │    └─ export: embedded registry ─or─ tar  ─►  earth  ─► docker load ─┼─► dockerd
 └──────────────────────────────────────────────────────────────────────┘
```

Docker appears twice: once to *start* the builder, once to *receive* the result. Every fact
the interpreter needs travels out as a marshalled graph and back as a gRPC read.

### Native

```text
 host
 ┌────────────────────────────────────────────────────┐
 │  earth                                             │
 │    interpreter ──► ir ──► sched                    │
 │         ▲              │                           │
 │         └── futures ───┘   (await one node, not a  │
 │                             whole solve)           │
 │    exec ──► snapshotter (ctrd lib) ──► runc        │
 │    content store / CAS  ──► registry (pull/push)   │
 │    secrets, ssh, files: values, not attachables    │
 │                                                    │
 │    export: the result is already a snapshot we own │
 │            └─ docker load only if the user wants it┼─► dockerd (optional)
 └────────────────────────────────────────────────────┘
```

### Hops per operation

| Operation                | Today                                                                           | Native                                               |
| ------------------------ | ------------------------------------------------------------------------------- | ---------------------------------------------------- |
| `IF` / `$(...)`          | marshal whole graph → gRPC Solve → solve → exec → gRPC ReadFile                 | schedule node → exec → read                          |
| `SAVE ARTIFACT AS LOCAL` | Solve → gateway Export → fsutil stream → write                                  | read from the snapshot (reflink)                     |
| `SAVE IMAGE`             | Solve → embedded registry *or* tar → pull-ping → `docker pull`/`load` → dockerd | write layers to store; `docker load` only on request |
| `LOCALLY RUN`            | magic UUID in an LLB vertex → session → host executor → back                    | `exec.Command`                                       |
| build startup            | pull/start/health-check a container, generate mTLS certs                        | none                                                 |

### macOS: Docker-free is already in flight

Container steps still need a Linux kernel; nothing removes that. But **PR #614**
(`feat: support Apple container`, draft, +1706/-43) adds `apple-container-shell` as a third
`ContainerFrontend` alongside docker-shell and podman-shell, with CI on `macos-26` runners
(`brew install container`, `container system start --enable-kernel-install`). Today it starts
*buildkitd* under Apple's runtime instead of Docker Desktop - which already makes EarthBuild
Docker-free on Apple silicon.

Start from there and the two removals compose:

```text
  #614 alone         earth -> apple/container -> buildkitd -> [ctrd libs + runc]
  native alone       earth -> dockerd        -> [ctrd libs + runc]
  both               earth -> apple/container -> step VM        (no Docker, no BuildKit)
```

Apple's containerization gives each container its **own lightweight VM** rather than one
shared Linux VM, and consumes OCI images directly - which is a second reason for the §2a rule
that a step's result must be a content-addressed OCI layer blob. The `regproxy` socat bridge,
which exists purely to reach the embedded registry across the Docker Desktop VM boundary, has
nothing left to do. `LOCALLY` steps run natively on the host.

Open questions this raises, none of them blocking:

* **Where does the snapshotter live on macOS?** Per-container VMs mean overlayfs semantics are
  inside the guest. Either keep a host-side CAS and share directories in (virtiofs), or run a
  persistent helper VM that owns the snapshotter. This is the main design fork for the macOS
  backend.
* **Does nanosecond mtime survive the guest boundary?** APFS stores nanoseconds and virtiofs
  should carry them, but §2c's round-trip test must run on the macOS backend too, not just on
  Linux. Unverified.
* **Per-step VM start-up cost** versus one long-lived VM. Measure before assuming either way.

## 1d. Security

Honest answer to "are we building it in from the ground up?": **partly, and partly by accident.**
Some things get better for free, one thing gets materially worse, and one is an architectural
requirement this RFC had not stated.

Start from the fact that governs everything else: **a build tool executes untrusted code by
design.** An Earthfile from a pull request, a dependency fetched mid-build, a base image - all of
it runs. The question is never "can we prevent execution" but "what does execution get access
to, and who has to trust the result".

### The requirement one-process nearly hid: privilege separation

Today `buildkitd` runs as `docker run --privileged ...` and the CLI does not. The privileged
component is separate from the large one by accident of deployment. Collapsing to one process
collapses that too - and E13 confirms the executor genuinely needs `CAP_SYS_ADMIN`, since
mounting a snapshot fails with `operation not permitted` without it.

So "one process" must mean **one large unprivileged process plus a minimal privileged executor
helper**, with the mount, namespace and cgroup operations behind a narrow, auditable interface.
That reintroduces a process boundary - but a deliberate one, a few hundred lines wide, drawn
where a security boundary belongs, rather than the accidental one at the LLB layer that §1b
prices at ~7,200 lines. Designing this in is cheap; retrofitting it means auditing everything
that ever ran as root.

macOS gets a stronger story free: Apple's runtime isolates each step in its own VM, which is a
harder boundary than a container. That is a security argument for mac-first that §2b did not
make.

### What improves

* **Secrets stop being serialised.** Today they cross a gRPC session into another process's
  address space and log surface. In process they are a byte slice handed to an exec.
* **No long-lived privileged daemon.** The developer machine used for E10 has had a privileged
  buildkitd container up for 33 hours. That is standing attack surface between builds. A
  process that exits leaves none.
* **Provenance becomes expressible.** Content-addressed steps plus the §2c timestamp work are
  most of what SLSA-style provenance needs; we would be able to state what produced an artifact
  because we would own the graph.

### What gets worse: the fleet

Distribution is the part that genuinely enlarges the attack surface, and it deserves its own
threat model rather than a footnote.

| Risk                                                                                                                             | Status                                                                                                                        |
| -------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| Keyless rendezvous off `github.run_id` - public metadata on public repos, so anyone can join the mesh and serve poisoned outputs | Known; mitigation is to mix in a per-run secret plus a driver-published node allowlist (§2.4)                                 |
| **Output integrity** - a worker returns a layer the driver cannot verify, because builds are not reproducible in general         | **Unsolved, and the hard one.** See below                                                                                     |
| Blob transport tampering                                                                                                         | Sound by construction: content-addressed, and go-iroh's `blobs` uses BAO verified streaming, so even partial data is verified |
| Cache poisoning persisting across builds in a shared CAS                                                                         | Needs a policy: what may enter the shared CAS, and from whom                                                                  |
| Dependency surface - go-iroh is young and would sit in a build tool's trust path                                                 | Vendor-review before adoption, and keep it behind `engine/fleet/mesh` so it is replaceable                                    |

On output integrity, the honest position is that **the fleet's trust boundary is the fleet
itself**: workers must be machines you already trust with your build, i.e. your own CI runners
in your own job matrix. This is not a marketplace for spare compute, and it should say so in the
documentation rather than being discovered later. Re-executing a sample of steps to compare
digests is possible for genuinely deterministic steps, and is worth measuring, but it cannot be
the general answer while `RUN curl ...` exists.

### Requirements, testable

1. Privileged operations confined to a minimal helper with an auditable interface; the CLI,
   interpreter and scheduler never run as root.
2. Secrets never written to disk, never in a step's environment unless declared, and never sent
   to a worker for a step that did not declare them.
3. Fleet identity: per-run secret in the session key, plus an allowlist; a worker that is not on
   it is refused.
4. Every blob verified against its digest on receipt, including partial and resumed transfers.
5. Untrusted-Earthfile mode: a documented set of what a build from an untrusted PR may reach -
   network, host paths, secrets, the shared cache - and a test that proves it.
6. `LOCALLY` becomes more dangerous when it is a first-class step rather than a patched exec op.
   It runs unsandboxed on the host by definition, so it must stay gated by `--strict` and must
   never be reachable from a remote or untrusted Earthfile.

## 2. Target architecture

One binary, `earth`, containing everything; no daemon required and no container to babysit.

```text
  Earthfile -> ast -> interpreter (unchanged semantics)
                          |
                          v
                   engine/ir      target-level step DAG, content-addressed
                          |
                          v
                   engine/sched   one graph, one pool, futures not barriers
                     /        \
       engine/exec (local)   engine/fleet (remote workers)
            |                        |
      containerd libs           go-iroh mesh + CAS
      (content, snapshots,      (driver <-> N workers)
       runc, registry)
```

### 2.1 `engine/ir` - the LLB replacement

Node kinds: `Image` (registry ref), `Exec`, `Copy`/file ops, `Local` (host context),
`Merge`, `Save`, `Host` (`LOCALLY`). Node ID = hash of (op, resolved inputs, platform), so
every node is content-addressed and every step is *pure and retry-safe*. That purity is
precisely what makes fleet scheduling tractable; it is not an extra.

Difference from LLB: the interpreter emits steps and holds **futures**, rather than
marshalling a whole state and posting a solve. The IR is ours, so `LOCALLY`, `WITH DOCKER`
and interactive sessions are first-class rather than patches.

Granularity is the **target/step**, not the LLB vertex - it matches Earthfile semantics,
matches `inputgraph`'s existing hash, and is the right unit to ship to a remote worker.

### 2.2 `engine/sched`

One scheduler owns one graph for the whole build. The interpreter awaits the single node it
needs (exit code, file read); everything else keeps running. No solve-shaped barriers.
Reuses `inputgraph`'s hasher for the cache key and subsumes auto-skip.

### 2.3 `engine/exec` - containerd as a library, not a daemon

Link the libraries; do not require `containerd.service`:

| Need          | Package                                                                  |
| ------------- | ------------------------------------------------------------------------ |
| content store | `containerd/core/content/local`                                          |
| snapshots     | `containerd/plugins/snapshots/overlay` (native fallback)                 |
| pull/push     | `containerd/core/remotes/docker`                                         |
| unpack/diff   | `containerd/core/images/archive`, `plugins/diff/walking`                 |
| run           | `containerd/go-runc` + `opencontainers/runtime-spec` (both already deps) |
| net           | CNI (we already ship `buildkitd/cni-conf.json.template`)                 |

An optional "use the host containerd daemon" mode is a later, cheap addition.

**The executor must be an interface, not a package.** The table above is the *Linux* backend.
On Apple silicon the backend is Apple's containerization (per-step VM, OCI images in, no
Docker) - the runtime PR #614 is already wiring in as a `ContainerFrontend`. So
`util/containerutil` does not simply die with the daemon: its frontend abstraction is the
seed of `engine/exec.Backend`, with `runc` and `apple/container` as the first two
implementations and the IR, CAS and scheduler common to both.

### 2.4 `engine/fleet` - the rebuck2 shape

rebuck2's model, restated for us: the **driver** owns the invocation and exposes the
execution API; **N workers** join a mesh, claim actions least-loaded, fetch inputs
peer-to-peer, execute, and stream outputs back. Rendezvous is keyless - both sides derive the
driver's node key from the session string.

Mapped onto EarthBuild:

* Driver = the `earth` process the user ran. Owns the graph, the interpreter, local outputs.
* Worker = `earth worker --session <s>`; a job in the matrix. Stateless, holds a CAS shard.
* Unit of work = an IR step. Content-addressed, so a lost worker means "re-run elsewhere",
  not "fail the build" (rebuck2 v0 fails in-flight actions on worker loss - we can do better
  cheaply, precisely because steps are pure).
* Blobs move peer-to-peer between workers, not via the driver - the driver is a scheduler,
  not a bottleneck. Batch (`GetMany`-style); one stream per blob does not survive a
  thousand-blob sync.
* Platform affinity per step: GitHub runners are amd64, the dev machine is arm64. The
  scheduler must not send an arm64 step to an amd64 worker.

**Security note on keyless rendezvous.** Deriving the session key from `github.run_id` alone
is fine for rebuck's threat model but weaker here: on a public repo the run id is public
metadata, so anyone who derives the key can join the mesh and serve poisoned outputs.
Mix in a per-run secret (a workflow secret, or an HMAC of the OIDC token) and verify worker
node IDs against an allowlist the driver publishes. Cheap now, painful to retrofit.

### 2.5 Transport - Go options

| Option                                                            | Verdict                                                                                                                                                                                                                                                                         |
| ----------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [`tmc/go-iroh`](https://github.com/tmc/go-iroh)                   | Clean-room pure-Go iroh, wire-compatible with Rust iroh: QUIC, relay, hole punching, ALPN, blobs, pkarr discovery. **Recommended.** Wire compatibility also means a Go worker can talk to rebuck's Rust side. Risk: young (~35 stars); we must be willing to read and patch it. |
| [`iroh-go`](https://docs.iroh.computer/languages/go) FFI bindings | Mature core, but drags in Rust and cgo - fails the all-Go requirement.                                                                                                                                                                                                          |
| `go-libp2p`                                                       | The heavyweight equivalent: QUIC, circuit-relay v2, DCUtR. Measured hole-punch success ~70% ([arXiv 2510.27500](https://arxiv.org/html/2510.27500v1)), 97.6% on first attempt. Mature, but a large dependency surface and a peer-to-peer worldview we do not need.              |
| `quic-go` + our own relay/rendezvous                              | Smallest dependency, most work. Viable fallback if go-iroh disappoints.                                                                                                                                                                                                         |
| Tailscale `tsnet`                                                 | Excellent NAT traversal, but requires a tailnet/auth key - wrong ergonomics for throwaway CI.                                                                                                                                                                                   |

Note `AGENTS.md`: new Go dependencies need explicit sign-off. This is the decision that
needs it.

## 3. What we lose, and what it costs to keep

| Capability                       | Provided today by         | Plan                                                                                                  |
| -------------------------------- | ------------------------- | ----------------------------------------------------------------------------------------------------- |
| `FROM DOCKERFILE`                | `dockerfile2llb` frontend | Biggest single loss. Keep a BuildKit path, or port the frontend to our IR (mechanical, not hard).     |
| `--cache-from/to` registry cache | BuildKit cache exporters  | Re-implement over our CAS + registry manifests, or drop remote cache in v1 and rely on the fleet CAS. |
| Multi-platform via qemu          | BuildKit + binfmt         | Keep binfmt; the executor change is small.                                                            |
| Rootless mode                    | BuildKit rootless         | Re-derive. Non-trivial.                                                                               |
| Lazy pulls (stargz)              | snapshotter plugin        | containerd gives us the same plugin.                                                                  |
| Cache-mount locking              | BuildKit cache manager    | Ours to write; get the locking right or corrupt caches.                                               |
| GC / disk budget                 | BuildKit GC               | Ours to write.                                                                                        |
| Secrets / SSH forwarding         | BuildKit session          | Simpler in-process, but it is code.                                                                   |
| Mature scheduler edge cases      | years of BuildKit         | The honest risk. Mitigation: run the existing `tests/` suite under both engines.                      |

## 4. Phasing

Each phase must stand alone and be shippable; no phase leaves the tree broken.

* **Phase 0 - measure (1-2 weeks).** Instrument every `StateToRef`: count, wall time, bytes
  marshalled, cache hit/miss, plus buildkitd RSS/CPU. Run against our own `Earthfile`
  (`earth +test`) and a large example. Controls: sweep `--parallelism`; try in-process
  BuildKit (it is a Go library - the daemon is a deployment choice, not a requirement);
  memoise `state.Marshal`. **Exit criterion: numbers that justify or kill the rest.**
* **Phase 1 - the seam (2-4 weeks).** Define `engine.Engine` (solve node, read file, read
  dir, export image, export artifact). Route every call site in §1 through it. Ship
  `engine/bkengine` as the only implementation. Zero behaviour change; the diff is wide but
  shallow. Fold `pllb` into an IR-builder interface, killing the global mutex.
* **Phase 2 - native local engine (3-6 months).** `ir` + `sched` + containerd exec + CAS,
  behind `--engine=native`, BuildKit still the default. Conformance = the existing
  integration suite, green under both engines.
* **Phase 3 - fleet (2-3 months).** go-iroh transport, driver/worker roles, `earth worker`,
  a GitHub Action for the matrix. Prove it on a two-machine LAN before CI.
* **Phase 4 - delete.** Flip the default, delete `buildkitd/`, drop both forks.

Sequencing note: phases 2 and 3 are separable but the IR must be designed for 3 from day
one - retrofitting content-addressing and step purity later is a rewrite, not a refactor.

## 5. Open questions

1. Do we keep `FROM DOCKERFILE`? It is the one feature that genuinely wants LLB.
2. Is remote registry cache (`--cache-from`) a v1 requirement, or does the fleet CAS replace it?
3. Rootless: required, or explicitly dropped?
4. Is a self-hosted relay acceptable, or must the mesh be direct-only / use public relays?
5. Fleet-first or local-first? Fleet-first is possible on top of today's BuildKit (workers
   each run a buildkitd) and would deliver value sooner, at the cost of building a scheduler
   twice.

## 6. Recommendation

Do Phase 0 and Phase 1 now; they are cheap, independently valuable, and they are the
evidence base for everything after. Do not start Phase 2 until Phase 0 says the solve
overhead is real and large.
