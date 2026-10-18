# Caching a Rust build

Rust builds cache badly under container build engines, and the reason is not the engine's
layering: it is that cargo decides what to recompile by comparing **mtimes**, not content. Each
source is compared against the fingerprint cargo wrote in `target/`, and anything **strictly
newer** is rebuilt.

Two consequences follow, and both were measured rather than assumed:

- A source whose content changed but whose mtime is older is reported `Fresh`, and the build
  produces a **stale binary** with no error anywhere.
- A source whose content is identical but whose mtime is newer is recompiled, every time.

So a build context that arrives with every file at one fixed instant is not merely uncached - it
is a context cargo cannot reason about at all.

## What the engine does about it

Layers preserve mtimes to the nanosecond (invariant I8), so anything a step *produces* keeps its
times across a layer boundary. The build **context** used to be the exception, packed at a fixed
epoch so that two clones of one commit produced identical bytes.

It now carries commit times instead: a committed file gets the time of the commit that last
changed it, and a locally-modified one its mtime on disk. A commit time is a property of the
history rather than of the clone, so two machines still agree, and it only moves forward, so it
carries the ordering content alone cannot. A modified working tree is not reproducible by
definition, so the local clock there costs nothing.

See [`EARTH_CONTEXT_TIMES`](settings.md) for the full reasoning and how to turn it off.

## The pattern

Dependencies go in their own layer, keyed on `Cargo.toml` and `Cargo.lock`. No cache mount is
involved, so the result is content-addressed, portable between machines, and shareable through a
registry - which a cache mount, pinned to the machine that wrote it, is not.

```earthfile
VERSION 0.8

deps:
    FROM rust:slim-bookworm
    WORKDIR /app
    COPY Cargo.toml Cargo.lock .
    # A stub, so cargo builds the dependency graph and nothing of ours.
    RUN mkdir -p src && echo 'fn main(){}' > src/main.rs
    RUN cargo build --release
    # The stub *is* this crate, so cargo left a fingerprint claiming it is
    # built. Drop this crate's own traces and keep every dependency's - without
    # this the real sources arrive looking older than the stub's fingerprint and
    # the stub binary is what ships.
    RUN rm -rf src \
        target/release/.fingerprint/mycrate-* \
        target/release/deps/mycrate*

build:
    FROM +deps
    COPY --dir src .
    RUN cargo build --release
    SAVE ARTIFACT target/release/mycrate mycrate
```

## What it does

Measured on a two-crate project, `termcolor` standing in for the dependency graph:

| Change                       | Engine         | cargo                                | Binary  |
| ---------------------------- | -------------- | ------------------------------------ | ------- |
| cold                         | 2 hit, 7 miss  | `Compiling termcolor` then the crate | correct |
| nothing                      | 11 hit, 0 miss | not reached                          | correct |
| `touch`, content identical   | 11 hit, 0 miss | not reached                          | correct |
| a source edited, uncommitted | 8 hit, 3 miss  | `Fresh termcolor`, crate recompiled  | correct |
| that edit committed          | 11 hit, 0 miss | not reached                          | correct |

The row that matters is the fourth: the dependency stays `Fresh` while the edited crate rebuilds.
Under the fixed epoch the same edit produced `Fresh` for **both** and shipped the previous binary.

Committing an edit already built is a full hit rather than a rebuild. The stamp does change - from
the local clock to the commit's - so the exact-key tier misses, and the observed-inputs tier
answers it instead, because that digest excludes mtimes by construction.

## Why not a cache mount

`--mount=type=cache` is faster still on the machine that has it, and that is the whole of the
objection: it lives outside the layer graph, so it is not addressed by content, does not travel to
another machine, is not shared through a registry, and cannot be reasoned about by anything that
compares two builds. A layer is all four. Use the mount as an optimisation on a developer's own
machine if you like, but a build that *needs* it is a build that only works where it has already
run.
