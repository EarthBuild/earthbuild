# Test plan

The native engine is testable because it runs alongside a live reference implementation throughout
development. Every correctness question reduces to "does the output differ from BuildKit, and is
the difference on the known exclusions list?" Steps are pure and content-addressed, so the oracle
is applied mechanically: normalise, diff, assert. The reference engine never disappears - BuildKit
remains a supported engine indefinitely - which means we never guess whether a failure is a
regression or a spec gap. This position (a live oracle from day one, plus four years of
issue-tagged regressions encoded in BuildKit's `client_test.go`, plus the existing 204-file test
corpus) is why the strategy is tractable at all: differential comparison substitutes for test
authoring, and new investment concentrates on the three bug classes the oracle structurally cannot
reach - distributed correctness, performance regressions, and security properties.

---

## Strand (a): matching BuildKit's hardened coverage

The goal is not to rewrite BuildKit's 123 `*_test.go` files. It is to run the same real workloads
against both engines, treat divergence as a build break, and harvest the conformance test
infrastructure that already exists in the BuildKit and containerd forks.

### a1. earth-diff: the differential oracle

**What it is.** A binary (`cmd/earth-diff`) that builds a target under both engines in parallel,
exports both artifact trees, normalises, and reports the first divergence as a structured
`(path, field, got, want)` tuple rather than a digest mismatch. Normalisation is the exclusions
table from `plan-native-engine.md` §2d, encoded as a typed Go slice - not a grep pattern - so it
is testable and auditable. A companion `cmd/corpus-filter` binary runs each candidate twice under
BuildKit, diffs with the same normaliser, and writes a `testdata/oracle-corpus/MANIFEST` of
screened files. New divergences not listed in `testdata/expected-divergences.json` fail the build;
growing that file requires `DIVERGENCE_APPROVED` in the PR description.

**What it catches that nothing else does.** Semantic divergence without a crash: wrong file mode,
missing xattr, wrong symlink target, missing env var in the image config, wrong exit code on a
build expected to fail. None of these appear as Go panics or test failures.

**Determinism screen.** Before any file enters the corpus, run it twice under BuildKit and drop it
if the outputs differ. Non-deterministic inputs (`RUN date`, unpinned tags, network fetches) cause
spurious failures on every CI run and train developers to ignore them.

**Priority corpus seed.** The first 7 files written for the corpus should be the issue-tagged
regression cases from `client_test.go`: `#276` (whiteout with parent dir, line 6197), `#2334`
(shared cache mount non-scratch base, line 5959), `#1336` (cache-export key loop, line 262),
`#2490` (move-parent-dir, line 6262), `#296`/`#319`/`#324` (symlink/rm edge cases, lines
6329-6375). Each represents a confirmed production bug; if our engine reproduces the fix, we know
the same class is handled.

| Detail          | Value                                                                    |
| --------------- | ------------------------------------------------------------------------ |
| First milestone | M1 - builds alongside the engine, not after                              |
| One-off cost    | 3-4 engineer-weeks (earth-diff, normalisation, corpus filter, CI wiring) |
| Ongoing cost    | 0.5 h/quarter for exclusions table review                                |
| New Go deps     | 0 - `google/go-cmp v0.7.0` already in `go.mod:22`                        |
| CI cost         | ~2-3 min/PR for the oracle matrix                                        |

**Exclusions table.** Every entry requires a stated reason. Reviewed quarterly as a whole.

| Excluded                             | Reason                                                                |
| ------------------------------------ | --------------------------------------------------------------------- |
| sub-second mtimes                    | intentional divergence (plan §2c); compare truncated to whole seconds |
| layer and image digests              | different tar writer and timestamps - compare contents, not digests   |
| `created` timestamps in image config | wall-clock                                                            |
| tar entry order                      | normalise by sorting before comparison                                |
| `/etc/hosts`, `/etc/resolv.conf`     | injected by the runtime during `RUN`; runtimes differ                 |

### a2. Ratcheted test-count gate

**What it is.** An Earthfile target `+native-test-count` that runs `go test -json ./tests/...
--engine=native`, counts `"Action":"pass"` events (not stdout grep - fragile on test names
containing the word "pass"), writes the integer to `native-test-ratchet.txt`, and fails CI if the
count is lower than the committed value. A `cmd/count-passing` binary (~50 lines) reads from
stdin, tallies pass actions, and compares. The count starts near zero at M1 and only goes up.

**What it catches that nothing else does.** Silent regressions in previously-working commands when
new code is added elsewhere. The tests were written for the old engine; the native engine is judged
against tests not trying to make it look good.

| Detail          | Value                           |
| --------------- | ------------------------------- |
| First milestone | M1                              |
| Cost            | ~0.25 engineer-weeks            |
| New Go deps     | 0                               |
| CI cost         | included in the normal test run |

### a3. Containerd conformance suites

**What it is.** The containerd fork ships two parameterised conformance suites that are free to
plug in against our own implementations:

- `core/snapshots/testsuite.SnapshotterSuite(t, "overlay", factory)` - 22 sub-tests covering
  prepare/commit/remove correctness under concurrency, parent-chain traversal, chown, mode-bit
  preservation, deletion of intermediate snapshots. Source:
  `/Users/gilescope/git/gilescope/containerd/core/snapshots/testsuite/testsuite.go:47`.
- `core/content/testsuite.ContentSuite(t, "local", factory)` - 13 sub-tests covering write-resume,
  digest verification, concurrent ingestion. Source:
  `/Users/gilescope/git/gilescope/containerd/core/content/testsuite/testsuite.go:49`.

Both take a factory `func(ctx, root) (Snapshotter, cleanup, error)` - register our overlay
snapshotter and content store with those factories. Zero new deps, no daemon.

Add a `check500LayersFlattening` case alongside the existing `check128LayersMount` (confirmed at
`testsuite.go:900`), asserting that a 501-step chain squashes before the OVL_MAX_STACK limit and
the result matches a flat build. Neither upstream tests above 128 layers; this gap is confirmed.

**What it catches that nothing else does.** The same bugs BuildKit's cache manager tests have
accumulated over years of production: wrong parent-chain traversal, incorrect chown across layers,
mode-bit corruption, content store write-resume corruption. Getting this coverage costs wiring
work, not test authoring.

| Detail          | Value                                                             |
| --------------- | ----------------------------------------------------------------- |
| First milestone | M1 - lands simultaneously with the snapshotter; cannot precede it |
| Cost            | 2-3 engineer-days (wiring cost only, not implementation cost)     |
| New Go deps     | 0 - suites already in the fork's dependency tree                  |
| CI cost         | included in the normal test run                                   |

### a4. Grammar-guided Earthfile generator

**What it is.** `internal/earthfile/gen/gen.go` - a recursive-descent generator mirroring the
production rules in `internal/earthfile/earthfile.abnf` (146 named productions, confirmed by grep).
Each production becomes a Go function; alternations are resolved by consuming bytes from
`AdaLogics/go-fuzz-headers.NewConsumer(data).GetBool()`/`GetInt()` (already an indirect dep,
confirmed in `go.sum`). Terminal strings come from a fixed pool of safe, deterministic values:
`echo hi`, `alpine:3.22@sha256:<pinned>`, `/out/file`. Depth is capped at 4 to prevent the
`recipe-instruction -> if-block -> recipe-line -> recipe-instruction` cycle from blowing up.

The generator emits a string, not an AST, so the parser is exercised too. Register as
`FuzzGenerate(f *testing.F)`, seeded from the screened oracle corpus.

**M1 scope:** the generator framework plus the three M1 productions (FROM, RUN, SAVE ARTIFACT)
only. Full 146-production coverage tracks M2-M5 as instructions land. The critical safety
constraint - only emit oracle-testable instructions, never `curl` or `date`, always emit pinned
refs - is written into the pool, not enforced after the fact.

**What it catches that nothing else does.** Instruction combinations the 204-file corpus never
exercises: nested IF inside FOR inside WITH DOCKER, RUN-split across a TRY/FINALLY boundary, ARG
with a dynamic expression inside a FOR loop body. The combinatorial space is unreachable by
hand-written tests.

| Detail          | Value                                                                |
| --------------- | -------------------------------------------------------------------- |
| First milestone | M1 for framework + M1 productions; M2-M5 for remaining productions   |
| Cost            | 2-2.5 engineer-weeks total; ~0.5 weeks/milestone for new productions |
| New Go deps     | 0 - `AdaLogics/go-fuzz-headers` already in `go.sum`                  |
| CI cost         | ~5 min/scheduled fuzz run                                            |

### a5. BuildKit scheduler unit-test port (35 tests)

**What it is.** Adapt `solver/scheduler_test.go` (3,961 lines, 35 `func Test*` functions,
confirmed in the fork at
`/Users/gilescope/go/pkg/mod/github.com/earthbuild/buildkit@v0.0.0-20260617184045-51fe8fb974fd/solver/scheduler_test.go`)
to `engine/sched_test.go`. Replace `solver.Solver`/`solver.Edge`/`solver.Vertex` with
`engine/ir.Node` and `engine/sched.Scheduler`. Keep the fake executor pattern: the
`testOpResolver`/`vertex`/`dummyResult` machinery (400-600 lines of scaffolding) becomes
`fakeBackend` implementing `engine/exec.Backend`. All 35 cases run with no daemon.

The import list (lines 1-22 of the original) confirms no daemon dependency: only `context`, `fmt`,
`math`, `math/rand`, `sync/atomic`, `testing`, `time`, standard identity/session packages, and
`testify`. The test vertices are their own executors via `Sys()`.

**What it catches that nothing else does.** Concurrent-graph correctness bugs invisible in unit
tests: a node executed twice when it should be once (false cache miss), a result not propagated to
all waiting jobs (stale reads), cancellation leaving the graph wedged, sub-build ordering
violations.

