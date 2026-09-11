# Skipping a job

A test job whose inputs have not changed should do no work. That is what `--auto-skip` was for, and
it is the Docker insight moved up one level: Docker succeeded because an unchanged layer is not
rebuilt, and the CI version of that is an unchanged *job* that is not run.

This note is about the key such a decision is made on. It is a design note, not a specification:
what it settles moves into green paper §4.4 when it is built.

---

## Why the tiers we already have do not answer it

The engine has two cache tiers and both are read-precise where it matters. Neither survives a fresh
CI runner, and the reason is not subtle:

| Tier                | Precision                    | Needs carried between jobs | Size      |
| ------------------- | ---------------------------- | -------------------------- | --------- |
| L1, Κ₁ chain key    | declared inputs              | the layer store            | gigabytes |
| L2, Κ₂ observed key | **only what each step read** | the layer store            | gigabytes |
| a job key           | to be decided below          | one digest                 | 32 bytes  |

On a machine with a warm store, L2 is the right mechanism and there is nothing to add: a step whose
predicted reads still hold the same digests is served from cache, and a file nobody opened cannot
make it stale (`engine/core/staleask.go`, green paper §3.6 and equation (4.6), I3). On an ephemeral runner the store is
not there, restoring it costs more than rebuilding, and L2 is unreachable.

So the job key is not a coarse substitute for L2. On the machine most builds actually run on it is
the only tier available, which is why its precision is the whole question.

---

## Three candidate keys

**A - the plan fingerprint.** Every declared input: the graph's node identities, which are recursive
over their inputs, so it covers each command, every build argument and environment value, the
platform, the resolved digest of each base image, the content digest of every path a `COPY` reads,
and what the build is asked to produce. Computable before anything runs, from a checkout alone.
Implemented (`engine/cli/inputs.go`).

**B - A with the context content removed.** The graph's shape and commands without what the copied
files contain. Not a key on its own: it cannot see a source edit.

**C - B together with the digests of the files the build actually read.** A `README` that changed and
that nothing opened does not move it; a source file that changed does. This is the key worth having,
and the rest of this note is about it.

A remains as C's fallback: C needs a previous run's observations, so a first build, a build on a
platform with no tracer, and a build the tracer could not follow completely all fall back to A.

---

## What C is

Let 𝐺 be the plan graph and 𝑅 the *host inputs* the last successful build depended on.

```text
(C.1)    σ         ≡  ℋ(𝒯(Earthfile) ‖ target ‖ platform ‖ 𝒮(args) ‖ 𝒮(secret digests) ‖ flags)
(C.2)    𝑅         ≡  { (host path, digest) } ∪ { (host directory, listing digest) }
                        ∪ { host path : absent }
(C.3)    Κ_job     ≡  ℋ(σ ‖ 𝒮(𝑅))
```

𝒮 is the injective encoding of green paper §1.4, over sorted keys. 𝒯 is the Earthfile's parse tree
with its source locations and doc comments removed - the meaning rather than the bytes, so a comment
or a reformat is not a rebuild.

**σ needs no plan and no graph.** An Earthfile, what was asked of it, and the values handed in
determine every step there will be, so the shape can be had from those directly - in milliseconds,
without interpreting anything, without running an `IF` to decide a branch, and without digesting a
byte of the build context. That is what makes a pre-flight check cheap enough to be worth making.

Deriving σ from the *graph* instead would be exact to the target rather than to the file, and was
tried: it needs either a second plan, which re-runs any `IF` the interpreter had to execute, or a
second identity tree over every node. Neither is worth what it buys. See "Cool things" below.

A moved tag, an edited `RUN`, a changed `ARG`, a renamed artifact and a different secret all move σ.
Only the *content of copied files* is deferred to 𝑅.

---

## Deriving 𝑅, which is the hard part

An observation records paths **inside the step's filesystem** - `/w/crates/greet/src/lib.rs` - and
Κ_job must be re-derivable from a host checkout with nothing built. The mapping between the two is
the copy that placed the file.

