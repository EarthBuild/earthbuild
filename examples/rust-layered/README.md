# A Rust workspace cached without a cache mount

No `CACHE` command and no `--mount=type=cache` anywhere in this example.
Everything it caches is an ordinary content-addressed layer, which means it is
shared through a registry, reproducible on a machine that has never run the
build, and comparable between two builds. A cache mount is none of those: it
lives outside the layer graph, so a build that needs one only works where it has
already run.

## Running it

```sh
earth ./examples/rust-layered+build          # the binary, as an artifact
earth ./examples/rust-layered+deps           # just the dependency layer
```

## How it works

Two layers, and the split is the whole idea.

`+deps` copies **the manifests and no source**, stubs out each crate, and runs
`cargo build --release --workspace`. It therefore compiles everything from
crates.io and nothing of yours, and because no source file is in its inputs it
survives every edit you make.

`+build` starts from that layer, copies the real sources, and builds. cargo
finds every dependency already compiled and recompiles only your crates.

The one subtlety is the `rm` at the end of `+deps`. The stubs *are* your crates,
so cargo leaves fingerprints claiming they are built; the real sources then
arrive looking no newer, cargo reports them `Fresh`, and the **stub binary is
what ships** - a wrong build with no error anywhere. Dropping your crates'
fingerprints and keeping every dependency's is what prevents it.

## Why this works here and not under Docker

cargo does not hash sources. It compares each one's mtime against the
fingerprint in `target/` and recompiles what is **strictly newer**. So a build
engine that flattens timestamps hands cargo a tree it cannot reason about:
either nothing is ever fresh, or an edit is silently ignored.

This engine keeps mtimes to the nanosecond through a layer, and gives context
files the timestamp of the commit that last changed them - stable across
machines, and only ever moving forward. See
[`EARTH_CONTEXT_TIMES`](../../docs/native/settings.md) and
[docs/native/rust.md](../../docs/native/rust.md).

## Compared with cargo-chef

cargo-chef exists to do this under Docker, where `COPY` invalidates on any file
change and a workspace's manifests cannot be copied alone. `cargo chef prepare`
writes a `recipe.json` skeleton and `cargo chef cook` builds the dependencies
from it.

Against that, this example:

- needs **no extra tool in the image**, and no `recipe.json` indirection - chef
  has to be installed or baked into a builder image first;
- is **explicit**: the manifests it copies are named in the Earthfile, so what
  keys the dependency layer is readable rather than derived.

Where chef is still the more convenient of the two is a large workspace: it
generates the stub skeleton that this example writes out by hand, and that
boilerplate grows with every crate you add.

**Neither approach caches your own crates.** Both warm the dependency graph and
nothing else, so editing any crate in the workspace recompiles every crate you
own - measured here: editing `mathy`, which nothing depends on, still recompiles
`greet` and `app`. Getting past that needs a previously-built `target/` in the
base image, which is what `+build-warm` is for:

```sh
earth --build-arg warm_image=ghcr.io/you/app-build-cache:main \
    ./examples/rust-layered+build-warm
```

Given such an image, cargo recompiles only the crates whose sources actually
changed, exactly as a local incremental build would - which is strictly more
than a dependency-only cache can do. Producing the image is `+cache`.

Publishing needs both halves to agree - `SAVE IMAGE --push` in the Earthfile and
`earth --push` on the invocation:

```sh
earth --push --build-arg cache_image=ghcr.io/you/app-build-cache:main \
    ./examples/rust-layered+cache
```

Then a later build starts from it:

```sh
earth --build-arg warm_image=ghcr.io/you/app-build-cache:main \
    ./examples/rust-layered+build-warm
```

Measured on this workspace, publishing the tree and then editing `mathy`, which
nothing depends on:

| crate       | `+build` (deps layer only) | `+build-warm` (whole tree) |
| ----------- | -------------------------- | -------------------------- |
| `mathy`     | Compiling                  | Compiling                  |
| `greet`     | Compiling                  | **Fresh**                  |
| `app`       | Compiling                  | **Fresh**                  |
| `termcolor` | Fresh                      | Fresh                      |

**That column is the whole argument.** A dependency-only cache - chef's, and
`+build`'s - has to recompile every crate you own whenever any of them changes,
because none of them were ever in the layer. A published build tree carries
`target/` too, so cargo recompiles what changed and nothing else, exactly as it
would locally.

It works because a layer keeps the mtimes the store holds and a context file
carries the time of the commit that last changed it. Both are properties of the
history rather than of the machine, so the comparison cargo makes means the same
thing on the machine that published the tree and the machine that pulled it.

## The crates

Three, arranged so incrementality is observable: `mathy` has no dependents,
`greet` has one, and `app` is the binary. Editing `mathy` should ideally leave
`greet` and `app` untouched.