| Detail          | Value                                                                  |
| --------------- | ---------------------------------------------------------------------- |
| First milestone | M4 - when `engine/sched` first exists; cannot precede it               |
| Cost            | 2-3 engineer-weeks (scaffolding layer must be rewritten for our types) |
| New Go deps     | 0                                                                      |
| CI cost         | included in the normal test run; no daemon                             |

### a6. Port BuildKit's cache-storage test suite

**What it is.** BuildKit's parameterised suite `solver/testutil/cachestorage_testsuite.go`
(confirmed in the fork: `RunCacheStorageTests(t, stFn)` at line 17, 6 test functions) is
parameterised by `func() solver.CacheKeyStorage`. Copy the suite to
`engine/cache/testutil/cachestorage_testsuite.go` (do not import from the fork - the fork is the
oracle, not a forward dependency) and call it against the native engine's cache key store.

The six sub-tests cover: write results, read them back in order, release at single and multi-level,
walk backlinks, walk IDs by result.

**What it catches that nothing else does.** Contract violations the plan already pinned as a kill
criterion: result leak after release (unbounded growth), broken backlinks (incorrect invalidation
cascade), ordering bugs in result walk (stale cache hits). If we adopt `solver.CacheKeyStorage`
verbatim the suite runs as-is; if we define our own interface, porting 6 functions is 2-3 days.

| Detail          | Value                                                               |
| --------------- | ------------------------------------------------------------------- |
| First milestone | M2 - the native cache key store must exist; write this alongside it |
| Cost            | ~1 engineer-week                                                    |
| New Go deps     | 0                                                                   |
| CI cost         | included in the normal test run, with `-race`                       |

### a7. Cache-key soundness fuzzing

**What it is.** `inputgraph/hash_fuzz_test.go` using `testing.F`. Seed corpus: every `.earth`
file under `tests/` (parsed and serialised back to bytes). The mutator applies a random single-
field edit to the AST - change one ARG name, flip one RUN command, alter one FROM image ref,
insert one RUN into an IF body - and asserts `HashTarget` returns a different key for the mutated
version. Run nightly with `-fuzztime=60s`.

The gap targeted: `loader_hashing.go:7-17` shows `hashIfStatement` hashes `len(IfBody)` but body
coverage comes only via the `loadBlock` recursion elsewhere - an easy place to silently miss
adding a new field when a new node kind lands. The fuzzer finds this class of omission before it
ships a false cache hit.

AST mutation scaffolding (parse, apply one typed mutation, serialise) is the substantial cost; raw
byte mutation is not useful here because it produces syntactically invalid files.

**What it catches that nothing else does.** Missing fields in the cache-key hasher. Unit tests in
`inputgraph/hash_test.go` cover only cases the author thought to write; a structural mutator
covers additions that appear later. False cache hits are the E5 kill criterion (immediate stop,
`experiments-adversarial.md` line 328).

| Detail          | Value                                                                   |
| --------------- | ----------------------------------------------------------------------- |
| First milestone | M2 - after the cache key store exists                                   |
| Cost            | 1.5-2 engineer-weeks (AST mutation scaffolding is the substantial part) |
| New Go deps     | 0 - `testing.F` is stdlib since Go 1.18; repo is on Go 1.26             |
| CI cost         | ~60 s/scheduled run                                                     |

### a8. Native Go fuzzing: parser and lexer

**What it is.** `FuzzParse` already exists at `internal/earthfile/parse_test.go:1842` with five
inline seeds. Two remaining actions:

1. **`FuzzLex`** (does not exist - confirmed by grep): add to `internal/earthfile/lex_test.go`.
   The lexer at `lex.go:326` returns `*lexer`, not a channel; `nextItem()` at `lex.go:302` is a
   pull-based method. The correct harness body is:

   ```go
   l := lex("Earthfile", string(data))
   for i := 0; i < 10_000; i++ {
       item := l.nextItem()
       if item.Typ == itemEOF { return }
   }
   ```

   The 10,000-item cap converts a hang to a deterministic failure rather than a CI timeout.

2. **Corpus wiring**: wire the 116 `.earth` fixtures in `tests/*.earth` as `f.Add` seed files via
   `filepath.WalkDir` rather than inline literals.

**What it catches that nothing else does.** Parser panics on malformed input; infinite loops in
continuation-sequence handling (`line-continuation = backslash EOL`, ABNF line 81) that surface
today as CI timeouts rather than reproducible failures; the dual-nil / dual-non-nil parse result
invariant.

| Detail          | Value                                                                    |
| --------------- | ------------------------------------------------------------------------ |
| First milestone | M1 (`FuzzParse` already exists; only `FuzzLex` and corpus wiring remain) |
| Cost            | ~0.2 engineer-weeks                                                      |
| New Go deps     | 0                                                                        |
| CI cost         | ~5 min/scheduled run with `-fuzztime=5m`                                 |

Run on a scheduled job, not per-PR. Commit seeds to `testdata/fuzz/FuzzParse/corpus/`.

### a9. Metamorphic transforms on the oracle corpus

**What it is.** `internal/earthfile/metamorphic/` with five transforms operating on the parsed
`Tree` AST (`Command.Clone()` confirmed at `earthfile.go:63`), each fed into `earth-diff`:

1. **ARG inoculation** - append `ARG _unused_metamorphic = hello` to every target; outputs must
   be identical.
2. **Target-order shuffle** - sort targets alphabetically; any target reachable without
   `FROM +sibling` must produce identical output.
3. **RUN-split** - replace `RUN a && b` with two `RUN` steps; filesystem state must match. Skip
   commands containing `$(...)` or backticks (heuristic: `strings.SplitN(cmd, " && ", 2)` only
   when no subshell present).
4. **Comment injection** - add `# metamorphic comment` before every instruction; outputs must be
   identical.
5. **WAIT-wrapping** - wrap every independent-target set in `WAIT ... END`; outputs must be
   identical (WAIT is a sequencing hint, not a semantic change when there is no ordering
   violation). Only meaningful at M4+ when the scheduler exists.

**What it catches that nothing else does.** Layer-ordering bugs that produce wrong output without
crashing (RUN-split); ARG iteration-order dependencies (ARG inoculation); scheduler treatment of
WAIT as a semantic barrier (WAIT-wrapping). Both the first and last are in the class of bugs that
produce wrong output rather than a panic.

| Detail          | Value                                                                                                                   |
| --------------- | ----------------------------------------------------------------------------------------------------------------------- |
| First milestone | M1 for the transform functions (unit-testable without engine); M2 for the full CI harness (earth-diff must exist first) |
| Cost            | ~1.25 engineer-weeks for all five transforms plus the driver                                                            |
| New Go deps     | 0                                                                                                                       |
| CI cost         | ~2 min/PR running the corpus through all five transforms                                                                |

### a10. False-cache-hit adversarial harness (E5b)

**What it is.** `engine/cache/observed_input_test.go` with five deterministic cases from
`experiments-adversarial.md` E5b (lines 340-355), each calling the observation recorder API
directly with synthetic filesystem state:

1. `if [ -f /etc/present-only-in-B ]` - absent in A, present in B; assert failed `stat` is in the
   observation set.
2. `ls /dir` - A and B differ in directory contents but no file is opened; assert readdir digest
   is in the key.
3. A step that reads a file only on the true-branch of a conditional; assert both branches produce
   different keys.
4. A step whose behaviour depends on `$HOME`; assert the env entry is in the key.
5. A step that stat-checks for existence then ignores the file; assert keys differ between bases.

These are unit tests of the observation recorder, not integration tests. The recorder must record
failed opens, stats of absent paths, and full readdir results - not merely successful reads.
`plan-native-engine.md` lines 308-314 identifies the exact failure mode: a step doing
`if [ -f /x ]` reads nothing when x is absent, so a naive read-set omits it and falsely hits
cache against a base where `/x` exists.

All five must be green before L2 (observed-input) caching is enabled in any non-opt-in mode.

**What it catches that nothing else does.** False cache hits under L2 caching - the single
correctness property the plan treats as an immediate stop (`plan-native-engine.md` lines
283-285). Earth-diff compares engines; this tests cache correctness within one engine.

| Detail          | Value                                                                                |
| --------------- | ------------------------------------------------------------------------------------ |
| First milestone | M4 - when the §2a-bis observation recorder is built; write these as TDD for that API |
| Cost            | ~0.5 engineer-weeks once the recorder API is defined                                 |
| New Go deps     | 0                                                                                    |
| CI cost         | negligible - pure unit tests                                                         |

### a11. Resource-exhaustion limit table

**What it is.** `engine/limits_test.go`, integration-tagged, asserting the engine emits a
human-readable diagnostic - not a bare kernel string like `invalid argument` - before hitting
each structural limit:

1. A programmatically generated 601-step target (`FROM alpine` + 600 `RUN echo N > /fN` via
   `text/template`) hits OVL_MAX_STACK (E11, `experiments-adversarial.md` lines 463-471): assert
   the error mentions "overlayfs limit" or "layer flattening required", not bare `EINVAL` at step
   501. As a negative control, assert the same Earthfile fails under `--engine=buildkit` at step
   500 (or passes if BuildKit has fixed it). Accompanies `check500LayersFlattening` from a3 at
   the unit level.
2. ARG_MAX exceeded: a single `RUN` with argv > 128 KB; assert "argument list too long".
3. PATH_MAX exceeded: `SAVE ARTIFACT` with a path > 4096 bytes; assert a path-length error.
4. Large local context (500 MB); assert no silent OOM.

The ulimit open-file case is omitted: CI runners have `ulimit -n` of 1,048,576, making it
impractical to exhaust without a fragile loop.

