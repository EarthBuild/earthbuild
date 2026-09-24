# Skipping a CI job that has nothing to do

A CI job that rebuilds an unchanged target spends a runner to learn that. `--emit-inputs` writes down
what a build's plan depends on; `--check-inputs` asks a later checkout whether any of it moved.

```console
$ earth --engine=native emit-inputs inputs.json +test
$ earth --engine=native check-inputs inputs.json +test
unchanged: +test needs no build
```

Commands rather than flags on `build`, because neither builds. They carry the whole build flag set -
`--build-arg`, `--platform`, `--secret` - because those decide the plan and therefore the fingerprint:
a check run with different arguments from the emit before it is a different question, and answering it
as though it were the same one is the false green this exists to avoid.

`earth-native` takes the same two words: `earth-native check-inputs inputs.json test`.

Exit codes are the interface: **0** unchanged, **2** changed, **1** something went wrong. The three
are distinct on purpose - a job that cannot tell "changed" from "the Earthfile does not parse" skips
on a broken build, which is the one outcome a skip must never be.

```yaml
- uses: actions/cache@v4
  with:
    path: inputs.json
    key: inputs-${{ github.job }}-${{ github.ref_name }}-${{ github.sha }}
    restore-keys: |
      inputs-${{ github.job }}-${{ github.ref_name }}-
      inputs-${{ github.job }}-${{ github.event.repository.default_branch }}-
- id: check
  run: earth --engine=native check-inputs inputs.json +test && echo "skip=true" >> "$GITHUB_OUTPUT"
  continue-on-error: true
- if: steps.check.outputs.skip != 'true'
  run: |
    earth --engine=native --ci +test
    earth --engine=native emit-inputs inputs.json +test
```

## What the fingerprint covers

The `fingerprint` field is the whole of the comparison. It is derived from the graph's node
identities, which are recursive over their inputs, so it covers:

| Covered                                  | How                                           |
| ---------------------------------------- | --------------------------------------------- |
| what each command *does*                 | every command is an operation in the graph    |
| build arguments and environment values   | expanded into the step that reads them        |
| the platform                             | hashed into every node                        |
| the resolved digest of each base image   | [pinned at plan time](pinning.md)             |
| what every `COPY` reads from the host    | the context's content digest, mtimes excluded |
| targets reached only by `BUILD +other`   | folded in beside the root                     |
| where `SAVE ARTIFACT ... AS LOCAL` lands | folded in beside the graph                    |
| what `SAVE IMAGE` declares, and `--push` | folded in beside the graph                    |

This is why it is not a path filter. `dorny/paths-filter` and its kin key on globs a human maintains:
they go green on an edited command, on a moved tag, and on a dependency reached through `BUILD`. The
fingerprint does not.

**It is the Earthfile's meaning, not its bytes.** A comment, a blank line or a reformat leaves the
fingerprint equal, because none of them changes an operation - which is the right answer, and not the
one a hash of the file would give.

**The last two rows are about what the build is asked to leave behind, and they are not decoration.**
`SAVE ARTIFACT x AS LOCAL out-$FOO.txt` has the same graph for every value of `FOO`: identical layers,
a different file on disk. A fingerprint over the graph alone certifies the second build unchanged,
skips it, and the file it was asked for is never written.

A build argument no step reads does not move the fingerprint, and should not: passing `--build-arg
UNUSED=x` is not a reason to rebuild.

The context digest excludes mtimes, so a fresh clone of one commit fingerprints the same as the
working tree it was cloned from - which is the case a CI runner is always in.

## What it does not cover, and what happens then

The plan describes the build; it does not describe the world. What a `RUN` downloads, what a
`LOCALLY` step reads off the machine, and the value behind a secret are all outside it.

Where the engine *knows* it cannot key something, it says so in `caveats`, and **a build carrying any
caveat is never certified unchanged** - `--check-inputs` exits 2 with the reason:

```console
$ earth --engine=native check-inputs inputs.json +test
the build's inputs have changed: this build cannot be certified unchanged
  Earthfile:7 is --no-cache, so it runs whatever the inputs say
```

The caveats are: a `--no-cache` step, a `LOCALLY` step, an image reference left unpinned (the
registry was unreachable, or nobody ran `--pin`), and a secret read where no fleet key is configured
so its value is outside the fingerprint.

The first two are stronger than caveats under `--auto-skip`: a build containing either records no
skippable answer at all, and says so.

```console
$ earth --engine=native --auto-skip +deploy
auto-skip: +deploy will not be skipped
  Earthfile:12 runs LOCALLY, on this machine and outside the build: skipping it would skip whatever it writes there
```