For each `OpLocal`-sourced `COPY` the plan knows `(context source -> destination prefix)`. An
observed path under a destination prefix rewrites to the host path beneath the corresponding source.

**𝑅 stores host-side digests, captured at record time, never the digest the step saw.** A copy may
legitimately change what the destination holds relative to the host file - `--chmod` changes the
mode, `--keep-own` and `--chown` the ownership - and re-deriving from the host must not have to
reproduce any of that. The transformations themselves are arguments of the `COPY` node and so are
already in `shape(𝐺)`. Content is never transformed, which is what makes the pairing sound.

Three kinds of entry, matching the three fields of an observation:

| Observation | Host entry                            | Re-derived by                                    |
| ----------- | ------------------------------------- | ------------------------------------------------ |
| `Reads`     | host path, content digest             | digesting the file                               |
| `Listings`  | host directory, digest of its entries | listing it under the same exclusions as the copy |
| `Negative`  | host path, asserted absent            | `lstat`                                          |

`Listings` is what makes a *new* file safe: a glob that would now match `src/new.rs` changes the
digest of the listing the step enumerated, so Κ_job moves even though no recorded path did. `Negative`
is what makes a file appearing where one was absent safe. Both already exist because L2 needs them
for the same reason (green paper §3.6, I3).

---

## Hard gates

A job key is served with nothing to verify it afterwards, so every uncertainty must refuse rather
than degrade. Where L2 can afford a hint, this cannot.

| Gate | Refuse to compute Κ_job when                                                                                     | Because                                                                      |
| ---- | ---------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| H1   | any step reported `Incomplete`                                                                                   | the tracer knows it missed something; L2 pays a miss, this pays a wrong skip |
| H2   | any step of the target recorded no observation at all                                                            | an unobserved step is one whose inputs are unknown, not one with none        |
| H3   | the plan carries any caveat of §A - `--no-cache`, `LOCALLY`, an unpinned reference                               | each is a declared reason the key under-claims                               |
| H4   | an observed read maps to neither a `COPY` from the context, nor a base image layer, nor an earlier step's output | a read nobody can explain is a read nobody can re-derive                     |
| H5   | the platform has no observation source                                                                           | see below                                                                    |

Each gate falls back to key A, which is conservative and correct. **A gate that fires is a rebuild,
never a skip.**

---

## Secrets, and what σ can say about them

With a fleet key configured, σ carries the *keyed digest* of every secret the build holds, so a
rotated credential is a different build. That is the strong form and it is what `EARTH_SECRET_HMAC`
buys.

Without one there is nothing to fold a value into, and the choice is between covering the secrets'
**names** and covering nothing. σ covers the names. It is a weaker claim, and the cost is exact: a
rotated credential does not move the shape, so a job whose result depends on *which* credential it
had could be skipped. Usually a secret fetches something rather than changing what is built; where
that is not true, configure the key.

Refusing instead was considered and rejected: it leaves `--auto-skip` doing nothing at all for
anyone who has not configured an HMAC, which is most people, and a mechanism nobody can switch on
protects nobody.

**Which of the two was used is folded into σ**, so a name-keyed shape and a digest-keyed one for the
same build are different values. A record written before a key was configured is simply not found
afterwards, rather than being found and trusted for more than it says.

## No tracer

`engine/trace` is seccomp user notification and is Linux only; `trace_other.go` is deliberately empty.
On darwin there is no observation source for `RUN`, so H5 fires and the build falls back to A.

Said once per build, not silently: a Mac developer measuring hit rates on their laptop and
concluding the mechanism does not work is the failure mode here, and it costs one line to prevent.

```text
note: no read tracing on darwin, so --auto-skip is using declared inputs
  a file copied into the build and never read will still cause a rebuild
```

---

## Adversarial tests, written before the mechanism

A false skip is a green tick on a build that was never run, which is the one outcome this must not
produce. Each of these is a red test first.