**What it catches that nothing else does.** Bare kernel error strings reaching the user. The
500-layer wall was a production surprise (E11); this table pins it as a committed diagnostic and
extends the same discipline to other structural limits before they become user bug reports.
Earth-diff cannot catch wrong error messages - it compares outputs, not failure strings.

| Detail          | Value                                                                                       |
| --------------- | ------------------------------------------------------------------------------------------- |
| First milestone | M3 for case 1 (layer flattening); cases 2-4 at M4-M7 as each capability lands               |
| Cost            | ~1 engineer-week for the table infrastructure and case 1; ~0.5 weeks for cases 2-4 combined |
| New Go deps     | 0                                                                                           |
| CI cost         | `//go:build integration` tag; root required (available on `ubuntu-latest` runners)          |

### a12. Secret-leakage layer audit and LOCALLY gate

**What it is.** Two security tests:

**(a) `engine/exec/secret_layer_test.go`** (M5-M6): run a step with `--secret` where the value
is a known sentinel (`EARTHBUILD_TEST_SECRET_SENTINEL`); commit the snapshot diff; walk every byte
of the resulting tar with `archive/tar` and assert the sentinel does not appear in any file's
contents, any symlink target, or any xattr value. Also assert no file at `/run/secrets/X` appears
in the overlay upper dir (verifying the tmpfs mount is not captured).

**(b) `engine/exec/locally_gate_test.go`** (M3): run a LOCALLY step without `--allow-privileged`;
assert the build fails before the LOCALLY step executes and a canary file it would have created
does not exist. The remote-caller refusal variant (a worker-dispatched Earthfile containing
LOCALLY with `--allow-privileged` on the worker side) carries `//go:build fleet-integration` and
requires the fleet harness.

**What it catches that nothing else does.** (a) Secret bytes captured in a CAS blob -
`tests/secrets.earth` tests correct injection but has no assertion on committed layer content.
(b) Privilege escalation via LOCALLY from a remote context - `tests/allow-privileged.earth` tests
the flag but not the remote-caller axis. Both are stated requirements in
`rfc-post-buildkit-engine.md` §1d.

| Detail          | Value                                                                                       |
| --------------- | ------------------------------------------------------------------------------------------- |
| First milestone | M3 for the LOCALLY gate; M5-M6 for the layer audit (stable layer export machinery required) |
| Cost            | ~1.5 engineer-weeks (both non-fleet tests); +0.5 weeks for remote-caller variant            |
| New Go deps     | 0                                                                                           |
| CI cost         | negligible                                                                                  |

### a13. Race detector and concurrency stress

**What it is.** The `goTestRace` template at `util/proj/golang.go:63-74` already generates
`go test -race ./...`. Three additions:

1. Enable `-race` on `engine/...` from M1 (a CI flag, not engineering work).
2. `earthfile2llb/wait_block_test.go` (does not exist yet): N goroutines concurrently calling
   `wb.Add(item)` and `wb.SetDoSaves()` on the same `waitBlock` (which has
   `seenItems map[states.WaitItem]struct{}` under `wb.mu` at `wait_block.go:25-29`); assert no
   panic, no missed items, consistent final state.
3. At M4: a diamond-dependency scheduler test in `engine/sched`: A->B, A->C, B->D, C->D; assert
   D executes exactly once and only after both B and C complete, across 100 shuffled orderings.

**What it catches that nothing else does.** Data races in the WAIT/END machinery and the future
scheduler. Race conditions in build schedulers are reliably invisible to manual testing and
reliably caught by `-race`. Earth-diff cannot catch a scheduler race that produces wrong output
only under contention.

| Detail          | Value                                                                             |
| --------------- | --------------------------------------------------------------------------------- |
| First milestone | M1 for `-race` on existing packages; M4 for the diamond-dependency scheduler test |
| Cost            | ~0.5 engineer-weeks total                                                         |
| New Go deps     | 0                                                                                 |
| CI cost         | ~2x wall time on the test run (the `-race` penalty)                               |

### a14. mtime round-trip and no-recompile regression gate

**What it is.** Two tests that are explicit work items in `plan-native-engine.md` §2c
(remaining work items 2 and 3):

1. `engine/exec/mtime_roundtrip_test.go`: write a file into a fresh snapshot with mtime
   `1700000000.123456789`; call the native engine's diff writer (the containerd fork at
   `/Users/gilescope/git/gilescope/containerd`, branch `giles-nanosecond-mtimes`); apply the
   resulting diff into a second snapshot; `syscall.Stat_t` the file and assert
   `Mtim.Nsec == 123456789`. Also assert the SAVE ARTIFACT export path does not re-truncate.

2. `tests/mtime-no-recompile.earth`: build a minimal Rust crate (pinned
   `rust:1.82-alpine@sha256:<digest>`); re-run with no source change through a layer round-trip;
   assert zero `compiler-artifact` lines in `cargo build --message-format=json` output. Assert
   `--engine=native` passes and `--engine=buildkit` fails (intentional divergence per plan §2c).

**What it catches that nothing else does.** Earth-diff cannot catch silent mtime re-truncation
because the exclusion table normalises sub-second mtimes out of comparison. This is the only guard
against the containerd fork's writer fix being silently undone by a downstream exporter.

| Detail          | Value                                                                                 |
| --------------- | ------------------------------------------------------------------------------------- |
| First milestone | M2 for the write-side round-trip (containerd fork only); M3 for the no-recompile test |
| Cost            | ~1 engineer-week (both tests are named work items, not speculative)                   |
| New Go deps     | 0                                                                                     |
| CI cost         | ~3 min/PR (Rust compile is the slow part)                                             |

### a15. Tar writer round-trip fuzz (containerd fork)

**What it is.** `FuzzWriteDiffRoundtrip(f *testing.F)` in
`/Users/gilescope/git/gilescope/containerd/pkg/archive/fuzz_test.go`. Generates synthetic file
trees (names, contents, modes, mtime nanoseconds) using
`AdaLogics/go-fuzz-headers.CreateFiles(upperDir)` (confirmed in the API at `consumer.go:815`),
calls `WriteDiff`, decodes the tar with `archive/tar`, and asserts mtime nanoseconds are preserved
exactly for each regular file and symlink. Seeds: the two existing test cases in
`tar_mtime_test.go` (line 42, `TestWriteDiffPreservesNanosecondModTime`; helper `findTarHeader`
at line 104) plus boundary values (`nsec=0`, `nsec=1`, `nsec=999999999`). Also asserts the
`WithSecondPrecisionModTime()` opt-in path floors to zero.

**What it catches that nothing else does.** Symlink and hardlink mtime preservation - not covered
by the single-file hand-written test. PAX boundary values (`nsec=999999999`) that a single
hand-picked nanosecond cannot reach. `CreateFiles` generates the realistic directory trees that
exercise these paths.

| Detail          | Value                                               |
| --------------- | --------------------------------------------------- |
| First milestone | M1 in the containerd fork (write-side only)         |
| Cost            | ~0.3-0.4 engineer-weeks                             |
| New Go deps     | 0 - `AdaLogics/go-fuzz-headers` already in `go.sum` |
| CI cost         | ~2-3 min/scheduled run                              |

### a16. Capability gate test

**What it is.** `TestCapabilityGate` in `engine/engine_test.go`. A table-driven test where each
row is `{capability, earthfile_string, expectedMsgSubstring}`. For each capability not yet in the
native engine (initial list: WITH DOCKER, --secret, --ssh, LOCALLY, registry cache), assert:
`--engine=native` exits non-zero, stderr contains the capability name and the milestone that will
add it and the `--engine=buildkit` fallback text, and no partial artifact is produced. Example
expected message: `"native engine (M8): --secret not yet supported; use --engine=buildkit or wait
for M8"`. New capabilities are added to the table at their introducing milestone, not before.

This enforces `plan-native-engine.md` lines 203-207 mechanically. Without it the rule is prose.

**Landed, in a different place and shape.** `engine/core/capability_test.go` and
`engine/interp/refusalwhy_test.go` between them assert every clause above: the refusal names the
construct, the source location, the milestone and the `--engine=buildkit` fallback; and
`TestRefusalHappensBeforeAnythingRuns` puts the unsupported construct *last* in a three-step graph
and asserts nothing ran, which is the "no partial artifact" clause stated as a property rather than
as an absence.

Two differences from the sketch, both deliberate. It is not one table in `engine/engine_test.go`,
because the refusal happens in two places for two reasons - the interpreter refuses a construct
while reading, the scheduler refuses a graph before evaluating - and one table in one package would
have tested whichever it could reach. And the capabilities are a *list* consulted by the gate rather
than a list in the test, so a construct added to the engine and not to the gate fails rather than
passing silently.

§5.1 cited this item as what tests I10 until 2026-08-16, which understated the engine: the work was
done and the table still promised it.

**What it catches that nothing else does.** Silent partial execution - a `WITH DOCKER` that
no-ops instead of failing will pass every test that checks only the exit code, but silently skips
the Docker-in-Docker workload the user expected.

| Detail          | Value                                               |
| --------------- | --------------------------------------------------- |
| First milestone | M1 - must exist the moment `--engine=native` exists |
| Cost            | ~3 engineer-days                                    |
| New Go deps     | 0                                                   |
| CI cost         | subsecond (no real build needed)                    |

---

## Strand (b): testing distributed workers locally

Fleet testing cannot wait for real GitHub Actions runners. The goal is to catch three classes of
distributed bug that no unit test reaches: duplicate step assignment, worker loss without re-queue,
and blob integrity bypass. Testing proceeds in three layers - deterministic simulation first, then
in-process fault injection, then multi-process on loopback.

### b1. synctest-driven scheduler (deterministic concurrency)