A host step writes outside the build, so skipping it does not produce a coarser answer - it produces
no answer. The other two caveats only make the key under-claim, which `--auto-skip` is allowed to
trade away; these are not the same thing. `--ci` (and `--strict`, which it implies) refuses `LOCALLY`
at plan time instead, so a pipeline never reaches this.

What remains uncovered without a caveat is a `RUN` that reaches the network. The engine cannot see
that, and neither can any other cache; it is the same assumption `CACHE` and every layer cache
already make.

## Naming what changed

A changed build says which input moved:

```console
$ earth --engine=native check-inputs inputs.json +test
the build's inputs have changed:
  context crates changed
  rust:slim-bookworm moved from rust@sha256:aede… to rust@sha256:1b4c…
```

The granularity is the `COPY` source, not the file inside it: `COPY --dir crates .` reads one digest
over the whole tree, so an edit anywhere under `crates` reads as `context crates changed`.

## Branches

The two `restore-keys` above are this branch's most recent fingerprint, then the default branch's. A
run on a new branch therefore starts from what `main` last recorded, which is usually right and is
never dangerous:

**Every way the file can be wrong costs a rebuild, and none of them costs a skip.** The fingerprint is
one value for one target on one platform, compared for equality. Restoring a stale one, one from
another branch, or none at all all read as "changed". There is no state to merge and so no way for two
branches to produce a file that is wrong rather than merely old.

That asymmetry is the whole reason to prefer a single value over an accumulating set here. A set - a
record of every input combination ever built - gets the branch question the other way round: the
useful thing about it is that entries from elsewhere apply to you, and the cost of restoring the wrong
one is a job that does not run.

GitHub's cache is immutable per key and scoped to the current branch plus the default branch. A file
holding one value works with that: a new key per commit, a prefix fallback, nothing to reconcile. A
database being accumulated into does not, quite - two jobs restoring one snapshot and saving two
successors leave one of them to be dropped by the next run, silently, and the file itself is a binary
`bbolt` database rather than something a reviewer can read in a diff.

If you would rather not use a cache at all: the file is small, deterministic and text, so committing
it works, and the branch semantics become git's own. A merge conflict in it then means exactly what it
looks like - two branches changed the same target's inputs.

## Against `--auto-skip`

`--auto-skip` answers a neighbouring question on the buildkit path. It is **deprecated**: its cloud
backend has been removed and only the local database still works, and whether it goes entirely is
being decided at <https://github.com/orgs/EarthBuild/discussions/707>. The native engine never had
it - `--auto-skip`, `--no-auto-skip` and `--auto-skip-db-path` are all in the ignored-flag list and
say so when passed.

The two are not interchangeable:

| Question           | `--auto-skip`                            | `check-inputs`                            |
| ------------------ | ---------------------------------------- | ----------------------------------------- |
| engine             | buildkit                                 | native                                    |
| where the key goes | a local database (`--auto-skip-db-path`) | a file, so a CI cache can carry it        |
| what it skips      | the target, from inside the invocation   | the job, from outside it                  |
| who computes it    | `inputgraph`, a second implementation    | the engine's own plan, one implementation |
| the base image     | hashed as the tag, so a moved tag skips  | hashed as the pinned digest               |
| what is stored     | every hash ever built, forever           | one value for one target                  |

That last row is the one to weigh. `inputgraph` walks the Earthfile and hashes it without evaluating,
which means two functions have to agree about what a build depends on - and this repository's own key
guard exists because exactly that arrangement, for `Κ₁` and the step class, silently disagreed about
nine fields. `check-inputs` reads the node identities the cache already keys on, so there is nothing
for it to drift from.

One concrete consequence: `inputgraph` does not resolve an image reference at all - `handleFrom`
returns early for anything without a `+` in it - so `FROM rust:slim-bookworm` reaches its hash as the
tag. A tag that moves is not a new key, and the target is skipped. The fingerprint here carries the
digest, because the plan pinned it.

Two rows favour auto-skip, and both are borrowable: it records the key for you when a build succeeds,
and it hashes each file of a `COPY` separately, so it could name the file rather than the tree.

The surviving backend is `--auto-skip-db-path`, whose own source says it is "only meant for
dev/testing": a `bbolt` file mapping each SHA-1 to the time it was built, with the target name
discarded and no eviction. Carrying that through a CI cache is possible and is not what it was
written for.

## Cost

Checking costs a plan: parsing, resolving each reference, and digesting the build context. On a large
tree the context digest dominates - seconds - which is the price against a whole runner.
