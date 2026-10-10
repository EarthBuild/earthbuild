# Scheduling

Scheduling answers three questions simultaneously: *what order* steps run, *on which worker*,
and *when to begin prefetching* their inputs. The user's framing - understand the tree, estimate
time and space, partition the workload - is correct as far as it goes. Two gaps matter for
EarthBuild:

1. **Progressive discovery.** Classical DAG scheduling (HEFT, PEFT, Ninja's subtree-weight)
   assumes the full graph is known before any step runs. Ours is not: `FROM` with a
   runtime-resolved tag, `IF`, and `ARG`-driven `BUILD` commands reveal children only after a
   parent executes. The scheduler must produce a valid schedule over the *known* prefix while
   updating priorities lazily as nodes are added (plan §2a-preq.

2. **Priority and placement are orthogonal.** *When* a step runs (critical-path order) and
   *where* it runs (data locality) are separable decisions. Conflating them - as BuildKit's FIFO
   does - makes both wrong.

---

## Objective function

Three metrics compete. A scheduler without a stated objective is a pile of heuristics.

| Metric             | Definition                                            | Conflicts with   |
| ------------------ | ----------------------------------------------------- | ---------------- |
| **Makespan**       | Wall-clock time from invocation to last step done     | Runner-minutes   |
| **Runner-minutes** | Σ(workers × duration); billing cost on GitHub Actions | Makespan         |
| **Bytes moved**    | Total layer data transferred between workers          | Neither directly |

**Primary objective: minimise makespan.** Users are waiting.

**Locality-first placement** typically reduces bytes moved *and* makespan simultaneously, because
transfer latency is on the critical path (measured: 0.44 ms Linux / 7.7 ms macOS solve round
trip; layer transfer dominates for steps above the ~200 ms machinery floor). When they conflict,
makespan wins.

**Runner-minutes as a budget constraint, not an objective.** Accept up to 2× serial runner-minutes
for a 2× makespan reduction. Beyond that, fleet overhead does not pay (plan §3.0, E7).

---

## Decision table

| Domain       | Scheduler decides                                      | Must never decide (green-paper §4.7)                | Left to the worker                   |
| ------------ | ------------------------------------------------------ | --------------------------------------------------- | ------------------------------------ |
| Ordering     | Priority within the ready set                          | Override dependency edges                           | Execution sequence inside a step     |
| Placement    | Which platform-eligible worker runs a step             | Ignore platform affinity                            | Container/VM lifecycle               |
| Barriers     | When all steps in a WAIT block are satisfied           | Execute any step after WAIT before the block clears | -                                    |
| Host steps   | Route `LOCALLY` to the invoking machine                | Route a `LOCALLY` step to a remote worker           | Host OS interaction                  |
| Cache lookup | Trigger L1/L2 lookup before dispatch                   | Change what a step returns (§4.2)                   | Cache storage format, layer transfer |
| Prefetch     | Initiate blob fetch before a step enters the ready set | Fetch blobs the step will not read                  | Blob-store I/O, network protocol     |
| Records      | Record outcome in build record                         | Alter the result to match a prior run               | Snapshot creation, diff capture      |

---

## 1. BuildKit: keep and drop

Source: `solver/` in `github.com/earthbuild/buildkit@v0.0.0-20260617184045-51fe8fb974fd`.

### What it does

The scheduling atom is `Edge = {Vertex, Index}` where `Index` is the output slot
(`types.go:44`). A vertex with N outputs creates N independently schedulable edges. One goroutine
runs `loop()` (`scheduler.go:72`) over a FIFO linked list (`s.next`/`s.last`). `signal(e)` appends
to the tail in goroutine-completion order (`scheduler.go:221`). `dispatch(e)` steps each edge
through a four-state machine: `Initial → CacheFast → CacheSlow → Complete` (`edge.go:14-21`).

**Two-phase cache:**

- *Fast key* (CacheFast): definition hash of the op + dep fast keys; no I/O. Probed via
  `op.Cache().Query(...)` (`edge.go:205`).

- *Slow key* (CacheSlow): content hash of a materialised dep result. Requires the dep to reach
  Complete. Computed via `ComputeDigestFunc` (`edge.go:839`).

**Mid-flight merge:** if two in-flight edges compute the same composite key, `mergeTo` re-wires
all their pipes to one survivor (`scheduler.go:181-202`, `317-357`). The index lookup at
`scheduler.go:184-196` is keyed by computed composite key, not pointer; whichever goroutine
registers first becomes the merge destination.

**Throttling** lives in the worker layer: `op.Acquire(ctx)` (`jobs.go:953`). The scheduler fires
goroutines freely with no concurrency cap.

**Placement:** none. `ResolveOpFunc` (`jobs.go:24`) maps a vertex's `Sys()` to an `Op`
implementation; `VertexOptions.WorkerConstraint` (`types.go:52`) exists only as a comment -
never implemented. Platform affinity must be designed from scratch.

### What we keep

| Keep                                  | Reason                                                   |
| ------------------------------------- | -------------------------------------------------------- |
| Atom = (step, output-slot)            | Finer dedup; disjoint subtrees can prune earlier         |
| Two-phase cache (fast → slow)         | Maps directly to our L1/L2 lookup (green-paper §4.3-4.4) |
| Merge on key collision                | Same content → same node; avoids double execution        |
| State-machine dispatch (non-blocking) | Loop must never block on I/O                             |
| Throttling at worker layer            | Scheduler emits tokens; worker pulls - clean boundary    |

### What we drop

| Drop                                                                | Reason                                               |
| ------------------------------------------------------------------- | ---------------------------------------------------- |
| FIFO queue                                                          | Goroutine arrival leaks into schedule order          |
| `map[*edge]` pointer keys                                           | Unstable identity; use content-addressed step digest |
| No priority function                                                | Cannot express critical-path preference              |
| No data-locality signal                                             | Pays full transfer cost every run                    |
| No platform affinity in scheduler                                   | §4.7 requires it as a hard constraint                |
| Map iteration in `recalcCurrentState` (`edge.go:550`)               | Non-deterministic key selection                      |
| Map iteration in `loadCache` (`edge.go:890`)                        | Non-deterministic cache-record tie-breaking          |
| Merge direction by arrival order (`scheduler.go:184-196`)           | Merge dest depends on goroutine race, not content    |
| `s.incoming`, `s.outgoing` appended in arrival order (`:290, :311`) | Pipe order varies across runs                        |

Five confirmed non-determinism sites - all must be absent from the native scheduler. The
`getAllMatches` function in `index.go:174-238` iterates a plain Go map; a `BTreeMap` keyed on a
stable canonical key gives the same semantics deterministically.

---

## 2. What the shipping engine tried

### Two uncoordinated converter layers

**Layer 1 (conversion):** `ConvertOpt.Parallelism semutil.Semaphore` (`earthfile2llb.go:60`)
limits how many targets convert to LLB concurrently. The interpreter processes Earthfile
statements sequentially; async conversion is blocked when `AutoSkip` is set.
`FOR`-loop parallelism and `LOCALLY` parallelism were attempted and abandoned because the
interpreter was the concurrency-control point.

**Layer 2 (execution):** BuildKit's solver, unaware of layer 1.

The two layers are uncoordinated: the conversion semaphore can starve the worker pool. The native
engine collapses both into one graph; the interpreter is never a concurrency gate.

### WAIT/END barriers

`wait_block.go:295-332` (`waitStates`) implements WAIT/END as an `errgroup` fan-out *outside* the
graph. The solver cannot see or reason about it. The `NewMultiSem` at `wait_block.go:316` uses
`semutil.NewWeighted(1)` as the second argument to guarantee at least one goroutine can always
progress - load-bearing deadlock prevention that must be preserved in whatever barrier mechanism
the native engine uses.

In the native engine, WAIT/END must be a first-class barrier node in the IR so the scheduler sees
it as a dependency edge, not a side-channel (green-paper §4.7.1).

### TICKTOCK: the prototype native scheduler

`solver/simple.go` (behind `--ticktock`, `Hidden: true` at `flag/global.go:284`) was an attempt
at a native solver. It is the clearest record of what does and does not work.

**Good parts:**

- `exploreVertices` (`simple.go:327-352`) does a DFS post-order toposort with deduplication via
  a seen-set. Deterministic and correct; handles diamond dependencies by keeping only the deepest
  occurrence. *Copy this toposort shape.*

- `cacheKeyRecurse` (`simple.go:493-524`) hashes the op's CacheMap digest, each dependency's
  selector and slow-computed digest, and all ancestor inputs into a single chain key. This is
  structurally identical to green-paper κ₁. Using the computed key rather than the LLB digest
  (`vertex.Digest()`) as the dedup mutex key is essential: the same effective operation can have
  different LLB digests from different ancestry contexts. Discovered in commit d4e630c48 (then
  re-fixed in d144e62af after regression). *Use the computed key from day one.*

**Bad parts:**

- `build()` (`simple.go:130-182`) iterates the toposort in a single `for` loop calling
  `buildOne()` serially. A 10-target `BUILD` is 10× slower than BuildKit's edge scheduler.
  *Replace with a ready-queue and worker pool.*

- `parallelGuard.acquire()` (`simple.go:536-578`) blocks concurrent callers via a goroutine that
  ticks at 100 ms intervals. With the measured per-step floor of ~16 ms warm cached, a serialised
  pair of jobs pays ~6× the cache-hit cost in mutex latency. `parallelGuardWait = 100 ms`
  (`simple.go:22`). *Replace with `singleflight` or a `sync.Cond` per key.*

- `runOnceCtrl` (`simple.go:43-67`) uses a 2 000-entry LRU. When the LRU overflows,
  earlier entries are evicted and their `hasRun()` returns false again, causing re-execution
  of source operations that may read a different file or resolve a different git ref than the
  first execution. *Replace with a per-build `map[key]struct{}` cleared at build-start.*

- No WAIT/END integration. No inline cache export (stubbed: `Exporter: nil`, `simple.go:166`).

The logging churn (eight-plus logging-only commits in the 62-commit Mar-May 2024 series)
signals the prototype was chasing non-deterministic failures caused by the wrong-mutex-key and
missing-full-lock bugs. The native engine must enforce these invariants structurally rather than
discovering them through debugging.

### auto-skip

`converter.go:2240` (`Parallelism.Acquire`): the `checkAutoSkip` path hashes the Earthfile AST
and transitive deps without executing; if the hash is in the skip DB the whole target is
bypassed. In the native engine this is subsumed by L1 cache: a target whose input set is
unchanged hits κ₁ on every step. No separate skip database is needed and the `WITH DOCKER`
parallelism incompatibility disappears.

---

## 3. Dagger and peers

### Dagger (dagql)

**E-graph congruence** (`cache_egraph.go:25-46`): each `egraphTerm` stores `selfDigest` (op
shape), `inputEqIDs` (equiv-class IDs of inputs), and `termDigest = hash(selfDigest, inputEqIDs)`.
When two terms share a `termDigest`, their output equivalence classes are merged transitively -
the Downey-Sethi-Tarjan congruence-closure shape. This is strictly stronger than BuildKit's
point-in-time key merge: it detects that two differently-derived steps are the same step even
when their chain keys differ (the §2a-bis problem). Read `cache_egraph.go` in full before
finalising native node identity.

`TreeSet` is used for result sets and candidate lookups; `firstResultDeterministicallyAtLocked`
(`cache_egraph.go:509`) picks the smallest `sharedResultID` - a monotonically assigned integer.
For our distributed case, replace the session-local integer with lexicographic order on blob
digest so the selection is reproducible across machines.

**Three-tier cache lookup** (`lookupMatchForDigestsLocked`: `cache_egraph.go:680`; structural
tier: `lookupMatchForCallLocked`: `cache_egraph.go:707`): Recipe (O(1) hash map) → Content
digest → Structural e-graph term lookup. `CacheHitRoute` (`cache_evidence.go:22`) records per-call
which tier was hit - purely observational, no scheduling effect. Map onto our three tiers (L1
chain-key, L2 observed-input, structural) for the §1a.4 explainable-cache-miss requirement.

**Post-facto equivalence** (`TeachContentDigest`, `cache_egraph.go:1031`): after a step
completes, its content digest is injected into the e-graph asynchronously under a read-verify-
commit mutex loop (not hardware CAS). Future calls whose content digest matches hit
`CacheHitRouteDigest` without re-execution. This is the implementation of green-paper §2a-bis
observed-input caching - learning equivalence from content after execution.

**Platform-as-resource** (`sessionSatisfiesResourceRequirementsLocked`, `cache_egraph.go:654`):
rejects candidates whose `requiredSessionResources` the caller does not hold. A result requiring
`amd64` is rejected on `arm64`; the miss is recorded as `MissIncompatibleCandidates` for
diagnostics. Adopt this as the first-class model for §4.7 platform affinity.

**Implicit inputs** (`cache_inputs.go:36, 56`): `PerClientInput`/`PerSessionInput` are mixed into
call identity without appearing in visible arguments. Map to: host-step scoping (mix in machine
ID) and trust-domain scoping (namespace prefix for untrusted PR builds, §5.3).

**Architecture boundary confirmed.** Dagger handles equivalence and dedup; BuildKit handles
placement. Our native engine unifies these - but keep the conceptual boundary: recognising
equivalent steps is not the same decision as deciding where to run them.

### Bazel / Skyframe

Change pruning (if a dep re-evaluates to the same output, downstream skips re-execution) = our
L2 key (§4.5). Critical-path tracking is diagnostic in `--profile` output, not a scheduling
input. Build it from day one in build records even if it does not drive scheduling initially.

Dynamic execution (racing local vs remote with `invokeAny`) is not transferable: in a p2p fleet,
data locality is knowable in advance from the CAS; racing wastes compute and runner-minutes.

### Buck2 / DICE

Monadic/dynamic graph (deps discovered at runtime) matches our design. Early cutoff = our L2
key. Gang scheduling with coarse locality constraints is the right model for placing multi-input
steps near their data. Note: the specific constraint strings (`network_domain`, `datacenter`)
cited for Buck2's RE API may be Meta-internal - treat as *UNVERIFIED*; the locality intent
transfers regardless.

### Ninja 1.14

`EdgeWeightHeuristic` uses `prev_elapsed_time_millis` from `.ninja_log` as edge weight; the
scheduler dispatches highest subtree-weight first. Measured: 11-21% wall-clock improvement on
real C++ projects. New edges with no history default to 1 ms (see §6 for why this is wrong and
what to use instead). Named pools throttle concurrent instances of a rule independently of global
`-j` - maps directly to our `--parallelism` cap and `WITH DOCKER` throttle groups.

### Nix

Substituter-first (check binary caches before building) = our L1/L2 lookup before execution
(green-paper §4.3). Static `speedFactor` for worker weighting is reportedly non-functional in
some configurations; replace with observed throughput (steps/second) from build records -
self-tuning rather than administrator-configured. Content-addressed early cutoff = our L2 key.

---

## 4. Algorithms

### HEFT (primary, Sched-2+)

Topcuoglu, Hariri, Wu. "Performance-Effective and Low-Complexity Task Scheduling for Heterogeneous
Computing." *IEEE TPDS* 13(3):260-274, 2002. doi:10.1109/71.993206.

```text
rank_u(t) = mean_cost(t) + max over successors s of (comm_cost(t, s) + rank_u(s))
```

Computed in reverse topological order O(v²p). Tasks sorted descending by `rank_u`; each placed
on the worker with earliest finish time (EFT = max(worker-ready, data-ready) + execution-time).

**Communication cost** = estimated output blob size ÷ measured inter-worker bandwidth.

**§4.7 hard filter first.** Platform mismatch, WAIT/END barriers, `LOCALLY` pin, stack depth,
and dependency order are enforced before EFT comparison. A worker failing any constraint is
ineligible regardless of EFT.

**Stability guarantee.** For fixed cost estimates, the schedule is deterministic. Tie-break by
step digest (lexicographic on BLAKE3) to guarantee reproducibility across runs. Same graph +
same estimates + same worker set = same schedule, every time.

**Progressive adaptation.** Nodes with unknown successors contribute zero downstream rank
(conservative underestimate; correctly orders the known prefix). Recompute only ancestors of
newly-revealed nodes: O(ancestors_affected × p) per discovery event, not O(v²p) for the full
graph.

### PEFT (upgrade, Sched-3+)

Arabnejad, Barbosa. "List Scheduling Algorithm for Heterogeneous Systems by an Optimistic Cost
Table." *IEEE TPDS* 25(3):682-694, 2014. doi:10.1109/TPDS.2013.57.

Builds an Optimistic Cost Table (OCT): best achievable completion if each successor runs on its
individually optimal processor. Prevents the HEFT "greedy trap" where placing step S on the
fastest worker delays S+1 and S+2 more than it saves. Adopt when > ~50% of steps have recorded
durations; with fully unknown durations OCT degenerates gracefully to HEFT.

### Work stealing (idle-worker fallback only)

Blumofe, Leiserson. "Scheduling Multithreaded Computations by Work Stealing." *JACM*
46(5):720-748, 1999. doi:10.1145/324133.324234.

Expected makespan T₁/P + O(T∞) - but the proof assumes zero communication cost. In a distributed
fleet, blob transfer dominates for steps above the 200 ms machinery floor. **Use only as
idle-worker fallback, constrained to same-platform workers.** The LIFO locality property (push
and pop from the same end of the deque) translates: prefer re-using the same worker for a chain
of dependent steps (it already holds the prior step's output layer).

### Heteroprio / locality scoring

Bramas. "Impact study of data locality on task-based applications through the Heteroprio
scheduler." *PeerJ CS* 5:e190, 2019. doi:10.7717/peerj-cs.190.

```text
locality_score(step, worker) = Σ size(l) for l ∈ step.inputs where l ∈ worker.CAS
                              / Σ size(l) for l ∈ step.inputs
```

Assign to the worker with the highest score within the platform-eligible set. The NUMA hierarchy
from the paper is replaced with a flat worker score. Gains in a GitHub Actions fleet (cross-runner
transfer bandwidth ~1-10 GB/s, blobs up to hundreds of MB) will exceed the measured 12-31% NUMA
improvement because absolute transfer cost is larger.

CAS inventory is already maintained for prefetch decisions (plan §2a-bis, so
locality scoring adds no new data structure - it is a different read of the same data.

### HRW hashing (stable step-to-worker assignment)

Thaler, Ravishankar. "Using Name-Based Mappings to Increase Hit Rates." *IEEE/ACM ToN* 6(1):1-14,
1998. doi:10.1109/90.664262.

```text
score(step, worker) = BLAKE3(step_id || worker_id)
assign step to argmax_worker score
```

Pure function of (step, worker set). When a worker leaves, only its steps reassign (~1/N steps
migrate). O(N) per decision. Strictly preferable to consistent hashing at small N (2-8 workers).
Use as tiebreaker within the platform-eligible set after locality score.
Step identity is already a 32-byte BLAKE3 digest (green-paper §4.4); concatenate with the
worker's ed25519 public key (green-paper Appendix C.1).

### CRUSH (hierarchical placement, Sched-3+)

Weil, Brandt, Miller, Maltzahn. "CRUSH: Controlled, Scalable, Decentralized Placement of
Replicated Data." *SC '06*, 2006. ssrc.ucsc.edu/Papers/weil-sc06.pdf

Pseudorandom walk down a weighted cluster map. Each runner is a leaf node tagged with its
platform. Gives locality-stable placement with failure-domain separation as a first-class rule,
no directory service required. Transfer: the placement and membership-change parts. Reject:
replication rules (storage concern, not builds). Defer to M4+ (multi-region fleet topology).

### Memory / pebbling (overlayfs limit)

Bathie, Marchal, Robert, Thibault. *IPDPS Workshops*, 2020. doi:10.1109/IPDPSW50202.2020.00102.

The 500-layer overlayfs limit (green-paper §4.6, `Φ(⟨ℓ₀…ℓₙ⟩)` when n > n_max) is exactly the
pebbling game with k=500. When the running count of active unreleased layers approaches n_max,
boost the priority of steps whose completion enables a squash (steps with no remaining dependents
except those behind a squash point). Series-parallel DAGs (most real Earthfiles) admit a
polynomial exact algorithm for squash-point placement. General DAGs are NP-hard; use ILP
rounding as a heuristic.

### Learning-augmented scheduling (theory lens)

Lykouris, Vassilvitskii. "Competitive Caching with Machine Learned Advice." *ICML* 2018.
Bamas, Maggiori, Svensson. "The Primal-Dual Method for Learning Augmented Algorithms." *NeurIPS*
2020.

The consistency/robustness framework is the right theoretical lens for EarthBuild's L0-L3
fallback hierarchy. When prior run data (L0 exact match) exists, use it aggressively;
when only coarse priors (L3 op-type bucket) exist, be conservative. The 90th-percentile L3 prior
(§6) is the "be pessimistic with low-confidence predictions" principle from this theory. Defer
formal bounds until the L0-L3 hierarchy is in production.

### PISA (adversarial validation)

Coleman, Krishnamachari. "PISA: An Adversarial Approach to Comparing Task Graph Scheduling
Algorithms." arXiv:2403.07120, 2024. (Accepted IEEE IPDPS 2025. No reliable proceedings DOI
confirmed.) **Verified 2026-08-12.**

Use the open-source SAGA library to search for Earthfile-shaped DAG instances that maximise the
gap between candidate schedulers. Standard benchmarks mask worst-case divergences; our workloads
span 3-step targets to 1000-step monorepos with a progressive-discovery component that no
standard benchmark includes.

Verified figures from the paper: for **all 15** algorithms evaluated, PISA finds an instance
where the algorithm performs at least **twice** as badly as another; for **10 of 15**, at least
**five times** worse. Run before committing to a default policy.

---

## 5. Stability

**Requirement** (green-paper §4.7.3): given the same graph, the same worker inventory, and the
same cost estimates, an implementation MUST produce the same schedule.

### Why BuildKit fails

`signal()` (`scheduler.go:221`) appends to the FIFO in goroutine-completion order. Five confirmed
non-determinism sites:

1. `muQ.Lock()` contention in `signal()` (`scheduler.go:222-234`) - concurrent dep completions
   race for queue position
2. Go map iteration in `recalcCurrentState` (`edge.go:550`) - which dep key is the representative
   varies
3. `loadCache` iterates `map[string]*CacheRecord` (`edge.go:890`) - cache-record tie-breaking
   inherits map order
4. Goroutine arrival order for `index.LoadOrStore` (`scheduler.go:184-196`) - merge destination
   depends on which goroutine registers first
5. `s.incoming[e]`, `s.outgoing[e]` appended in goroutine-arrival order (`scheduler.go:290, 311`)

### How to achieve stability

**Priority queue, not FIFO.** The ready queue is a max-heap keyed on
`(upward_rank DESC, node_digest ASC)`. `upward_rank` is a pure function of the graph and cost
estimates. `node_digest` is `BLAKE3(op, resolved_input_IDs, platform)` - a stable tiebreaker
independent of timing.

**Sort newly-ready batches.** When multiple steps become eligible simultaneously, sort the batch
by node digest before inserting into the heap. One sort per batch (typically small).

**Content-addressed merge direction.** When two edges with identical keys are merged, the survivor
is the one with the lexicographically smaller step digest, not the one that arrived first.

**Deterministic record selection.** Break ties in cache-record selection by blob digest, not
by map iteration order.

**HRW as the stable tiebreaker.** Within the platform-eligible set,
`score(step, worker) = BLAKE3(step_id || worker_id)`. Same step, same worker set → same
assignment. Membership changes reassign minimally (~1/N).

**Stability is economically load-bearing.** If step s is consistently assigned to worker W, W
accumulates s's input layers across runs and the next run's transfer cost approaches zero.
Arbitrary re-assignment discards that prior (plan: "the cheapest transfer is the one
that does not happen"). Stability is the economic precondition for data locality to pay off -
not merely a tidiness property (green-paper §4.7.3 says this explicitly).

---

## 6. Cost estimation

### Source: build records

Green-paper Appendix B.2: each record holds step identity, κ₁, κ₂ where
computed, result digest, exit code, **timings**, observation set digest, squash flag, and outcome
(L1 hit / L2 hit / miss / refused). Timings are the cost oracle.

**Extension needed:** the schema holds the result digest; output blob *size* requires a secondary
CAS metadata lookup. Extend B.2 to include:

1. Output blob size in bytes
2. Input blob sizes per layer ID
3. Worker queue depth at execution time
4. Available bandwidth at execution time

Without (1) and (2), `comm_cost(t, s)` in `rank_u` requires a CAS query per step per worker at
dispatch time. Without (3) and (4), stored durations cannot be normalised to "duration at
standard load". Green-paper §2.3 states records are derived state and do not affect
correctness, so extending the schema is backwards-compatible.

### Fallback hierarchy (L0 → cold)

Mirrors the mask hierarchy (plan §2a-bis:

| Level | Key                        | Duration source                            |
| ----- | -------------------------- | ------------------------------------------ |
| L0    | Exact κ₁ match             | EMA of prior durations for this exact step |
| L1    | Command class + base layer | Mean duration across L1-matching records   |
| L2    | Base layer alone           | Mean duration across L2-matching records   |
| L3    | Op-type bucket             | Mean duration across op-type bucket        |
| Cold  | None                       | 200 ms machinery floor (E11)               |

Use an exponential moving average at L0, not a plain mean - recent runs predict better.

### Cold start

For a step with no history at any level, use the 200 ms cold machinery floor as the absolute
minimum. As a *scheduler prior*, prefer the 90th-percentile duration from the same op-type
bucket (L3). This schedules unknown steps *early* (conservatively assume slow), preventing the
feedback loop where an underestimated step is scheduled last, runs on an overloaded worker,
records a slow time, and continues to be ranked low.

Do not default to 1 ms (Ninja 1.14's choice for unknown edges): at 1 ms, unknown steps are
always scheduled last, which is the worst possible default for a build where most steps in a
new project are unknown.

### Anti-feedback-loop normalisation

Before storing a duration, normalise:

```text
adjusted_duration = raw_duration / (1 + queue_depth_at_execution)
```

This separates "the step is inherently slow" from "the step ran on an overloaded worker".

Never record duration from a run where the step was re-queued due to worker failure (green-paper
Appendix C.4 gap): that sample reflects infrastructure noise, not step cost.

### Separation: placement vs. dispatch

L1-L3 masks are computable from the Earthfile alone before input digests resolve (plan §2a-bis: "L1-L3 are computable from the Earthfile alone, before inputs are resolved"). This
means:

- **Placement** (which worker) can be decided when the IR node is created. Begin pre-positioning
  data immediately.

- **Dispatch** (when to run) waits until deps are satisfied.

Pre-positioning data during the scheduling latency turns it into overlap with other computation
(plan §3.0a). This is delay scheduling in reverse (Zaharia et al., EuroSys 2010,
doi:10.1145/1755913.1755940): move the data to the best worker during the latency window rather
than waiting for a local worker to become free.

### Cache hit route as cost signal

Extend B.2 to record time spent in the cache lookup itself (L1 key probe vs. L2 consistency
check vs. structural e-graph walk). An L2 consistency check is proportional to the prediction
size (green-paper §4.5). Include lookup cost in `rank_u` so the scheduler does not assume cache
hits are free for steps with expensive-to-verify prior records.

---

## What to build first

**Scheduler levels are not the milestone ladder.** The plan's M1-M10 say what an *Earthfile* can
build; Sched-1..3 say how clever the *scheduler* is. They are independent, and conflating them
would put HEFT at a milestone with no graph to schedule. The mapping:

| Level   | Needed by   | Because                                                                                                                 |
| ------- | ----------- | ----------------------------------------------------------------------------------------------------------------------- |
| Sched-1 | plan **M4** | M4 is the first milestone with a graph - `FROM +other`, `BUILD`. M1-M3 are single-target and need only dependency order |
| Sched-2 | **Phase 3** | HEFT needs recorded durations *and* more than one worker to place work on; both arrive with the fleet                   |
| Sched-3 | after E7    | every item is an optimisation over a scheduler that already works and has been measured                                 |

Sched-1 is a day's work and can ship long before M4 - it simply has nothing to do until then.

### Sched-1: correct and deterministic (no cost estimates needed)

The simplest scheduler that fully satisfies green-paper §4.7:

1. **WAIT/END as a first-class IR barrier node.** Not a side-channel in the converter. The
   scheduler sees it as a dependency edge. Preserve the "at least one can always progress"
   invariant from `wait_block.go:316`.
2. **Topological ready queue sorted by `node_digest ASC`.** Steps are eligible when all dep edges
   are satisfied. Deterministic by construction; no cost estimates required.
3. **Platform affinity filter.** Before assigning a step, check `step.platform ⊆ worker.platform`.
   Ineligible workers are skipped. `LOCALLY` steps are pinned to the invoking machine.
4. **Least-loaded placement.** Within the platform-eligible set, assign to the worker with the
   fewest in-flight steps.
5. **Deduplication.** Steps with the same node ID share a single execution; subsequent requesters
   join on a channel (not a 100 ms polling ticker).

This is correct (satisfies §4.7 I1-I5), deterministic, and can be built in a day. It is not
optimal but it cannot produce wrong results.

### Sched-2: HEFT and data locality (requires build records)

After Sched-1 ships and records are being emitted:

1. Replace `node_digest` sort with `(upward_rank DESC, node_digest ASC)` heap.
2. Seed `rank_u` from L0-L3 duration lookups; 90th-percentile L3 prior for unknowns (200 ms
   floor as absolute minimum, not as scheduler prior).
3. Replace least-loaded with `locality_score(step, worker)` placement.
4. Extend B.2 to record output blob size for communication cost in `rank_u`.
5. Add HRW tiebreaker within the platform-eligible set.

### Sched-3 and later

| Item                             | Depends on                                        |
| -------------------------------- | ------------------------------------------------- |
| PEFT OCT look-ahead              | > 50% of steps with recorded durations (Sched-2+) |
| Pebbling-aware squash scheduling | Overlayfs limit reached in practice (Sched-2+)    |
| E-graph congruence (§2a-bis)     | Sched-2+ node identity and CAS infrastructure     |
| CRUSH hierarchical placement     | Multi-region fleet topology (Phase 3)             |
| PISA adversarial validation      | Sched-2 scheduler exists to compare against       |
| Incremental rank recompute       | Sched-2+ priority queue infrastructure            |
| GNN/RL rank replacement (Decima) | Substantial build history across users (M4+)      |
