# Sharing caches between machines

A `CACHE` mount belongs to the machine that filled it. `--portable-except` says it may be shared with others:

```Dockerfile
CACHE --id go-mod --portable-except 'lock,**/*.lock,**/*.partial' /go/pkg/mod
```

EarthBuild cannot work out for itself whether a cache tolerates this. Whether two copies of `~/.m2/repository` can be combined is a fact about Maven, so you assert it and EarthBuild acts on it. A wrong assertion corrupts builds silently - see the warning in the [`CACHE` reference](../earthfile/earthfile.md#cache).

## Pick the mountpoint first

Most tool caches are two things in one directory: a content-addressed store, and an index or lockfile that is rewritten. Mount the store rather than the home, and the exclusion list often becomes empty:

| Instead of mounting | Mount                            | Because                                      |
| ------------------- | -------------------------------- | -------------------------------------------- |
| `~/.cargo`          | `~/.cargo/registry/cache`        | `registry/index` is a rewritten git checkout |
| `~/.cache/pip`      | `~/.cache/pip/wheels`            | `http-v2` is an HTTP freshness cache         |
| `~/.gradle/caches`  | `~/.gradle/caches/build-cache-1` | `modules-2/metadata-*` holds absolute paths  |

## Recommended settings

Measured against each tool's own cache format. Where a cache is not listed, see [Working it out for yourself](#working-it-out-for-yourself).

### Go

| Mountpoint                   | Setting                                                                                                                |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `/go/pkg/mod`                | `--portable-except 'cache/lock,cache/download/**/*.lock,cache/download/**/*.partial,cache/download/sumdb/*/lookup/**'` |
| `/go/pkg/mod/cache/download` | the same list, and 3.8x smaller - see below                                                                            |
| `/root/.cache/go-build`      | `--portable-except 'trim.txt'`, in a container - see below                                                             |

The module cache is the best case there is, and it is the one setting here that has been
measured rather than reasoned about. Two caches populated independently at deliberately
different root paths held **95,283 paths each, of which 95,282 were byte-identical**, with
no difference in mode and no path present in only one. A third, populated twenty minutes
later, agreed on all 3,871 paths it shared.

Across architectures it is exact: the same module set on `darwin/arm64` and `linux/amd64`
shared **94,162 paths and disagreed on none of them**, in content or in mode.

The single exception was a checksum-database lookup, and it is the only mutable region:
`cache/download/sumdb/<name>/lookup/<module>@<version>` records the **signed tree head at
the time of the lookup**, so two machines asking on either side of a database append get
different bytes for the same path. The tiles beside it are safe - a partial tile carries its
width in its own name (`8/1/967.p/104`), so it is as immutable as a full one.

Two traps in the obvious exclusion list, both of which the measurement caught:

* `**/*.lock` **over-reaches.** Of 538 `.lock` paths, 11 are third-party *source* -
  `Cargo.lock`, `Gemfile.lock`, `Pipfile.lock`, `buf.lock` - inside extracted module trees,
  and permanently immutable. Anchor the pattern at `cache/download/`.
* A bare `lock` matches `gvisor.dev/gvisor@.../pkg/sentry/fsimpl/lock`, which is a
  **directory**. Write `cache/lock`, which is the one Go actually takes.

Most builds never write a lookup file at all: Go consults the checksum database only for a
module absent from `go.sum`, so a project with a complete `go.sum` produces none. The 337
above came from `go mod download all` walking the whole module graph.

**Mounting `cache/download` alone moves 3.8x less.** The zips are 299 MB where the extracted
trees they produce are 1.1 GiB, and Go re-extracts on demand. Prefer it where bandwidth costs
more than CPU, and the whole mount where it does not - extraction is per-step and not cheap.

**The build cache was listed here as unshareable, and that was wrong.** The argument was that an entry is keyed on an ActionID including absolute paths, so two machines never compute the same key - which assumes two machines have different paths. Inside a container they do not: same image, same working directory, same `GOCACHE`.

Measured, building `std` from one pinned image digest on a native `linux/amd64` box and on an Apple-silicon Mac running the same image under `--platform linux/amd64`: **1,052 of 1,052 compiled objects byte-identical, under identical ActionIDs**, with nothing present on one side only. Emulated x86-64 and native x86-64 produce the same bytes, because Go's code generation is a function of `GOARCH` and `GOAMD64` and never inspects the host.

An index entry is `v1 <ActionID> <OutputID> <size> <nanotime>`, and that last field is the write time - so the entries are *immutable* (Go updates their mtime for trimming, never their bytes) and not *reproducible*. It decides nothing: the id, the output and the size all agree, and `get` performs no freshness check. Exclude `trim.txt`, which is garbage-collection bookkeeping, and share the rest.

Two conditions, both of which a containerised build already meets: the machines must run the **same toolchain version**, because the compiler's own build id is in every ActionID, and they must build at the **same absolute paths**. Outside a container, expect a cache that is never read - harmless, and useless.

### Rust

| Mountpoint                   | Setting                                                   |
| ---------------------------- | --------------------------------------------------------- |
| `$CARGO_HOME/registry/cache` | `--portable-except ''`                                    |
| `$CARGO_HOME/registry/src`   | `--portable-except ''`, with care                         |
| `target/`                    | **never** - absolute paths in `.d` files and fingerprints |

Two warnings on `registry/src`. Cargo performs **no content verification** when reusing an extracted source tree, and unlike `vendor/` there is no `.cargo-checksum.json` there to check against - so a corrupt entry propagates silently into a build. Cargo also does not make the directories read-only, so a `build.rs` can modify one in place.

For compilation results use `sccache` with its own S3 or Redis backend rather than sharing a directory.

### Node

| Mountpoint                 | Setting                                                                    |
| -------------------------- | -------------------------------------------------------------------------- |
| `~/.npm/_cacache`          | `--portable-except 'tmp/**'`                                               |
| pnpm store (v10 and below) | `--portable-except '**/*.lock'`                                            |
| pnpm store (v11 and above) | **do not set it** - `index.db` is a SQLite database and cannot be combined |

Share the whole of `_cacache`, not just `content-v2`. The content store is genuinely content-addressed, but npm looks entries up through `index-v5`, so content without the index is never found and buys nothing. The index buckets are append-only, which is why this works - but `npm cache verify` rewrites them, so do not run it while a build is using the cache.

Yarn Berry's `.yarn/cache` zips are content-addressed, with one catch: `yarn.lock` checksums are computed over the zip bytes, so machines must agree on `compressionLevel` or every entry mismatches.

### JVM

| Mountpoint                       | Setting                                              |
| -------------------------------- | ---------------------------------------------------- |
| `~/.gradle/caches/build-cache-1` | `--portable-except 'gc.properties,**/*.lock'`        |
| `~/.gradle/caches/modules-2`     | `--portable-except 'metadata-*/**'`                  |
| `~/.m2/repository`               | **do not set it** if you use `SNAPSHOT` dependencies |

A Maven `SNAPSHOT` is the exact thing this flag forbids: the same path holding different contents over time. Without snapshots the repository is safe, as `--portable-except '**/*-SNAPSHOT/**,**/*.lastUpdated,**/resolver-status.properties,**/_remote.repositories'` - but the simpler answer is to keep it local.

Gradle's build cache is content-addressed and portable. Its entries are keyed by an MD5 hash, which is a collision-resistance question rather than a sharing one, but worth knowing.

### Python

| Mountpoint            | Setting                                    |
| --------------------- | ------------------------------------------ |
| `~/.cache/pip/wheels` | `--portable-except ''`                     |
| `~/.cache/uv`         | `--portable-except 'interpreter-v*/**'`    |
| `$PIPENV_CACHE_DIR`   | `--portable-except ''`                     |
| virtualenvs, anywhere | **never** - absolute paths in every script |

`uv`'s archive cache is content-addressed and hard-links into environments. Its interpreter probe cache records where a Python interpreter is on *this* machine, which is why it is excluded.

### C and C++

| Mountpoint            | Setting                                                |
| --------------------- | ------------------------------------------------------ |
| ccache directory      | **do not set it** - use ccache's own remote storage    |
| CMake build directory | **never** - it is a configured build tree, not a cache |

ccache is content-addressed at the result level but its *manifests* are mutable: each new compilation that matches one appends to it. Excluding the manifests leaves results nothing can look up. ccache already speaks HTTP and Redis for exactly this purpose, so use that.

### Bazel

| Mountpoint               | Setting                |
| ------------------------ | ---------------------- |
| `--disk_cache` directory | `--portable-except ''` |
| repository cache         | `--portable-except ''` |

Both are plain content-addressed stores with no index, no database and no garbage-collection metadata beside the blobs. EarthBuild also serves the remote-execution API, so a Bazel build can use it as a remote cache directly and skip the mount.

### System package caches

| Mountpoint                | Setting                                                                  |
| ------------------------- | ------------------------------------------------------------------------ |
| `/var/cache/apt/archives` | `--portable-except 'partial/**,lock'`                                    |
| `/var/cache/apk`          | `--portable-except 'APKINDEX*'`                                          |
| `~/.nuget/packages`       | `--portable-except ''`                                                   |
| `~/.gem/ruby/*/cache`     | `--portable-except ''`                                                   |
| `/var/cache/dnf`          | **do not set it** - `repodata` and the `solv` files are rebuilt in place |

A `.deb` or `.apk` at a given version is the same file everywhere; the repository indexes beside them are not, which is why they are excluded. Note that apt's indexes live in `/var/lib/apt/lists` and its database in `/var/lib/dpkg` - neither belongs in a cache mount at all.

## Timestamps do not travel, and should not

A unit's bytes are its name, so a shared cache normalises every timestamp on the
way out - two machines holding identical entries must agree on a digest, and an
mtime never does.

**On the way in, the arriving file keeps the time it arrived.** That asymmetry is
deliberate. An mtime is not part of an entry's content; it is a fact about this
machine's copy, and several tools read it. Cargo compares mtimes in its
fingerprints, so a crate or source tree stamped 1970 would look older than
everything built from it - which reads as "already fresh, no rebuild needed",
the wrong direction for a mistake to point.

If your tool derives anything from an entry's timestamp rather than from its
contents, say so in its helper's `import` rather than relying on the file's own.

## Telling EarthBuild a unit never changes

A helper may answer a `props` verb with one property per line. One is understood:

```text
units-immutable
```

It means a key's unit never changes content - a new fact gets a new key, never
new bytes under an old one. True of a Go build cache (an action id is a hash of
the step's inputs), a Go module cache and a cargo `.crate` file; **false of npm**,
whose index buckets are append-only, so a key already present can have gained a
record since.

Where it holds, EarthBuild exports only the units its last map did not name. On a
warm cache that is the difference between framing four units and framing two
hundred and forty-five, and it grows with the cache.

A helper that does not implement `props` claims nothing and everything is
exported, which is the conservative reading and what every helper did before the
verb existed. **Do not claim it to go faster.** A cache whose units can change
under a stable key will share last build's bytes, and the symptom appears on
another machine.

## What a helper's `import` must promise

Two things, and only the helper can promise either - they are facts about the
cache's format, which is the whole reason a helper exists.

**A unit becomes visible whole or not at all.** Stage beside the destination and
rename. "Write it if it is absent, skip it if it is present" is the wrong rule:
it turns an interrupted import into permanent corruption, because the
half-written file is exactly what the skip preserves.

**It may run while the tools that own the cache are reading.** EarthBuild
serialises its own importers, one per cache directory, and cannot do more than
that - `--sharing=shared` is you saying several steps may use the directory at
once and the tools inside cope, which is a statement about *npm's* locking and
*cargo's*. An importer is not one of those tools.

## Caches are not shared from a microVM yet

On macOS, and on Linux with the Firecracker backend, the layer store lives on the
guest's own block device. A cache mount can only be read from the side it is on,
and EarthBuild's sharing runs on the host - so a build there prints

```text
caches are not shared from here: the store is on the guest's device,
  and a cache mount can only be read from the side it is on
```

once, and carries on. Builds are correct and no slower than they were; they just
do not fill or use a peer's cache.

Native Linux (`EARTH_VM=0`, and the default on a worker without Firecracker)
shares normally.

## A step that holds a secret shares no cache

`RUN --secret` or `--aws` on a step means none of its cache mounts cross, whatever
`--portable-except` says. The build prints the reason and carries on.

This is coarser than it could be and deliberately so. EarthBuild scans a step's
*output* for a secret's bytes, and a cache mount is not the output - it has never
been scanned, because until now its contents could not leave the machine. The
scan cannot simply be pointed at the mount either: it needs the secret's value,
which is staged beside the step and never reaches the part of the engine that
shares caches, and moving it there to do the scan would put credentials somewhere
they currently never go.

So the rule is mechanical rather than clever, and that is its advantage: it holds
for a secret the step base64'd into a config file or compiled into a binary,
which no scan of raw bytes would catch.

**If you want that cache shared, put the credential in its own step.** A
`RUN --secret` that fetches, then a plain `RUN` that builds, is two steps and only
the first is withheld.

## Untrusted builds: `EARTH_TRUST_DOMAIN`

A cache mount's directory is named by its `--id`, and that name is one namespace for every build a machine has ever run. On a shared worker that means a pull request from a fork writes into the same directory a protected-branch build reads - and signing does not help, because the attacker is a legitimate writer.

Set `EARTH_TRUST_DOMAIN` to a value that is stable **per trust level**, and every cache mount in that build is isolated to it:

```bash
EARTH_TRUST_DOMAIN=trusted   # protected branches
EARTH_TRUST_DOMAIN=fork      # pull requests from forks
```

EarthBuild cannot work this out for itself. Whether a build is trusted is a fact about your repository's policy - who may open a pull request, which branches are protected - and it lives in your CI configuration, not in anything an Earthfile can see.

Unset means the single implicit domain every build has always shared, which is the right default for a machine that only ever builds your own branches.

**Stable per trust level, never per run.** A value that changes every build isolates every build from every other. That is not a stricter security setting; it is a cache nobody ever hits. Use the trust level, not the run id.

## Working it out for yourself

Three questions, in this order. The first decides whether the flag belongs on this cache at all; the other two only fill in the list.

1. **Can a *part* of this cache be used on its own?** A worker fetches the paths a step actually reads, never the whole directory, so a cache that only works complete cannot be shared piecemeal however stable its contents are. A SQLite index, a repository database, a manifest every lookup passes through: each is perfectly portable and none is subsettable. If the answer is no, stop - the flag will not help, and the exclusion list cannot rescue it, because excluding the index leaves the entries unreachable.
2. **Would another machine's copy of a path do instead of your own?** Not "are the bytes identical", which is stronger than necessary. An entry carrying a build timestamp differs on every machine and answers the same question, so it is fine to share. An entry carrying `/home/alice/.cache` is byte-stable for ever and must never be.
3. **Which paths fail question 2?** Those go in the list: interpreter locations, absolute-path indexes, lockfiles, `tmp/` and `partial/` directories. Anything whose *meaning* is local to one machine - rather than merely anything that gets rewritten.

### A portable cache is portable within a lineage

Question 2 hides an assumption: "another machine's copy" means a machine running *the same step*, and a step's key covers its base image, so the toolchain and the paths are identical by construction.

A cache **id** is not covered that way. It is a name you choose, and two different steps using the same id share one directory even with different base images. Whether that is safe depends on whether the tool can tell a foreign entry apart. Go can - the compiler's build id is inside every ActionID, so an entry from another toolchain never matches. Most tools cannot.

So give a cache an id per lineage, not per purpose: `go-build-1.26` rather than `go-build`, if two targets in the same project build with different toolchains. The cost of being wrong is a cache that silently answers with another toolchain's work.

If you cannot answer the first question, leave the flag off. A cache each machine fills for itself is slower and always correct.

### Or measure it

Question 1 is mechanically checkable, and answering it that way is how the Go list above got
its two corrections. Fill the cache twice, at **deliberately different root paths**, and
compare every shared path:

```bash
A=$PWD/cache-a B=$PWD/cache-root-deliberately-much-longer
# ... populate both, however your tool does it ...
python3 - "$A" "$B" <<'EOF'
import hashlib, os, sys
def scan(root):
    out = {}
    for dp, dns, fns in os.walk(root):
        for fn in fns:
            p = os.path.join(dp, fn)
            if os.path.islink(p):
                continue
            with open(p, 'rb') as f:
                h = hashlib.sha256()
                for c in iter(lambda: f.read(1 << 20), b''):
                    h.update(c)
            out[os.path.relpath(p, root)] = h.hexdigest()
    return out
a, b = scan(sys.argv[1]), scan(sys.argv[2])
for r in sorted(a.keys() & b.keys()):
    if a[r] != b[r]:
        print(r)
EOF
```

Every path it prints belongs in the exclusion list, and every path it does not print must
stay out of one. The differing root paths are the point: a file that embeds the directory it
lives in is the commonest way a cache turns out not to be portable, and two runs at the same
path will never show it.
