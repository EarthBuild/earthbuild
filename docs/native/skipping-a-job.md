# Skipping a CI job that has nothing to do

A CI job that rebuilds an unchanged target spends a runner to learn that. `--emit-inputs` writes down
what a build's plan depends on; `--check-inputs` asks a later checkout whether any of it moved.

```console
$ earth-native --emit-inputs inputs.json +test
$ earth-native --check-inputs inputs.json +test
unchanged: +test needs no build
```

Exit codes are the interface: **0** unchanged, **2** changed, **1** something went wrong. The three
are distinct on purpose - a job that cannot tell "changed" from "the Earthfile does not parse" skips
on a broken build, which is the one outcome a skip must never be.

```yaml
- uses: actions/cache@v4
  with:
    path: inputs.json
    key: inputs-${{ github.job }}-${{ github.run_id }}
    restore-keys: inputs-${{ github.job }}-
- id: check
  run: earth-native --check-inputs inputs.json +test && echo "skip=true" >> "$GITHUB_OUTPUT"
  continue-on-error: true
- if: steps.check.outputs.skip != 'true'
  run: earth-native --ci +test && earth-native --emit-inputs inputs.json +test
```

## What the fingerprint covers

The `fingerprint` field is the whole of the comparison. It is derived from the graph's node
identities, which are recursive over their inputs, so it covers:

| Covered                                | How                                             |
| -------------------------------------- | ----------------------------------------------- |
| the Earthfile's text                   | every command is an operation in the graph      |
| build arguments and environment values | hashed into the step that reads them            |
| the platform                           | hashed into every node                          |
| the resolved digest of each base image | [pinned at plan time](pinning.md)               |
| what every `COPY` reads from the host  | the context's content digest, mtimes excluded   |
| targets reached only by `BUILD +other` | folded in beside the root                       |

That last row and the first are why this is not a path filter. `dorny/paths-filter` and its kin key
on globs a human maintains: they go green on an edited command, on a moved tag, and on a dependency
reached through `BUILD`. The fingerprint does not.

The context digest excludes mtimes, so a fresh clone of one commit fingerprints the same as the
working tree it was cloned from - which is the case a CI runner is always in.

## What it does not cover, and what happens then

The plan describes the build; it does not describe the world. What a `RUN` downloads, what a
`LOCALLY` step reads off the machine, and the value behind a secret are all outside it.

Where the engine *knows* it cannot key something, it says so in `caveats`, and **a build carrying any
caveat is never certified unchanged** - `--check-inputs` exits 2 with the reason:

```console
$ earth-native --check-inputs inputs.json +test
the build's inputs have changed: this build cannot be certified unchanged
  Earthfile:7 is --no-cache, so it runs whatever the inputs say
```

The caveats are: a `--no-cache` step, a `LOCALLY` step, an image reference left unpinned (the
registry was unreachable, or nobody ran `--pin`), and a secret read where no fleet key is configured
so its value is outside the fingerprint.

What remains uncovered without a caveat is a `RUN` that reaches the network. The engine cannot see
that, and neither can any other cache; it is the same assumption `CACHE` and every layer cache
already make.

## Naming what changed

A changed build says which input moved:

```console
$ earth-native --check-inputs inputs.json +test
the build's inputs have changed:
  context crates changed
  rust:slim-bookworm moved from rust@sha256:aede… to rust@sha256:1b4c…
```

The granularity is the `COPY` source, not the file inside it: `COPY --dir crates .` reads one digest
over the whole tree, so an edit anywhere under `crates` reads as `context crates changed`.

## Cost

Checking costs a plan: parsing, resolving each reference, and digesting the build context. On a large
tree the context digest dominates - seconds - which is the price against a whole runner.