**What it is.** `engine/sched` is designed from M4 with two injectable interfaces:

```go
type Clock interface { Now() time.Time; Sleep(d time.Duration) }
type Picker interface { Choose(ready []NodeID, load map[WorkerID]int, locality map[NodeID]WorkerID) WorkerID }
```

In tests, wrap the scheduler in `synctest.Test(t, func(t *testing.T) { ... })` (confirmed in use
at `internal/synccache/cache_test.go` lines 48, 69, 87 - 35 uses total). The fake clock does not
advance until all goroutines are blocked, so `synctest.Wait()` steps through all scheduling
decisions deterministically. Primary deliverable: `FuzzSched` - feed random sequences of node
completions and worker arrivals and assert every node executes exactly once.

The interfaces must be designed in at M4. Retrofitting later touches every `time.Sleep` call site
in `engine/sched`. This is not an add-on to an existing scheduler; it is a design constraint on
the scheduler from the first line.

**What it catches that nothing else does.** Scheduler races visible only under specific goroutine
interleavings: a node dispatched to two workers simultaneously; a result arriving after its worker
is declared dead triggering double-execute. These require thousands of `-race` runs to surface in
real time; synctest finds them in the first run.

| Detail          | Value                                                                                                    |
| --------------- | -------------------------------------------------------------------------------------------------------- |
| First milestone | M4 - when `engine/sched` first exists                                                                    |
| Cost            | ~0.5 engineer-weeks initial; ongoing code-review discipline for every new `time.Sleep` in `engine/sched` |
| New Go deps     | 0 - `testing/synctest` is stdlib since Go 1.24; repo is on Go 1.26                                       |
| CI cost         | negligible                                                                                               |

### b2. In-process MemTransport shim with fault injection

**What it is.** `engine/fleet/memtransport.go`: a `Transport` interface implementation (the seam
plan §3a describes as "keep go-iroh behind `engine/fleet/mesh` so it is swappable") connecting
goroutines via `net.Pipe()` pairs with injectable fault hooks:

```go
type FaultInjector struct { ... }
func (fi *FaultInjector) WithDropRate(p float64) *FaultInjector
func (fi *FaultInjector) WithDelay(d time.Duration) *FaultInjector
func (fi *FaultInjector) WithCorrupt(fn func([]byte) []byte) *FaultInjector
```

Three fault injection tests: (1) drop a blob mid-transfer - assert driver retries from a different
worker; (2) corrupt a blob - assert the BAO verifier rejects it and the driver re-fetches;
(3) delay a heartbeat past the timeout - assert the worker is declared dead and its steps
re-queued.

Note: `net.Pipe` is reliable and ordered, unlike QUIC. This shim tests fault-handling logic at
the message level, not the network layer. The multi-process harness (b3) tests real process death
separately. The shim is `synctest`-compatible and 10x faster than b3.

The `Transport` interface must be designed as part of Phase 3 architecture - this cannot be
retrofitted.

**What it catches that nothing else does.** Retry storms (driver re-fetching from a dead peer in a
loop), heartbeat races, and the BAO verification path that the happy path never exercises.

| Detail          | Value                                          |
| --------------- | ---------------------------------------------- |
| First milestone | Phase 3 - after the Transport interface exists |
| Cost            | ~1.5-2 engineer-weeks                          |
| New Go deps     | 0 - `net.Pipe` is stdlib                       |
| CI cost         | ~1 min/PR                                      |

### b3. Multi-process local fleet harness

**What it is.** `engine/fleet/localtest/` (build tag `//go:build integration`). Spawns N
`earth worker` subprocesses over Unix domain sockets (`--mode=worker --session=$id
--listen=unix://$tmpdir/$n.sock`), drives builds, asserts distributed correctness. Three test
cases:

1. **Step distribution**: 2-worker build of an Earthfile with 4 independent targets; assert each
   worker claims at least 1 step.
2. **Worker-loss re-queue**: `cmd.Process.Kill()` on worker 2 after a `step_claimed` log line;
   assert the build completes and output is byte-identical to a 1-worker run.
3. **Byte-identical output**: same build under N workers and 1 worker (`plan-native-engine.md`
   §3c exit criterion, lines 716-720).

Pattern from `rebuck2/tests/e2e-requeue.sh` (confirmed at
`/Users/gilescope/git/gilescope/rebuck2/rebuck2/tests/e2e-requeue.sh`, 72 lines): separate
`--store` dirs, log-scanning for join sentinel via `bufio.Scanner` on process stdout pipe,
`cmd.Process.Kill()` for fault injection. The binary is built in `TestMain` via
`exec.Command("go", "build", ...)` - budget 30-60 s for startup.

Wall-clock speedup (assert < single-machine wall / 2) is tracked as an informational metric, not
a CI gate: loopback processes on a shared runner will not reliably achieve 2x, and GitHub runner
timing varies 30-50%.

**What it catches that nothing else does.** Race conditions in step assignment where two workers
claim the same step; worker-loss handling that drops a step instead of re-queuing. These require a
running fleet - no unit test or in-process shim exercises real process death.

| Detail          | Value                                                                                                    |
| --------------- | -------------------------------------------------------------------------------------------------------- |
| First milestone | Phase 3 - fleet packages must exist; `earth worker` subcommand must exist (§3b, ~5 weeks)                |
| Cost            | ~1.5 engineer-weeks (not 1 - binary build in `TestMain` and `CAP_SYS_ADMIN` for overlay mounts add cost) |
| New Go deps     | 0                                                                                                        |
| CI cost         | ~3 min/PR; requires `ubuntu-latest` with privileged containers                                           |

### b4. Fleet security sub-tests

**What it is.** Extends b3 with three security sub-tests that require a running fleet:

(a) **Allowlist enforcement**: launch a worker with a fresh ed25519 key pair not in the driver's
allowlist (`rfc-post-buildkit-engine.md` §1d requirement 3); assert the driver logs a refusal
before the build starts and completes on legitimate workers.

(b) **Blob integrity**: a fake worker serving a blob with the correct content-ID but wrong bytes;
assert go-iroh's BAO verifier rejects it and the driver re-fetches or fails clearly.

(c) **LOCALLY from a remote caller**: a worker-dispatched Earthfile containing a LOCALLY step;
assert the driver refuses even with `--allow-privileged` on the worker side.

Prerequisite: confirm go-iroh QUIC forms connections over loopback without a STUN/relay hop
(one afternoon experiment) before building this harness.

| Detail          | Value                                                        |
| --------------- | ------------------------------------------------------------ |
| First milestone | Phase 3 - after b3 and the Transport interface exist         |
| Cost            | ~1 engineer-week additional on top of b3                     |
| New Go deps     | go-iroh (already required for Phase 3 - no incremental cost) |
| CI cost         | shared with b3                                               |

### b5. Goroutine-leak detection

**What it is.** In `TestMain` for `engine/fleet/`, record
`before := runtime.NumGoroutine()` after a 100 ms settle. Each fleet test `t.Cleanup` checks the
count against `before`. When exceeded, log `runtime/debug.Stack()` (all goroutine stacks, stdlib,
no dep) so the failure is debuggable. Run with `-count=3` so leaks accumulate and become visible.
Pair with `-race` (already in CI via `goTestRace`).

Prefer the `settledGoroutines()` pattern from `internal/synccache/cache_test.go:740` over a raw
`runtime.NumGoroutine()` snapshot - GC-looping until stable avoids false positives from parallel
tests sharing a process.

`goleak` is explicitly ruled out by `AGENTS.md` ("Do not add golang dependencies unless asked").

**What it catches that nothing else does.** Worker goroutines not stopped on context cancellation,
blob-streaming goroutines abandoned after a fault, gossip goroutines that keep running after mesh
teardown.

| Detail          | Value                |
| --------------- | -------------------- |
| First milestone | Phase 3              |
| Cost            | ~0.25 engineer-weeks |
| New Go deps     | 0                    |
| CI cost         | negligible           |

---

## Strand (c): performance testing

Performance tests are not aspirational targets. Every threshold is anchored to a concrete
measurement in `experiments-adversarial.md` with hardware and date provenance. A benchmark without
a known baseline is noise; a benchmark anchored to an experiment is a regression gate.

### c1. Benchmark suite: per-step floor, fixed overhead, and diff capture

**What it is.** `engine/bench_test.go` (build tag `//go:build perf`), five `testing.B` functions:

1. **`BenchmarkPerStepFloor`**: 20-step Earthfile, all `RUN echo $i`. Assert
   `b.Elapsed()/20 < 250 ms` cold, `< 20 ms` warm. Anchored to E11 (200 ms/16 ms;
   `experiments-adversarial.md` lines 488-492) with 25% slack.

2. **`BenchmarkFixedOverhead`**: `FROM alpine + RUN true` with warm cache. Assert total wall
   < 100 ms under `--engine=native`. Anchored to E10 (1,377-1,626 ms under BuildKit).

3. **`BenchmarkDiffCapture100k`**: 100 k-file tree with 10 k changed files, mirroring E4's setup.
   Assert `WriteDiff` < 2 s (upper-only path). Anchored to E4 (1,535 ms upper-only, 21,818 ms
   double-walk; `experiments-adversarial.md` lines 216-239). Lives in the containerd fork
   (`/Users/gilescope/git/gilescope/containerd/pkg/archive/`) - that is where `WriteDiff` lives.

4. **`BenchmarkPeakRSS`**: run the largest Earthfile in `tests/` under `--engine=native`; assert
   `runtime.ReadMemStats().Sys` does not exceed 5 GB. Anchored to E8 kill criterion
   (`experiments-adversarial.md` lines 393-401). Use `runtime.ReadMemStats` not
   `/proc/self/status` (platform-independent).

