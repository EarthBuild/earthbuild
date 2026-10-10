# Sharing a cache mount between machines

`CACHE` gives a step a directory that survives between builds **on one machine**.
Two flags make its contents available to other machines as well:

```Earthfile
CACHE --portable-except 'tmp/**' --helper ./cachehelper-npm.wasm /root/.npm/_cacache
```

`--portable-except` is the author's claim that a peer's copy of a path under this
mount is as good as your own, naming the paths where that is not true.
`--helper` is a WebAssembly module that says what *crossing* means for this
format: what a unit is, what it is called, and how two of them merge.

**Both are required.** A claim with no helper is a directory nothing can take
apart; a helper with no claim is a directory whose author never offered it.
Either way the contents stay put, which is what every cache mount did before.

## Running these

```bash
earth ./examples/cache-helpers+all
```

Nothing to build first. Each `--helper` names an artifact of the repository's
`+cache-helper` target:

```Earthfile
CACHE --portable-except 'tmp/**' \
    --helper ../../..+cache-helper/build/cachehelper-npm.wasm /root/.npm/_cacache
```

which is resolved while the plan is made, the way `COPY +target/artifact` is -
so a helper is an ordinary build input rather than a file somebody has to
remember to produce. These run in CI beside every other example for the same
reason.

A plain path still works and means the directory of the Earthfile that wrote it:
`--helper ./h.wasm` is the right thing when the module is committed or built
outside the build.

## What each one shows

| Example | Cache | Why it is interesting |
| ----------- | --------------------------- | ------------------------------------------------------------ |
| `go-build` | `~/.cache/go-build` | compute, not downloads - the case sharing exists for |
| `go-mod` | `$GOMODCACHE/cache/download` | the GOPROXY layout, path for path |
| `npm` | `~/.npm/_cacache` | append-only buckets: a union, not a file copy |
| `cargo` | `registry/cache` only | `.crate` files are immutable; the index beside them is not |

**npm is the one that proves the helper is necessary.** cacache's `index-v5`
buckets hold several records each and are appended to, so importing one means
unioning records rather than writing a file. A generic file-level importer -
write it if absent, skip it if present - is correct for a content-addressed blob
and *silently wrong* here: it discards every record the sender had and the
receiver lacked.

It is also the one format that does not claim `units-immutable`, because a key
already present can have gained a record since. The other three do claim it, and
an export then ships only the units the last map did not name.

## What you will see

```text
cache eg-go-build: 241 units shared, map 4ae48884...
```

and on a second build of something different against the same cache:

```text
cache eg-go-build: 245 units shared (4 new), map 774438d5...
```

On a machine that has one, a peer's units arrive before the step runs:

```text
cache eg-npm: 16 units stocked
```

## Things worth knowing

**A step that holds a secret shares no cache.** `RUN --secret` or `--aws`
withholds every cache mount in that step, whatever `--portable-except` says, and
says so. A cache's contents have never been scanned for a credential, because
until now they could not leave the machine. Put the credential in its own step if
you want the cache shared.

**Untrusted builds want `EARTH_TRUST_DOMAIN`.** A cache's directory is named by
its `--id`, and on a shared worker a pull request from a fork writes where a
protected-branch build reads. Set the variable to something stable per *trust
level* and the two are isolated.

**Not from a microVM yet.** On macOS, and on Linux with the Firecracker backend,
the store lives on the guest's own block device and a cache mount can only be
read from the side it is on. Those builds say so once and carry on. Native Linux
shares normally.

There is more in [docs/caching/sharing-caches.md](../../docs/caching/sharing-caches.md),
including how to choose `--portable-except` for a cache not listed here.

## Writing your own helper

The four here are one Go program, `tools/cachehelper`, built once per format -
a helper is **one** format, because `import` is handed a cold empty directory and
nothing can be probed in one. It implements six verbs over stdin and stdout:
`probe`, `ident`, `index`, `export`, `import` and `props`.

The two obligations only a helper can meet are in
[docs/caching/sharing-caches.md](../../docs/caching/sharing-caches.md): a unit
becomes visible whole or not at all, and an import may run while the tools that
own the cache are reading.