| #   | The build did this                                                | Κ_job must     |
| --- | ----------------------------------------------------------------- | -------------- |
| 1   | a file in the copied tree changed, nothing read it                | not move       |
| 2   | a file in the copied tree changed, a step read it                 | move           |
| 3   | a new file appeared in a directory a step enumerated              | move           |
| 4   | a file a step looked for and did not find now exists              | move           |
| 5   | a file a step read was deleted                                    | move           |
| 6   | a read file's contents are unchanged and its mode is not          | move           |
| 7   | a symlink a step followed now points elsewhere                    | move           |
| 8   | a `RUN` command was edited                                        | move           |
| 9   | a base image tag moved to a new digest                            | move           |
| 10  | a `SAVE ARTIFACT ... AS LOCAL` destination changed                | move           |
| 11  | the tracer reported `Incomplete`                                  | not be offered |
| 12  | a step of the target recorded no observation                      | not be offered |
| 13  | two targets in one Earthfile, only the other one's inputs changed | not move       |

3, 4, 5 and 7 are the ones that would make this unsafe if `Listings` or `Negative` turned out not to
cover what they claim to. They are the reason the suite comes first.

---

## Storage, and carrying it through CI

One record per `(target, platform)`: `shape(𝐺)`, the entries of 𝑅, and the Κ_job they imply. A file,
not a database - a CI cache carries it, a reviewer can read it, and there is nothing to merge.

Two shapes work on GitHub Actions and they are not the same thing:

* **the record as a cached file**, restored with a branch-scoped key and a fallback to the default
  branch. Needed because 𝑅 itself must be carried - it is what makes the key derivable at all.
* **Κ_job as a cache key**, with `lookup-only`. Keys are immutable, scoped to the current branch plus
  the default branch and the base branch of a pull request, evicted LRU at 10 GB and after seven days
  unused. A content-addressed key fits that exactly: nothing to reconcile, and a miss is a build.

The second is how the skip decision is made; the first is how the input to that decision survives.

---

## Cool things this does not do

**Per-target granularity within one Earthfile.** σ is over the whole file, so editing any target
moves the key for every target in it. A monorepo Earthfile with thirty targets and thirty jobs
re-runs all thirty on a one-line edit.

Recovering it means hashing only the statements reachable from the target asked for, which means
following `FROM`, `BUILD` and `COPY +x/y` and expanding enough `ARG` to resolve the names - a second
evaluator, which is the thing `inputgraph` is and the thing this deliberately is not. Or it means
deriving σ from the plan, with the costs above.

Not worth it yet, on the evidence: Earthfiles change rarely and the files they copy change constantly,
so the case this would improve is the rare one. The record format does not care - σ is opaque to
everything else - so it can be swapped later without a migration.

**A build spread over several Earthfiles.** `IMPORT` and `./sub+target` are refused rather than
hashed, for the same reason: finding which files participate is the reachability walk above. Hashing
every Earthfile under the context would work and is coarser still.

## What this deliberately does not cover

A `RUN` that reaches the network. The tracer sees the socket, not what came back, and no key over the
checkout can describe it. This is the same assumption `CACHE` and every layer cache already make, and
it is stated rather than mitigated.

---

## Open

* **Artifact edges.** `COPY +other/thing` is not a host path: its content is a function of the other
  target's own 𝑅, so the records compose. Whether that composition is worth building at once or
  whether such a target simply falls back to A on the first cut is undecided.
* **Where the record lives by default.** Beside the engine's own store, as the prediction history
  does, or beside `--auto-skip-db-path`. See `engine/cli/autoskip.go`.
* **Whether σ should cover the invocation's flags exhaustively.** It covers the ones that change what
  a build does - `--push`, `--strict`, `--no-output`, `--allow-privileged`, the version flags - and
  not the ones that change how it reports. A flag added to the first group and not to σ is a false
  skip, so the list wants a guard of the kind `TestEveryFlagIsClassified` already is.
