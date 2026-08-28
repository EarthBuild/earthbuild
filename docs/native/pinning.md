# Pinning image references

`FROM golang:1.26.5-alpine3.24` names a tag, and a tag moves. Every build therefore asks the registry
what the tag means right now, because that digest is what keys the cache: even a build with nothing
to do has to know whether the answer changed.

That lookup is most of a build that has nothing else to do - about 0.45s of a 0.5s no-op. A reference
that already names its digest skips it entirely:

```text
FROM golang:1.26.5-alpine3.24            planning 0.46s
FROM golang:1.26.5-alpine3.24@sha256:…   planning 0.03s
```

## Writing them down

```console
$ earth-native --pin
  ./Earthfile:4 golang:1.26.5-alpine3.24 -> golang:1.26.5-alpine3.24@sha256:787328…
```

This edits the Earthfile, and it is the only thing the engine does that changes a file you wrote - so
it happens only when you ask for it by name, never as a side effect of building. Run it again and it
reports `nothing to pin`.

A build that had to resolve anything says so, and says this:

```text
  pinned    golang:1.26.5-alpine3.24 -> golang@sha256:787328…
  note      --pin writes these into the Earthfile, which makes the build reproducible and skips the lookup
```

What is left alone: target references (`+base`, `./dir+target`), `scratch`, `FROM DOCKERFILE`,
references built from an argument, and anything that already names a digest. A reference the registry
cannot be reached for is reported and left as written - an unreachable registry means a file this
could not improve, not one it damaged.

## Keeping them up to date

The form is `image:tag@sha256:…` rather than `image@sha256:…`, and the tag is doing real work: it is
what you read to know which version you are on, and it is what
[Renovate](https://docs.renovatebot.com)'s `docker` datasource matches to bump **both** halves when a
new version appears. Pinning is not freezing.

Renovate's `dockerfile` manager does not look at `Earthfile` by default. One stanza points it there:

```json5
{
  dockerfile: {
    enabled: true,
    managerFilePatterns: ['/Earthfile/', '/.*\\.earth$/'],
  },
}
```

This repository already carries that, in `.github/renovate.json5`.

Note that `default:pinDigestsDisabled` - which this repository also extends - only stops Renovate
*adding* digests to references that lack them. A reference that already names one is kept current,
which is exactly the arrangement `--pin` is for: you decide what is pinned, Renovate keeps it moving.

## What it costs not to

An unpinned `FROM` is resolved against the origin registry on every build, because a tag is a
question about today rather than a fact - see `EARTH_STREAM_TO_GUEST` and `resolve.go` for why
that lookup deliberately does not use a mirror or a cache. The lookup is two round trips, a token
and a manifest, and it is on the critical path: nothing about the build can start until the base
is known.

Measured on Linux, one `RUN` step, warm cache, best of three:

| build           | unpinned | pinned |
| --------------- | -------- | ------ |
| nothing to do   | 412ms    | 9ms    |
| one step to run | 498ms    | 71ms   |

Forty-six times faster on a no-op and seven times on a one-step change - and the difference is
almost exactly the same 427ms either way, because it is a fixed cost paid before anything happens.
On an incremental build, which is what a developer runs all day, that fixed cost *is* the build.

The engine says so itself when the lookups are slow enough to matter: the `note` line after a build
reports what that build's own lookups cost, measured rather than estimated.

This is why pinning is worth more than it looks. Reproducibility is the reason it exists; on a
machine where somebody is running `earth` every few seconds, the latency is the reason they will
keep it.
