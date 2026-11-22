# `earth-skip`

Build a target, or answer in a tenth of a second that nothing it reads has
changed since the last time it was built.

```yaml
- uses: actions/checkout@v4

- uses: ./.github/actions/earth-skip
  with:
    target: +test
    github-token: ${{ secrets.GITHUB_TOKEN }}
    build-args: |
      RUST_VERSION=1.86
      FEATURES=full
```

The action installs EarthBuild through `earthbuild/actions-setup`, restores the
record of what the target read last time, builds only if something it reads has
moved, and saves the record again.

## What it is for

A layer cache answers "have I built this exact step before". This answers "does
this job need to run at all", and it answers it over **what the build read**
rather than what it was given. A file copied into the build context and never
opened does not invalidate it - which is the case a content-hash of the context
cannot express, and the reason this exists.

Measured on a Substrate node: a cold build takes about twenty minutes; a build
whose inputs have not moved answers in **0.10 s** from a 283 KB record naming
676 files read, 706 paths that must stay absent, and 53 directory listings.

## The job still runs

The record names checkout paths whose digests have to be re-read to answer the
question, so the checkout is unavoidable and no `if:` gate on an earlier job can
be derived from it. What is saved is the build, not the job:

|          | without  | with     |
| -------- | -------- | -------- |
| checkout | ~1-2 min | ~1-2 min |
| build    | ~20 min  | 0.10 s   |

## Inputs

| Name               | Default       | Meaning                                                      |
| ------------------ | ------------- | ------------------------------------------------------------ |
| `target`           | *required*    | Target to build, e.g. `+test` or `./sub+test`                |
| `build-args`       | `''`          | `NAME=value` per line; see below                             |
| `platform`         | runner's      | Platform to build for                                        |
| `extra-args`       | `''`          | Passed to `earth` verbatim                                   |
| `setup`            | `true`        | Install EarthBuild; false when the workflow already did      |
| `version`          | `latest`      | Version for `actions-setup`                                  |
| `github-token`     | `''`          | For `actions-setup`'s release-list call; pass `GITHUB_TOKEN` |
| `cache-key-prefix` | `earth-skip`  | Prefix for the cache key holding the record                  |
| `record-path`      | `.earth-skip` | Directory the record lives in between runs                   |

`build-args` is a newline-delimited list because **GitHub Actions inputs are
strings**: there is no list or map type for `with:`, in composite actions or
reusable workflows. This is the same shape `docker/build-push-action` uses, for
the same reason. Only the first `=` separates, so a value may contain spaces and
further `=`; blank lines and `#` comments are ignored; and a line with no `=` is
an **error**, because an argument silently dropped builds something other than
what the workflow says.

Order does not matter - the shape hashes arguments sorted - so reordering the
list does not cost a rebuild.

Secrets do not belong here. `with:` values are easy to spill into logs; pass
secrets through `env:` instead. Without `EARTH_HMAC` configured a build is keyed
on *which* secrets it reads and not on their values, which the engine says out
loud when it happens.

## Outputs

| Name      | Meaning                                              |
| --------- | ---------------------------------------------------- |
| `skipped` | `'true'` when the build was answered from the record |
| `reason`  | Why it could not be skipped, when it could not       |

## What can never be skipped

A build whose target contains `LOCALLY` or a `--no-cache` step records no
skippable answer, and the action surfaces the reason as a run annotation:

```text
+deploy cannot be skipped: Earthfile:12 runs LOCALLY, on this machine and
outside the build: skipping it would skip whatever it writes there
```

A host step writes outside the build, so skipping it does not give a coarser
answer - it gives no answer. `--ci` (and `--strict`, which it implies) refuses
`LOCALLY` at plan time instead, so a workflow passing `extra-args: --ci` never
reaches this.

## Caching

Cache keys are immutable, so the key rolls per run and older records are found
by prefix through `restore-keys`. A pull request sees its own branch's caches
and the default branch's, so its first run asks "has anything I read changed
since main".

The record is saved only when the **build step** succeeded. `always()` would
save records no build produced; keying on the job would discard a record the
build legitimately earned when a later step failed. Losing a record costs the
next run a build, which is the safe direction.

A store populated before placements were recorded with cache entries does not
acquire them, because entries are inserted and removed but never rewritten
(I9) - such a build falls back to the coarser plan fingerprint until those
entries are evicted.

## See also

* `docs/native/skipping-a-job.md` - the user-facing description
* `docs-internals/job-skipping.md` - the keys, the refusal gates and why
