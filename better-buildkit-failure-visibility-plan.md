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
