# Better BuildKit Failure Visibility Plan

## Goal

When a build fails inside BuildKit, Earth should report the original failing operation and error, not a generic `context canceled`.

This matters especially for nested Earth builds in CI, where the outer solve can be canceled after an inner solve, session, command, or resource failure. The current output can lose the first meaningful error, making both CI failures and user-facing product failures difficult to diagnose.

## Current Findings

Earth already has a path for better cancellation reporting:

- `builder/solver.go` wraps canceled solve errors with the first fatal or canceled vertex seen by `solvermon`.
- `logbus/solvermon/solvermon.go` watches BuildKit status updates and records the first fatal vertex failure or first cancellation-like vertex.
- `cmd/earthly/app/run.go` prints a better cancellation message when that wrapper exists.

The gap is that BuildKit often returns only a generic canceled solve:

- `control/control.go` receives `c.solver.Solve(...)` returning plain `context canceled`.
- `solver/progress.go` can synthesize canceled vertex errors at stream end.
- `solver/scheduler.go` and `solver/internal/pipe/pipe.go` can turn canceled request flow into generic cancellation.
- `solver/jobs.go` is where useful vertex errors are usually written, but not every cancellation path gets a specific root cause into status.

That means Earth may have no fatal vertex, no useful cancellation vertex, and no specific error to show.

## Definition Of Done

- A failed nested Earth build should show the original inner failure whenever BuildKit observed one.
- If the failure is session loss, resource kill, or BuildKit shutdown, the message should say that explicitly.
- A later `context canceled` must not overwrite an earlier non-cancellation root cause.
- OTEL/export/reporting failures must remain non-fatal.
- BuildKit fork changes should include `// Earthbuild:` markers where we touch upstream code.
- Existing normal BuildKit failures should keep their current useful messages.

## Implementation Plan

### 1. Add A BuildKit Root-Cause Recorder

Add a small first-error recorder to BuildKit solve/job state.

It should capture:

- solve ref
- session id
- vertex digest
- vertex name
- op description where available
- source subsystem: exec, cache map, slow cache, local source, gateway, exporter, session
- original error

Rules:

- Store only the first useful non-cancellation error.
- Do not replace a useful cause with `context canceled`.
- Do not expose secrets; reuse existing error strings and status paths rather than dumping command environments.

Likely files:

- `solver/jobs.go`
- `solver/llbsolver/solver.go`
- possibly a small helper file under `solver/`

### 2. Record Causes Before They Collapse Into Cancellation

Wire the recorder into paths where BuildKit still has the real error:

- `sharedOp.Exec`
- `sharedOp.CacheMap`
- `sharedOp.CalcSlowCache`
- gateway forwarding errors around `wrapSolveError`
- local source/session lookup failures
- exporter/finalizer failures in `solver/llbsolver/solver.go`

Each touched BuildKit site should include a short `// Earthbuild:` marker.

### 3. Return The Preserved Cause From Control/Solve

In `control/control.go`, when `c.solver.Solve(...)` returns a canceled error:

1. Prefer `context.Cause(ctx)` if it is specific.
2. Otherwise prefer the recorded solve root cause.
3. Otherwise return a richer cancellation error containing solve ref, session id, and last active vertex summary.

This is the primary product fix. Earth should receive a specific error rather than a bare canceled solve.

### 4. Improve BuildKit Status For Canceled Solves

When `solver/progress.go` synthesizes final canceled vertices, include recorded root-cause context in at least one status update if no better vertex error was already sent.

That gives Earth's existing `solvermon` path enough signal to report the target and command that were active when the solve failed.

### 5. Improve Earth's Fallback Message

Even with BuildKit fixed, Earth should have a useful fallback when BuildKit returns cancellation without a fatal vertex.

Extend `logbus/solvermon` to retain:

- last active vertices
- last completed/canceled vertices
- a small scrubbed tail of recent vertex logs

Then update the cancellation branch in `cmd/earthly/app/run.go` to print a concise "last active operations" section when no specific root cause arrives.

### 6. Keep One CI Harness Safety Net

For `+RUN_EARTHLY`, keep or add a diagnostic tail when the `exit_code=` sentinel is missing.

This should be a last-resort harness diagnostic, not the main solution. The product path should normally carry the root cause from BuildKit to Earth.

## Tests

BuildKit tests:

- root-cause recorder stores the first non-cancel cause
- later `context canceled` does not overwrite the stored cause
- `Control.Solve` returns the stored cause when solve returns canceled
- local source/session failures include useful source/session context
- synthesized canceled progress includes root-cause context when available

Earth tests:

- canceled solve with a useful cancellation vertex prints target/command context
- canceled solve with no useful vertex prints last active operations
- fatal vertex failures still take precedence over cancellation symptoms
- cancellation-like strings such as `no active sessions` are treated as cancellation context, not the root cause when a better cause exists

## Verification

Run:

```sh
go test ./control ./solver ./source/local
```

from the BuildKit fork.

Run:

```sh
go test ./logbus/solvermon ./cmd/earthly/app ./builder
earth +lint
earth --ci -P --no-output ./tests+ga-no-qemu-group4
```

from Earth.

## Rollout Order

1. Add and test the BuildKit root-cause recorder.
2. Wire recorder calls into exec/cache/gateway/local-source/export paths.
3. Return preserved causes from `Control.Solve`.
4. Improve Earth cancellation fallback output.
5. Add the `+RUN_EARTHLY` sentinel-tail safety net if it is not already sufficient.
6. Push BuildKit fork first, update Earth's BuildKit SHA, then run targeted CI jobs.

## Notes

The most important fix is preserving the original BuildKit root cause before cancellation fan-out loses it. Earth can only format the information it receives; today the failed nested cases can reach Earth as plain `context canceled`, which is not enough for a good product error.

## Field Evidence (2026-06-10, run 27293924464 / job group15)

The diagnostics shipped in fork rev `85c7359` work: instead of bare
`context canceled`, Earth now prints `BuildKit canceled or lost the solve
session` + last-active/recent operations. What they revealed:

- Both retry attempts of group15 died identically: outer earth's solve
  session lost mid-build (attempt 1 ~2 min in during `+earthly` go build;
  attempt 2 ~18 min in).
- buildkitd's view at the same instant: `killing process because execution
  context was canceled` — the cancel arrived from the client/transport
  side. Each side blames the other; no root cause either side.
- NOT memory: dmesg clean, 14G swap 0B used. NOT disk: 25G reclaimed.
- Timing correlation: both deaths coincide with a nested RUN_EARTHLY
  vertex completing — suspect cross-session teardown in the
  subbuild/edge-merge path (004c18472 fixed parent refs across edge
  merges; the cancellation propagation may have a sibling bug).

Next debugging lever: reusable-test.yml now dumps buildkitd logs before
the between-attempts reset, so attempt-1 daemon logs survive.

## Update (2026-06-10 evening, run 27298656262 / docker-test-misc)

The root-cause recorder now surfaces an original error for class-3 deaths:

```text
Original BuildKit error: failed to apply diffs: failed to handle changes:
context canceled: context canceled
```

interrupting `COPY +earthly/earthly /root/.earthly/earthly-prerelease`
(Earthfile:146, +earthly-script-no-stdout) inside the NESTED earth's own
buildkitd (buildkitsandbox, BUILDKIT_MAX_PARALLELISM=1). Preserved
attempt-1 daemon log confirms the cancel arrives from the client side
("killing process because execution context was canceled").

Pattern across occurrences: deaths always land in CPU-saturated phases
(inner `go build`, large artifact COPY/diff-apply) on a 4-core runner
running many sibling nested builds.

### Leading hypothesis: gRPC keepalive starvation

A CPU-starved inner earth misses keepalive pings to its buildkitd; the
transport drops; the session closes; everything unwinds as `context
canceled`. Earth sets no keepalive options (client lib defaults). The
fork already has `WithGRPCDialOption` (e163acdbb), so earth can pass
relaxed `keepalive.ClientParameters` (e.g. Time 30s / Timeout 60s /
PermitWithoutStream) without forking further.

Next experiment: A/B a keepalive bump on the nested test groups; if
session losses vanish, the class is closed.

### Reproducibility (third occurrence, run 27298656262 / wait-block-quick)

All three class-3 deaths interrupted the SAME vertex:
`COPY +earthly/earthly /root/.earthly/earthly-prerelease`
(`+earthly-script-no-stdout`, reached via `+test-misc`), immediately after
the nested from-source `+earthly` go build/link saturates the runner.
This is a reproducible chokepoint, not background noise — loop
`+test-misc` on a 4-core VM for the keepalive A/B. A complementary
mitigation: let the nested test reuse the staged earthly binary instead
of compiling from source inside the container (removes the CPU spike and
several minutes per job).

### Root cause found (2026-06-10 late): scheduler dispatch diagnostics

Writing a concurrent merged-edge discard regression test (-race) exposed
the real defect chain in the fork's own diagnostics (`helpMe`,
6d9a29f49): `dispatch()` reflection-formatted (`%+v`) the entire edge
struct on EVERY dispatch, in the single-threaded scheduler hot loop,
while other goroutines mutate edge/state under their own locks.

- Data race (confirmed by -race via the new tests).
- Reflective read of a map mid-write is a Go runtime FATAL — the nested
  buildkitd dies instantly with no log flush, and the inner earth
  reports exactly "BuildKit canceled or lost the solve session".
- Per-dispatch reflection/alloc tax serializes the scheduler precisely
  when builds are largest (the observed CPU-saturated death windows).

The earlier keepalive hypothesis is retired: neither client nor daemon
configures gRPC keepalive, so no pings exist to miss.

Fixed in fork branch `giles-fix-merged-edge-discard` (32708f3f9857):
race-free dispatchTrace formatted only on the error path, dgstTracker
mutex, plus TestMergedEdgeDiscardWhileSiblingInFlight and
TestSubBuildMergedEdgeDiscardWhileSiblingInFlight pinning discard
behavior. Earth pin bumped in 59bc6dff; CI validating.

### ACTUAL root cause (2026-06-10 night): session healthcheck hair-trigger

The scheduler-diagnostics fix was real but not the killer — class 3
recurred on the patched daemon. The preserved attempt-1 daemon log
showed a clean run until the cancel arrived from the monitor side, and
the failing build is the OUTER earth's (never nested at all).

The fork's configurable session healthcheck (configurabletimeout.go)
runs with appdefaults allowedFailures=1, frequency=1s, timeout=10s:
one missed 10s health round-trip and the session is killed. On a
saturated 4-core runner the earth client is starved exactly that long
during go build/link, so its session died mid-solve — surfacing as
"BuildKit canceled or lost the solve session" with context-canceled
fan-out (diffapply, LLBBridge). Fits every observation: CPU-correlated
timing, both attempts dying, no daemon-side error, the message itself.

Fixed in fork eb44e0f74a7a: allowedFailures=3, timeout=30s (dead
clients still reaped in ~90s). Earth pin bumped in 96ffec4b. The
healthcheck cancel cause ("session healthcheck failed too many times")
should now also surface through the root-cause recorder if it ever
fires again.

### Convicted (2026-06-11): flightcontrol waiter poisoning

The cancellation-origin attribution (e692116e) fired on the next two
class-3 failures and said, all four times: "Local build context is
still alive" — earth innocent, daemon/session side guilty, healthcheck
provably idle (its failures now log; zero logged).

Mechanism: shared lazy merge refs (COPY +earthly/earthly) are unlazied
under a package-global flightcontrol keyed by ref. The combined context
keeps fn alive while any caller lives, but fn dies of cancellation
anyway through resources tied to the WINNING caller — its session group
closing / leases released when that solve ends. flightcontrol's wait()
only retried late arrivals; a live waiter inherited the winner's
"failed to apply diffs: context canceled" verdict verbatim, failing a
healthy build because an unrelated sibling solve finished first.

Fix (fork 79762ff4c, red test first): a waiter whose own context is
alive retries instead of inheriting a canceled-error artifact.
TestLiveWaiterRetriesWinnersCancellationArtifact pins it. Earth pin
bumped in fa703f40.

### Reframe (2026-06-13, Opus): the masking is earth-side, at the errgroup

Cancellation-origin attribution (e692116e) said "earth ctx alive" on all
class-3 failures — but it checked the TOP-LEVEL ctx. earth solves under
`errgroup.WithContext` (builder/solver.go); the derived ctx cancels when
EITHER goroutine returns. MonitorProgress also returns earth's own
status-processing errors (bp.NewCommand). When it aborts it cancels the
errgroup, bkClient.Build returns bare "context canceled", and the old
code preferred that buildErr — discarding the real monitor error.

The exit-137 on the active vertex is buildkit's teardown SIGKILL of the
canceled exec ("killing process because execution context was
canceled"), not a direct OOM — confirmed in daemon logs. So 137 is a
symptom; the cause is whatever cancelled first.

Fixes shipped (earth-side, no fork rebuild — fast iteration):

- A: chooseSolveError prefers a non-cancel monitor error over a canceled
  build error (builder/solver.go + red test). Probe: a self-cancel will
  now name its cause next run instead of "lost the session".
- B: cap `go test -p` to 2 in not-a-unit-test.sh to flatten the RSS peak
  that correlates with the kills.

Open question the probe resolves: if class-3 now prints "earth progress
monitor aborted the build: `cause`", it was earth-side all along; if it
still prints "lost the session", the cancel is genuinely transport/
daemon-side and memory (B) is the lever.

### SOLVED (2026-06-13, Opus): stats-stream decode aborted the build

Fix A (chooseSolveError) did its job as a probe. On the first clean run
(93043686), the class-3 failure printed its true cause instead of the
veil:

```text
earth progress monitor aborted the build: failed decoding stats stream:
unexpected stats stream protocol version 123
```

123 = 0x7B = '{'. The daemon's runc stats collector intermittently hits
EOF ("runc stats collection error: EOF", visible in buildkitd logs the
whole time) and emits a raw/partial frame where earth's parser expects
versioned framing (`[0x01][uint32 len][JSON]`). The parser errors,
vertexMonitor.Write returns it, MonitorProgress aborts, the errgroup
cancels, the running exec is SIGKILLed (137), and it all surfaces as
"BuildKit canceled or lost the solve session". Every prior theory
(scheduler race, healthcheck, flightcontrol, OOM) was downstream of this
single non-fatal-telemetry-treated-as-fatal bug.

Fix (e4cfa2ad, earth-side, no fork rebuild): stats decode failures are
now non-fatal — drop the bad batch and re-sync via Parser.Reset. Red
test reproduces a raw '{' frame and the recovery.

Optional deeper fix (fork): make the runc stats collector not emit a raw
frame on EOF. Not required for green; the earth-side guard is the correct
defensive design per this plan's own rule (reporting failures must remain
non-fatal).