5. **`BenchmarkColdStart`**: start the engine fresh per iteration (use `-benchtime=1x -count=10`,
   not a normal `b.N` loop); assert p50 < 200 ms under `--engine=native`. Anchored to E10
   (2.4 s cold start under BuildKit; `experiments-adversarial.md` lines 413-435). Tracked as
   informational in CI; not a hard gate (too variable on shared runners).

Baseline stored in `testdata/perf-baseline.json`. Regression gate: fail if benchmarks 1-4 regress
by more than 15%. Use `go tool benchstat` via a `tools.go` pin for statistical noise handling.

Run both engines as sub-benchmarks (`BenchmarkXxx/buildkit` and `BenchmarkXxx/native`) in a single
binary invocation so they share scheduler noise and thermal state; the ratio is more stable than
either absolute figure on a non-pinned runner.

`golang.org/x/perf` for `benchstat` is a justified exception to the no-casual-deps rule:
`go tool benchstat @latest` needs network access on every CI run, which is worse than a pinned
dep. Requires explicit user consent before adding to `go.mod`.

| Detail          | Value                                                                                                |
| --------------- | ---------------------------------------------------------------------------------------------------- |
| First milestone | M1 for `BenchmarkDiffCapture100k` (containerd fork, no engine needed); M2 for benchmarks 1-2 and 4-5 |
| Cost            | ~1 engineer-week                                                                                     |
| New Go deps     | `golang.org/x/perf` for `benchstat` via `tools.go` (requires consent)                                |
| CI cost         | ~4 min/scheduled run on a pinned Linux runner; zero on default PR runs (build tag excludes them)     |

Only run on a pinned Linux runner. `ubuntu-latest` is not pinned.

### c2. Scheduler throughput micro-benchmark

**What it is.** `BenchmarkSchedulerPerStep` in `engine/sched/sched_test.go` using the `FakeBackend`
(zero latency). Runs a 1,000-step linear DAG (each step depends only on the previous); reports
ns/op as the pure scheduler overhead per step. Acceptance criterion committed to
`engine/sched/bench_baseline.txt`: must not regress more than 20% from baseline.

