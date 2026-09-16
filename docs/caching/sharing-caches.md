# Sharing caches between machines

A `CACHE` mount belongs to the machine that filled it. `--immutable-except` says it may be shared with others:

```Dockerfile
CACHE --id go-mod --immutable-except 'lock,**/*.lock,**/*.partial' /go/pkg/mod
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

| Mountpoint                   | Setting                                                                                                                 |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `/go/pkg/mod`                | `--immutable-except 'cache/lock,cache/download/**/*.lock,cache/download/**/*.partial,cache/download/sumdb/*/lookup/**'` |
| `/go/pkg/mod/cache/download` | the same list, and 3.8x smaller - see below                                                                             |
| `/root/.cache/go-build`      | **do not set it** - see below                                                                                           |

The module cache is the best case there is, and it is the one setting here that has been
measured rather than reasoned about. Two caches populated independently at deliberately
different root paths held **95,283 paths each, of which 95,282 were byte-identical**, with
no difference in mode and no path present in only one. A third, populated twenty minutes
later, agreed on all 3,871 paths it shared.

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

The *build* cache is not. Its entries are immutable, and combining two of them is harmless - it is simply useless, because a build cache entry is keyed on an ActionID that includes absolute paths. Two machines compute different ActionIDs for the same compilation, so a shared cache is never read. Let each machine keep its own.

### Rust

| Mountpoint                   | Setting                                                   |
| ---------------------------- | --------------------------------------------------------- |
| `$CARGO_HOME/registry/cache` | `--immutable-except ''`                                   |
| `$CARGO_HOME/registry/src`   | `--immutable-except ''`, with care                        |
| `target/`                    | **never** - absolute paths in `.d` files and fingerprints |

Two warnings on `registry/src`. Cargo performs **no content verification** when reusing an extracted source tree, and unlike `vendor/` there is no `.cargo-checksum.json` there to check against - so a corrupt entry propagates silently into a build. Cargo also does not make the directories read-only, so a `build.rs` can modify one in place.

For compilation results use `sccache` with its own S3 or Redis backend rather than sharing a directory.

### Node

| Mountpoint                 | Setting                                                                    |
| -------------------------- | -------------------------------------------------------------------------- |
| `~/.npm/_cacache`          | `--immutable-except 'tmp/**'`                                              |
| pnpm store (v10 and below) | `--immutable-except '**/*.lock'`                                           |
| pnpm store (v11 and above) | **do not set it** - `index.db` is a SQLite database and cannot be combined |

Share the whole of `_cacache`, not just `content-v2`. The content store is genuinely content-addressed, but npm looks entries up through `index-v5`, so content without the index is never found and buys nothing. The index buckets are append-only, which is why this works - but `npm cache verify` rewrites them, so do not run it while a build is using the cache.

Yarn Berry's `.yarn/cache` zips are content-addressed, with one catch: `yarn.lock` checksums are computed over the zip bytes, so machines must agree on `compressionLevel` or every entry mismatches.

### JVM

| Mountpoint                       | Setting                                              |
| -------------------------------- | ---------------------------------------------------- |
| `~/.gradle/caches/build-cache-1` | `--immutable-except 'gc.properties,**/*.lock'`       |
| `~/.gradle/caches/modules-2`     | `--immutable-except 'metadata-*/**'`                 |
| `~/.m2/repository`               | **do not set it** if you use `SNAPSHOT` dependencies |

A Maven `SNAPSHOT` is the exact thing this flag forbids: the same path holding different contents over time. Without snapshots the repository is safe, as `--immutable-except '**/*-SNAPSHOT/**,**/*.lastUpdated,**/resolver-status.properties,**/_remote.repositories'` - but the simpler answer is to keep it local.

Gradle's build cache is content-addressed and portable. Its entries are keyed by an MD5 hash, which is a collision-resistance question rather than a sharing one, but worth knowing.

### Python

| Mountpoint            | Setting                                    |
| --------------------- | ------------------------------------------ |
| `~/.cache/pip/wheels` | `--immutable-except ''`                    |
| `~/.cache/uv`         | `--immutable-except 'interpreter-v*/**'`   |
| `$PIPENV_CACHE_DIR`   | `--immutable-except ''`                    |
| virtualenvs, anywhere | **never** - absolute paths in every script |

`uv`'s archive cache is content-addressed and hard-links into environments. Its interpreter probe cache records where a Python interpreter is on *this* machine, which is why it is excluded.

### C and C++

| Mountpoint            | Setting                                                |
| --------------------- | ------------------------------------------------------ |
| ccache directory      | **do not set it** - use ccache's own remote storage    |
| CMake build directory | **never** - it is a configured build tree, not a cache |

ccache is content-addressed at the result level but its *manifests* are mutable: each new compilation that matches one appends to it. Excluding the manifests leaves results nothing can look up. ccache already speaks HTTP and Redis for exactly this purpose, so use that.

### Bazel

| Mountpoint               | Setting                 |
| ------------------------ | ----------------------- |
| `--disk_cache` directory | `--immutable-except ''` |
| repository cache         | `--immutable-except ''` |

Both are plain content-addressed stores with no index, no database and no garbage-collection metadata beside the blobs. EarthBuild also serves the remote-execution API, so a Bazel build can use it as a remote cache directly and skip the mount.

### System package caches

| Mountpoint                | Setting                                                                  |
| ------------------------- | ------------------------------------------------------------------------ |
| `/var/cache/apt/archives` | `--immutable-except 'partial/**,lock'`                                   |
| `/var/cache/apk`          | `--immutable-except 'APKINDEX*'`                                         |
| `~/.nuget/packages`       | `--immutable-except ''`                                                  |
| `~/.gem/ruby/*/cache`     | `--immutable-except ''`                                                  |
| `/var/cache/dnf`          | **do not set it** - `repodata` and the `solv` files are rebuilt in place |

A `.deb` or `.apk` at a given version is the same file everywhere; the repository indexes beside them are not, which is why they are excluded. Note that apt's indexes live in `/var/lib/apt/lists` and its database in `/var/lib/dpkg` - neither belongs in a cache mount at all.

## Working it out for yourself

Three questions, in this order:

1. **Is a path ever written twice with different contents?** If yes, the flag is unsafe - and that is the whole test. Version-numbered artefacts pass; `SNAPSHOT`, `latest`, `nightly` and rebuilt indexes fail.
2. **What is rewritten rather than appended to?** Lockfiles, `tmp/` and `partial/` directories, statistics, garbage-collection bookkeeping, SQLite databases. These go in the exclusion list.
3. **Is anything outside the mountpoint required to read it?** An index, a database, a manifest. If the store cannot be used without it and it cannot be shared, sharing the store buys nothing.

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
