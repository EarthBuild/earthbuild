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