Separately, `BenchmarkColdStartFloor` measures the time from `os.Exec` of
`earth --engine=native true.earth+true` to process exit on a warm-cache rebuild. Target: under
200 ms (vs today's ~1,400 ms for BuildKit). This benchmark correctly treated as informational
only in CI (too variable); track via a PR comment rather than a gate.

**What it catches that nothing else does.** Scheduler overhead regressions - an O(N^2) graph walk,
a lock held in the hot path, a per-step allocation that generates GC pressure - that do not appear
in functional tests but make watch mode useless for large builds.

| Detail          | Value                                                |
| --------------- | ---------------------------------------------------- |
| First milestone | M4 - when `engine/sched` and the `FakeBackend` exist |
| Cost            | ~2 engineer-days                                     |
| New Go deps     | 0                                                    |
| CI cost         | ~30 s/nightly run                                    |

### c3. User-facing OTel trace for slow-build diagnosis

**What it is.** The engine emits one OTel span per scheduler decision, covering: `sched.realise`,
`exec.run` (with platform and step hash as attributes), `snapshot.prepare`, `diff.capture`,
`cas.put`, and `registry.pull`. A `--trace` flag exports the completed build's trace as a
Perfetto-compatible JSON file the user can open at `ui.perfetto.dev`.

`internal/telemetry/telemetry.go` already sets up an OTel tracer (confirmed: `telemetry.Tracer()`
at line 29). `go.opentelemetry.io/contrib/exporters/autoexport v0.69.0` is already in `go.mod:38`
and handles `OTEL_EXPORTER_OTLP_ENDPOINT=file://./earth-trace.json`. Zero new deps.

**What it catches that nothing else does.** Slow builds that are not regressions but are
user-reported bugs: a specific step taking 5 s because of an unexpected registry pull, a
diff-capture hitting the double-walk path instead of the upper-only path. Without spans these are
diagnosed by adding temporary logging; with spans the user provides the trace in the bug report.

| Detail          | Value                                   |
| --------------- | --------------------------------------- |
| First milestone | M2 (stub `--trace` flag can land at M1) |
| Cost            | ~1 engineer-week                        |
| New Go deps     | 0                                       |
| CI cost         | ~2 min/PR                               |

### c4. Crash-safety: SIGKILL mid-build CAS consistency check

**What it is.** `engine/crash_test.go` (build tag `//go:build integration`). Start a build of a
multi-step Earthfile, SIGKILL the earth process at a random point after at least one step has
written a blob to the content store (via a test-only `EARTH_CRASH_AFTER_STEP=N` env var gated by
build tag), restart, and assert: (1) the content store passes a consistency check (every blob
referenced by a committed manifest exists and its digest verifies); (2) the restarted build
completes without corrupting previously-written blobs.

The hard part is not blob write safety (containerd's content/local uses tmp-then-link atomic
writes) but manifest-commit atomicity: a SIGKILL after a blob write but before the manifest is
committed leaves a blob unanchored. The test verifies the restarted build does not re-use a
partially-committed manifest.

**What it catches that nothing else does.** Partial writes leaving the CAS in an inconsistent
state after SIGKILL. No other mechanism exercises a crashed-and-restarted engine.
`plan-native-engine.md` §2a lines 268-270: "every step is pure and therefore retry-safe" - this
is the invariant being pinned.

| Detail          | Value                                                                           |
| --------------- | ------------------------------------------------------------------------------- |
| First milestone | M2                                                                              |
| Cost            | ~1-1.5 engineer-weeks (manifest-commit atomicity is harder than the blob check) |
| New Go deps     | 0                                                                               |
| CI cost         | `//go:build integration`; ~2 min/PR                                             |

**Landed, and the hard half turned out not to exist here.** The sketch's difficulty is
manifest-commit atomicity - *"a SIGKILL after a blob write but before the manifest is committed
leaves a blob unanchored"* - which is a property of containerd's model. **This engine has no
manifests.** What references a result is an action-cache entry, it is written from `res.Layer` after
the layer is committed, and `Lookup` refuses a claim whose layer is absent. So a crash can leave a
layer with no entry, which is garbage, and never an entry with no layer, which would be a claim
pointing at nothing.

`TestABuildKilledMidFlightLeavesAUsableStore` covers both clauses: every surviving layer is
readable, and every surviving cache entry names a layer that exists. The second is clause (1)
translated, and it pins the *ordering* rather than the tolerance - reversing the two writes passes
every other assertion in that file and fails this one.

A temporary file left behind is deliberately allowed. A crashed build cannot be expected to have
tidied, and the store's rules make it harmless; asserting a clean store would be asserting something
the invariant does not claim.

---

## Test pyramid

Proportion of ongoing engineering effort:

| Layer                                                     | Proportion | Rationale                                                                |
| --------------------------------------------------------- | ---------- | ------------------------------------------------------------------------ |
| Differential oracle (earth-diff + ratchet)                | 40%        | Replaces hand-authoring thousands of tests; highest ROI per hour         |
| Integration tests (resource limits, security, fleet e2e)  | 30%        | Cover bug classes the oracle cannot reach (security, distributed, crash) |
| Unit / property tests (fuzzing, synctest, benchmarks)     | 20%        | Cheap to run; catch parser, scheduler, and cache-key bugs early          |
| Corpus maintenance (determinism screen, exclusion review) | 10%        | Without this the oracle becomes noisy and trusted less over time         |

The oracle dominates because it is the mechanism that makes strand (a) tractable without rewriting
BuildKit's test suite. The integration layer dominates the remainder because distributed and
security bugs are invisible to differential testing. Unit tests are cheap and should be written
first (TDD for the cache key store, the observation recorder, the scheduler) but they are not
where the coverage leverage is. The corpus maintenance budget is not optional: an unscreened
corpus is a noise source, not an asset.

---

## What we will NOT test

| Not tested                                        | Why                                                                                                            |
| ------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Layer-digest compatibility between engines        | Two engines never mix (plan Phase 1). Inter-engine interop tests are undefined.                                |
| BuildKit internals                                | The fork is the oracle; we test against its outputs, not its code.                                             |
| QUIC NAT traversal in CI                          | Real GitHub Actions runners (E6 experiment) are one-time, not a continuous gate. NAT is environment, not code. |
| macOS backend correctness (before M3)             | Until Linux is solid and the dual-engine matrix exists, macOS is development-only.                             |
| Platform affinity in the local fleet harness      | On a single-architecture host, affinity tests test emulation, not scheduling. Use a heterogeneous CI matrix.   |
| Sub-second mtime preservation under BuildKit      | BuildKit does not preserve them. This is documented as intentional divergence.                                 |
| Non-deterministic Earthfiles in the oracle corpus | Screened out by the determinism filter. A non-deterministic file is a noise source.                            |
| Wall-clock speedup as a CI gate on local fleet    | Loopback processes on a shared runner do not reliably achieve 2x. Track as informational.                      |
| Ulimit open-file exhaustion                       | CI runners have `ulimit -n` of 1,048,576; exhausting it requires a fragile loop.                               |

---

## CI ratchet: what breaks the build

These are the numbers tracked from M1. Any regression is a build break, not a warning.

| Metric                                        | Mechanism                                                       | What breaks the build                                                                                              |
| --------------------------------------------- | --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| Corpus targets that plan                      | `corpus-ratchet.txt`, one line per `GOOS`                       | Count drops **or rises** without the file being updated                                                            |
| Corpus targets in `WITH DOCKER` files         | `corpus-ratchet.txt`, `<GOOS>-docker`                           | Same rule on a slice the whole-corpus count is too coarse to protect (E389)                                        |
| Targets in `tests/*.earth`                    | `corpus-ratchet.txt`, `<GOOS>-earthtests`                       | 116 Earthfiles the corpus walk never saw, because it matches the name `Earthfile` (E410)                           |
| Docker-daemon and nesting tests               | `+engine-daemon`, privileged, `RAN_FLOOR=6`                     | A test fails, or fewer than six ran, counted by name - a green target that verified nothing (E387, E400)           |
| `tests/` **building** under the native engine | `corpus-ratchet.txt`, `<GOOS>-earthtests-run`, `+engine-daemon` | Count falls below the committed floor. Twelve targets attempted, sixty seconds each; a prefix, not a sample (E428) |
| Earth-diff failures on screened corpus        | `testdata/expected-divergences.json`                            | New failure not in the file, or file grows without `DIVERGENCE_APPROVED` in the PR description                     |
| Per-step floor (warm)                         | `BenchmarkPerStepFloor` on pinned Linux runner                  | `b.Elapsed()/20` exceeds `20 ms * 1.15`                                                                            |
| Fixed overhead                                | `BenchmarkFixedOverhead` on pinned Linux runner                 | Total wall exceeds `100 ms * 1.15`                                                                                 |
| Diff-capture upper-only path                  | `BenchmarkDiffCapture100k` on pinned Linux runner               | `WriteDiff` on 100 k-file tree exceeds `2 s * 1.15`                                                                |
| Peak RSS                                      | `BenchmarkPeakRSS` on pinned Linux runner                       | `runtime.ReadMemStats().Sys` exceeds 5 GB                                                                          |
| Parser fuzz corpus                            | Seed files in `testdata/fuzz/FuzzParse/corpus/`                 | Any new crash or hang                                                                                              |
| Lexer fuzz                                    | `FuzzLex` with 10,000-item cap                                  | Any panic or item-count exceeded (i.e., infinite loop)                                                             |
| Cache-key soundness                           | `FuzzHashSoundness` nightly                                     | A mutation that does not change the key                                                                            |
| E5b false-cache-hit gate                      | `engine/cache/observed_input_test.go`                           | Any of the five adversarial cases returns a hit                                                                    |
| Crash-safety                                  | `engine/crash_test.go` (integration-tagged)                     | Restarted build re-uses a partially-committed manifest                                                             |

The exclusion table in `testdata/expected-divergences.json` is reviewed quarterly as a whole.
Growing it without removing a compensating entry requires justification, because that table is
where "equivalent" quietly becomes "similar".

A ratchet file is updated by the engineer who moves the number. The only legal direction is upward;
a PR that regresses the count is rejected unless the removed test itself was wrong (in which case
the removal is the explicit change, not a side effect).

**`corpus-ratchet.txt` exists and is enforced. `native-test-ratchet.txt` does not exist**, and this
table described it for months as though it did - which is the difference between a plan and a
mechanism, and the reason the first one now says which it is (E353).

The corpus ratchet fails on a **rise** as well as a fall, which the row above says and this
paragraph is where the argument goes: a ratchet that lets an improvement pass unrecorded stops
protecting the level that was reached, and the next regression is measured against a number nobody
has updated since. It is asserted where the count is computed, so the number in the log and the
number being checked cannot disagree.

## The Earthfile corpus

`engine/interp` runs every Earthfile in this repository - 192 of them - through the interpreter and
asserts one property: each target is either **planned** or **refused actionably**, where actionable
means the message says *where* (a location, a quoted name, a target) and *what to do* (a remedy or
the other engine). It never panics and never fails with a bare message.

Those are the two outcomes that make a partial engine worse than none: one loses the build, the
other leaves a user unable to tell whether the fault is theirs.

**It is also the build queue.** The refusals are counted and ranked, so the next construct to
implement is whichever the corpus says is most common, rather than whichever seems interesting:

| Date       | Targets planned | Top gap    |
| ---------- | --------------- | ---------- |
| 2026-08-13 | 59              | `DO` (149) |
| 2026-08-13 | 162             | `DO` (173) |
| 2026-08-13 | 210             | `IF` (158) |

The jump from 59 to 162 was one bug: Earthfiles have a base recipe - commands before the first
target, inherited by every target - and the interpreter ignored it. That defect was invisible in
the engine's own tests, because someone writing tests for their own interpreter writes targets
that begin with `FROM`.

**Two readings, and they disagree.** A refusal deep in a chain of references is inherited by
everything that reaches it, so counting *refusals* ranks by blast radius: one remote `FROM` in one
file accounted for 182 refused targets through four levels of reference. That is the right measure
for choosing what to fix - one line unblocks 182 targets - and badly overstates how much is left.

The report therefore counts both. 701 refusals come from **265 distinct causes**, and the ranking by
cause looks nothing like the ranking by target:

| causes | targets | construct            |
| ------ | ------- | -------------------- |
| 41     | 54      | `BUILD` options      |
| 41     | 51      | `WITH`               |
| 28     | 33      | `LOCALLY`            |
| 23     | 44      | missing context file |
| 12     | 12      | cycles               |

A cause is the *deepest* location in the error chain, since that is the line to change.

**The twelve cycles are correct.** They are every target in
`tests/cli/testdata/infinite-recursion/Earthfile`, a fixture that exists to contain infinite
recursion for the engine that already ships. This engine finds the same cycles, including the
three-hop one, which is an independent check on the detector that no test written beside it can
give - and is now asserted directly rather than noticed in a report.

**Running it elsewhere.** The corpus root comes from `EARTH_CORPUS_DIR`, defaulting to the
repository. A cross-compiled test binary therefore runs against a mounted tree:

```bash
tar czf corpus.tgz $(find . -name Earthfile -not -path "*/node_modules/*")
# on the target machine, with corpus.tgz unpacked at /corpus:
docker run --rm -v /corpus:/corpus:ro -e EARTH_CORPUS_DIR=/corpus alpine /bins/interp.test
```

Skipping when the corpus is absent was considered and rejected: a coverage test that quietly finds
nothing reports success while testing nothing. It fails instead, naming the variable to set.

## Pinning the argv

`engine/interp` asserts the exact command line each `RUN` produces, for a table of shapes whose
*meaning* depends on quoting: a nested `sh -c`, a pipeline, a redirect inside quotes, an undeclared
variable, an escaped dollar.

It exists because a change to expansion rewrote what commands ran, and every test still passed.
Quotes were removed from `sh -c "echo x > /f"`, the redirect moved to the outer shell, the inner
shell received only `echo` - and the build **succeeded**, writing an empty file. Nothing in the
suite used a command whose meaning depended on its quoting, so nothing noticed.

The guard was checked by reverting the fix and confirming it fails. A guard that cannot fail is the
same defect as the vacuous tests this suite has removed twice.

**Assert contents, not exit codes.** The end-to-end artifact test now runs its text through a
pipeline and compares the exact result. A build that loses its quoting still exits zero; only the
bytes tell you. "Not empty" would not have been enough either - a half-working pipeline produces
something.

Two layers, deliberately:

| layer            | catches                                   | cost           |
| ---------------- | ----------------------------------------- | -------------- |
| argv assertions  | any change to what the shell will receive | milliseconds   |
| end-to-end bytes | that the argv was the *right* one         | a sandbox, ~3s |

The first is the regression net; the second is the proof. Neither substitutes for the other, and
the empty artifact slipped through precisely because only the second existed.

## The same constructs, both backends

The case table is written once and run twice: through a host target on this machine, and through a
sandboxed one in a VM. Same commands, same expectations; only the preamble differs, because a
`LOCALLY` target writes into the project directory and a `FROM` target needs a `SAVE ARTIFACT` to
carry the result out.

It is a **differential** test, and that is the value: a construct that behaves differently in a
sandbox than on this machine is a bug in one of them, and neither suite alone can say so.

| suite   | cases | time | needs                     |
| ------- | ----- | ---- | ------------------------- |
| host    | 11    | 0.3s | nothing                   |
| sandbox | 19    | 37s  | a VM, a network, an image |

Conditions the plan cannot decide are tested through a seam rather than a sandbox. `interp.Conditions`
is supplied by the caller, so which conditions get evaluated, what they are evaluated against, and
what happens to a failure are all assertable with a fake on any machine in milliseconds. The two
properties worth the most are negative ones: a decidable condition must **never** reach the
evaluator - spending a sandbox on a string comparison at every `IF` in every Earthfile - and with
no evaluator at all the condition must still be refused, so a plan-only caller never starts a
sandbox behind its own back.

The sandboxed evaluator is proved end to end on four conditions that *cannot* be decided any other
way - `[ -f /flag ]` after the step that writes it, and `command -v` for something installed and
something not. The unit tests cover the mapping (exit zero is true, non-zero is false, could-not-run
is neither) and the e2e proves the prefix actually runs; neither alone is enough, because a mapping
can be right about a build that never happened.

**a1, the differential oracle, exists in miniature.** `TestBothEnginesProduceTheSameArtifact`
builds the same Earthfile with `earth` and with this engine and compares the artifact byte for
byte. Five constructs, agreeing, in about 17 seconds. It grows by adding rows to a table, which is
the point of the strategy this plan opens with: comparison substitutes for test authoring.

The sandbox suite ends where a build tool's claims can be settled by someone else: an image this
engine wrote is loaded by `skopeo` and run by `docker`, and has to print what the build put in it.
Every test above that line checks this engine against its own understanding. This one does not, and
it is the only one that can say the output is *right* rather than *consistent*. It skips wherever
skopeo, docker or the sandbox is missing, so it costs nothing where it cannot run.

The sandbox suite shares **one image cache per machine** (`EARTH_IMAGE_CACHE_DIR`), which is not a
detail: a cache per case re-fetched the base image every run until Docker Hub rate-limited it, and
the tests then reported the quota as a skip. A suite that turns a missing dependency into a skip
goes green while its coverage empties, and only the *duration* gives it away. Rate-limit skips are
now zero.

The differential against the reference engine needs `EARTH_TEST_ORACLE=1` as well, because that
engine can wedge in a way no timeout in the test interrupts.

`scripts/verify-engine.sh` runs the lot: gofmt, vet on this machine and on linux/amd64, the tests,
and a short race pass. `--net` adds the sandbox suite. It exists because the checks had been a
string of shell commands with `&& echo OK` on the end, and **four times in one day that OK printed
when the check had not passed** - once because the package had not compiled, once because a test
binary had timed out, twice because the echo was not attached to the thing it claimed to report.

A status line that is not conditional on the result is not a check. The script is
`set -euo pipefail`, each step reports its own outcome, and the final line is only reachable if
nothing exited non-zero. It is mutation-proven the way the tests are: a misformatted file, a
failing test and a package that will not compile each produce the right `FAIL` lines and exit 1 -
the last of these being the exact case that produced three of the four false signals.

The corpus sweeps skip under `-short`, and the reason is a measurement rather than a preference:
six of them now walk every Earthfile in the repository, and race instrumentation turned a 40-second
package into a **373-second** one. `go test -short -race ./engine/...` is 12 seconds and is what a
change gets checked with; the sweeps run in full on every ordinary pass.

That timing was found by a check reporting success when it had timed out - the `echo RACE_OK` in
the loop ran whether or not the grep matched anything. A status line that is not conditional on the
result is not a check, and this is the fourth of that family today.

The table has a floor. `minimumCases` fails the suite if it ever holds fewer cases than it did,
because an edit meant to add five cases once matched nothing, added none, and left the suite green.
A passing run is evidence that what ran passed, never evidence of how much ran. Lowering the floor
is deliberate and reviewable; drifting below it is neither.

The sandbox suite shares one cache across all its cases. A cache per case re-pulled the base image
nineteen times in half a minute, which an anonymous registry quota answers with 429 - and a suite
that reports someone else's quota as its own failure teaches its readers to discount its failures.

**Eight cases are sandbox-only, and the reason is not a gap.** Two kinds of construct mean
different things on the two backends, and it is right that they do:

- `COPY`, because a host target has no image to copy *into*;
- anything naming an absolute path, because `/script` is the image root in a sandbox and the
  **machine's** root on a host. A shared case writing to `/` would be a build tool writing to the
  root of a developer's filesystem, and the host refusing it is the system working.

They are skipped on the host *with that reason* rather than quietly dropped, so the coverage
difference stays visible instead of hiding behind a green run. A differential can only compare
constructs whose meaning is shared; pretending otherwise would compare nothing and report agreement.

The ratio is the argument for having both. The host suite runs on every change, everywhere,
including a Linux container with no runtime; the sandbox suite proves the fast one is measuring the
right thing. Running only the slow one means running it rarely, which is how a suite stops catching
things.

## Builds that actually run

`engine/cli` runs a table of complete builds - parse, plan, schedule, execute, export - one
construct at a time, and asserts what ended up on disk. Ten cases, **0.2 seconds**, no sandbox, no
image, no network.

Host steps are what make it affordable. A `LOCALLY` target needs nothing but the machine, so the
whole path can be exercised on any platform in milliseconds. Before this, end-to-end coverage meant
booting a VM and pulling an image, which is why there was so little of it.

**The corpus measures what plans; this measures what happens.** The distinction is not academic:
the corpus was blind to a build that planned perfectly and then demanded a sandbox it never used,
and is blind by construction to everything after the graph exists.

It found four defects on its first run:

- `DO` inside a `LOCALLY` target was refused, because it demanded a filesystem that a host target
  deliberately does not have;
- a function called from a host target lost its host-ness, so every `RUN` inside it asked for a
  base image;
- a host step received **only ε**, which leaves no `PATH` - so a `LOCALLY` target could not run
  `tr`, `mkdir`, or anything that is not a shell builtin;
- two of the cases were wrong in the test itself, in the ordinary way shells are confusing.

The third is a reversal worth stating plainly. ε is restricted for a sandboxed step because it must
*bound what the step observed*, or the key is a claim about something that read more (I3). That
reasoning does not reach a host step: it is unsandboxed, so nothing bounds it, so it is never
cached (I7). There is no key to keep sound, and the restriction cost the entire feature while
buying no correctness.

## Building the corpus, not only planning it

`TestCorpusTargetsActuallyBuild` in `engine/cli`, gated on `EARTH_TEST_BUILD=1`, runs corpus targets
instead of planning them.

It exists because this engine kept proving that the two are different. `COPY x .` planned perfectly
and failed in the guest; `WORKDIR` followed by `COPY` planned perfectly and put the files at the
filesystem root; a step had no `/dev`; `ENV` took `PATH` with it. Every one of those produced a
flawless plan, and the corpus test - which only plans - reported them all as successes.

**It reports rather than fails.** A corpus of other people's Earthfiles needs networks, credentials
and tools this machine does not have, and a test that went red for those would be a test nobody
reads. It prints how many targets built and names the ones that did not, which is a number to watch
rather than a gate to pass.

Targets are filtered to ones that could plausibly run here: nothing naming another repository,
nothing needing a secret, and no `LOCALLY` - which would run commands from a stranger's Earthfile on
the developer's own machine. `EARTH_TEST_CORPUS` points it at a different tree; the default is this
repository's `examples/`.

## Which packages run in parallel

`t.Parallel()` is not applied uniformly, and the split is deliberate:

| Package                                         | Parallel | Why                                                     |
| ----------------------------------------------- | -------- | ------------------------------------------------------- |
| core, image, guest, layer, blob, cache, sim, ir | yes      | pure: temp dirs, fake registries, no process-wide state |
| interp                                          | no       | `copy_test.go` chdirs, and its subject *is* the cwd     |
| cli, exec                                       | no       | share VMs, the image cache and the layer store          |

The one file that chdirs is the whole reason `interp` is serial - not `t.Setenv`, which is the
objection everybody reaches for first. A test whose subject is the working directory cannot be made
parallel by tidying; it would have to stop being that test.

`cli` and `exec` are a different refusal. They could be made to work with enough per-test isolation,
but a suite that runs eight sandbox VMs at once measures how much memory the machine has, and a test
that fails on a laptop and passes in CI is worse than a slow one.

**Parallelism is a test, not just a speed-up.** Making 197 tests parallel turned up a data race in
`ir.(*Node).ID()` that had been invisible for the life of the engine, because a serial suite never
had two goroutines on one node - see E24. Anything added to a parallel package should stay parallel
for that reason, and `-race` over the parallel packages is a stronger check than `-race` over the
serial ones.

## The store a test builds into has to be deletable

A build store holds unpacked layers with their modes intact, which is not incidental - it is what
makes a step's filesystem right. `maven:3.8.5-openjdk-17` ships a directory that denies writing, and
removing a file inside such a directory needs permission on the *directory*, not on the file. So
`os.RemoveAll` cannot clear a store, and `t.TempDir` cleans up with `os.RemoveAll`.

The corpus build test found this the honest way: it built everything it was asked to and then failed
its own cleanup. `storeDir(t)` in `engine/cli` is the fix - a directory whose cleanup is
`image.RemoveAll`, registered after the TempDir's own so it runs before it and leaves nothing to trip
over. Anything used as `EARTH_CACHE_DIR` in a test comes from there.

Deliberately *not* folded into `useStore`, which is called once per case with a store the whole suite
shares: deleting it there would clear the cache between cases that are meant to share it. Lifetime
belongs to whoever creates the directory, not to whoever points at it.

## What counts as an engine failure in the corpus

The corpus build number is only useful if it moves for reasons someone can act on, so a failure is
put in one of two buckets and the buckets are kept honest.

**This machine's**, not counted against the engine:

- an image with no manifest for the sandbox's architecture, or one whose binaries are for another -
  matched on the engine's own wording, which is why that wording lives in one place
- a step that probed the filesystem's case behaviour and got ESTALE, *on a store the engine has
  already reported as case-insensitive*

A third kind was added after a sweep counted one: a **registry that answered 502**. Two targets of
the same Earthfile got the correct architecture refusal and a third failed fetching a layer, which
is E15's territory - a bad minute at Docker Hub - rather than anything anyone can act on from here.
Anchored on the request and not the number: a step is entitled to print "502" itself, and a build
that failed because *its own* server misbehaved is a build that failed.

**Both halves of that second rule are required.** ESTALE on its own is a symptom this engine could
perfectly well have caused, and treating every one as environmental would hide exactly the failures
the count exists to show. Pairing it with the engine's own note about the disk is what makes it a
statement about the machine. `examples/next-js` earns the rule: it fails this way on a stock Mac and
builds end to end when the store is case-sensitive (E25).

`TestACaseInsensitiveStoreIsNotAnEngineFailure` pins all four cases, including the one that must
*not* be laundered - the same panic with no note beside it.

## The corpus is built in a copy

`SAVE ARTIFACT ... AS LOCAL` writes where the Earthfile says, and for a corpus of tutorials that is
next to their own sources. The first full sweep left **35 files in the repository** - jars, bundled
javascript, compiled binaries, and a `package.json` that one Earthfile deliberately writes back -
all untracked, and all indistinguishable from work once staged. 58,000 lines of build output came
within one `git add -A` of being committed.

`corpusRoot` copies the corpus into a `t.TempDir()` and builds there. The whole tree, not each
Earthfile's own directory: an example may reach a sibling, and a corpus that half-works is worse
than one that does not run at all. `TestTheCorpusIsBuiltInACopy` pins both halves - that the root is
not the real `examples/`, and that the copy actually holds the corpus, because a copy that silently
came up empty would report zero targets and read as a pass.

The general rule this is an instance of: **a test that runs someone else's build must not run it
where the build can write to the repository.** Cleaning up afterwards is the version of this that
gets forgotten.

## The differential oracle

`TestBothEnginesProduceTheSameArtifact` builds the same Earthfile with this engine and with the one
that ships, and compares what comes out. It is the only check that this engine agrees with the
implementation people actually use, and everything else here is a check that it agrees with *us*.

Run it with `scripts/verify-engine.sh --oracle`. It sits behind its own flag rather than `--net`
because it drives a daemon in a container, and a wedged daemon does not fail - it stops making
progress, in a way no context deadline interrupts, and takes the rest of the run with it. That is
not hypothetical: it is why this test skipped for days.

**A case may supply a whole Earthfile.** The original table wrapped each body in a single target,
which covers what one recipe does - and the interesting disagreements between two engines are about
what one target means to *another*. Those need two targets:

```earthfile
build:
    SAVE ARTIFACT index.js /dist/index.js   # a name in a namespace, not a path
probe:
    COPY +build/dist dist                   # a directory in that namespace
```

That case exists because the rule it tests was **inferred rather than looked up**: `COPY
+target/<dir>` was implemented from reading `examples/tutorial/js/part2` and deciding what it must
have meant. The tutorials are evidence about the shipping engine, not a specification of it. The
oracle turns the inference into a check, and both arms - the directory and the artifact named in
full - agree with the reference.

**A differential that passes is only worth having if it can fail.** Breaking the namespace expansion
so every entry lands at the destination root turns exactly one case red and leaves the other six
green, which is what a comparison is supposed to do. Worth re-doing whenever a case is added: a
differential that cannot distinguish the engines is a slow way of testing nothing.

Done again for the symlink case (E74), and it is the reason to keep doing it: stubbing out the link
resolution turns that case red with `copy_file_range: is a directory` - the engine copying a link
where a tree was meant - and every other case stays green. Four seconds of work to know the case has
teeth, against a case that would otherwise have joined the table green and stayed green whatever
happened to the code.

## The vocabulary guard

A claim about what this engine *cannot* do has no executable consequence, so it survives the moment
it stops being true. Three of them did, in one week:

- a note said a target named `base` was accepted here; the parser had always refused it (E36)
- the plan said `LOCALLY` was refused; it plans, runs, and matches the reference (E42)
- the first draft of the guard itself said `GIT CLONE` was refused, on the strength of an
  `unsupported("GIT CLONE --keep-ts")` call site - which refuses a *flag*, not the command

Each was believed for exactly as long as it went unchecked, and each would have cost whoever trusted
it either a re-implementation or a wrong plan.

`TestTheVocabularyIsWhatWeSayItIs` writes the claims down where the suite can disagree with them.
Every command in the language gets a minimal use and a `supported` boolean, and the test fails in
**both** directions: a construct that starts working is as loud as one that stops.

Two details that make it worth having rather than a list that rots differently:

- **Only a refusal by name counts.** A missing file or a target that saves nothing is the fixture
  being wrong, not a gap, and treating those as evidence would fill the table with false absences.
- **A fixture that errors while accepted is logged, not swallowed.** `GIT CLONE` needs a runner the
  plan-only caller does not provide, so it is accepted and then fails - and a fixture that quietly
  stopped exercising its command would otherwise pass forever.

The general shape: **notes about presence get tested, notes about absence get believed.** Anything
this engine declines belongs in a table a test reads, not in a paragraph a person reads.

**The same guard exists one level down, for flags.** `TestTheFlagsAreWhatWeSayTheyAre` covers the
options on COPY, SAVE ARTIFACT, RUN and CACHE, for the mirror-image reason: the command table exists
because `LOCALLY` was refused in the notes and not in the engine, and the flag table exists because
`--keep-ts` was refused by the *engine* while this engine already did exactly what it asks. One
direction costs a re-implementation; the other turns away a build that would have been correct.

It is not a hypothetical guard. Putting `--keep-ts` back into the refusal list turns it red:

```text
COPY --keep-ts is refused, and this table says it is supported
```

So the bug that took a hand-audit of the refusal list to find is now caught by the suite. That
audit is the thing worth not repeating - a list of what a system declines is a specification, and
reading one by hand is how the last three stale claims survived.

Note what these two guards do *not* do: they say nothing about whether a supported construct is
supported *correctly*. `VOLUME` was accepted and silently dropped from the image for as long as
anyone can tell (E39). Presence is what the differential is for; these tables only pin the shape of
the answer.

## The corpus is this repository first

The ratchet's largest Earthfile is the one in this repository's root: 32 targets, the tool building
itself, and every construct a real project uses rather than the ones a tutorial demonstrates.

`-dry-run` is the cheap half of it. Resolving a plan runs the front end whole - parser,
interpreter, argument expansion, conditions, loops, artifact resolution - and runs only the steps a
`$(...)` or an `IF` genuinely needs. Ten targets resolve in a few minutes and that sweep found
three engine defects the tutorial corpus could not reach (E48), because no tutorial builds a
directory over several steps, saves it, and reads it back.

The sweep is now a test. `TestTheRepositorysOwnTargetsPlan` resolves ten of this repository's own
targets - `+go` through `+all-binaries` - and fails if any of them stops planning or plans nothing
at all. Thirty-seven seconds for the ten, which is cheap enough to sit in the sandbox suite beside
everything else.

**It catches what it is for**, checked by breaking the artifact-stack merge (E48) and watching
`+lint` fall over exactly where it did the first time:

```text
+lint does not plan: FOR at Earthfile:129:
"find . -name go.mod -print0 | xargs -0 dirname" exited 123
```

Ten rather than all thirty-two: several targets want credentials, a registry or a released version,
and **a ratchet that needs secrets is a ratchet that gets skipped**. It also asserts each plan has
steps in it - a target that resolved to nothing would pass an error check while measuring nothing,
which is the way this kind of sweep usually rots.

Planning rather than building, deliberately. Resolving a plan runs the whole front end and only the
steps a `$(...)` or an `IF` genuinely needs. What it cannot catch is a step that fails when run.

**`TestTheRepositoryBuildsItself` closes that gap for the target that matters.** It builds
`+earthly` - the whole tool - and asserts the result is a Linux arm64 ELF executable of a plausible
size at `build/linux/arm64/earthly`, which is a path made from two built-in arguments and one that
is declared nowhere. Behind `EARTH_TEST_BUILD` like the corpus sweep, because it is a minute rather
than a second.

It removes `build/` from its copy first, and that line is the test. Without it the assertion found a
gitignored 49 MB binary from 2020 that the copy had brought along - so the test passed with the
engine sabotaged, which is how E62 found that it measured nothing. **A build test that does not
clear its output directory is asserting the past.**

**A warning about the guest, which cost two false negatives in the session that found those
defects.** `earth-guestd` is a separate Linux binary running inside a VM that outlives the build, so
`go build ./cmd/earth-native` changes nothing about the code that does the copying. A probe after a
guest-side fix reports the *old* engine, and a false negative that looks like a fix that did not
work is the most expensive kind. Rebuild it with `GOOS=linux GOARCH=arm64` and take the sandbox
down with `-stop-sandbox` before believing a measurement.

## The Linux materialiser runs in CI

The conformance suites for `engine/mat/overlay` used to skip wherever they were most useful. A
container's root is overlayfs, overlayfs will not stack on itself, and the repository's own
`+unit-test` - which is what CI runs - is a container. So the second implementation of the
materialiser port was exercised only inside the VM on a developer's Mac (E69).

`overlay.Mountable` now tries the caller's directory, then any tmpfs already present, then one it
mounts itself, and returns the unmount alongside. 25 subtests run there now where 2 skipped.

The rule it keeps: an error that is **not** `ErrUnavailable` is still a failure. Trying harder to
run must not turn a broken materialiser into a skip, which is the way a conformance suite retires
without anybody deciding to.

## The message-stability guard

`TestEveryRefusalSaysTheSameThingTwice` produces each of five refusals twenty times and requires one
answer.

It exists because the engine's own determinism check found a varying error message **about one run
in six** - two map orders agree half the time, and the message only appears for targets that get
refused - so a real defect was recorded twice as an unexplained sighting before anybody caught it
(E66, E67). A property checked probabilistically is not checked.

Twenty repetitions is the forcing function: Go randomises map iteration per loop, so a pair of runs
would be a coin flip. It also asserts each refusal names its file or its target, because a message
that was consistently *empty* would pass a comparison of two empty strings.

## The clamp guard

`TestEveryMtimeIsClampedOrExcused` reads the engine's own source and requires every `os.Chtimes`
call to pass a time that came through `stamp()`, or to sit on a list that says why it does not.

Source-reading is a blunt instrument and is used here because the property is about *where code is*
rather than what it computes. Three times a second piece of copying code appeared beside the first
and disagreed with it about timestamps - `SAVE ARTIFACT` of a file against a directory, and then
`COPY` of a file against `COPY --dir` (E47). A behavioural test catches the arm it exercises; this
one catches the arm at the moment it is written, which is the only point at which the author is in
a position to notice.

The exemption list has one entry, `image/unpack.go`, and it is not a concession: unpacking a
downloaded image writes the times from its tar header, and those belong to the image rather than to
this build. Clamping them would alter layers the engine did not make and break the digests it has
just verified (I8). The guard **logs its excused sites on every run**, so an exemption cannot
become invisible by being tolerated.

Two mutations check it: an unclamped write is reported by file and line, and a walk finding fewer
than three writes fails outright - a source-reading guard that reads nothing otherwise passes for
the best-looking wrong reason there is.
