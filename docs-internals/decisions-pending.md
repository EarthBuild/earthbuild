# Decisions this engine is waiting on

Every item here is blocked on a judgement rather than on work, and each carries
the number that judgement needs. Measured 2026-08-29 unless said otherwise;
sources are the `E8xx` entries in `experiments-adversarial.md`.

## Correctness and behaviour

| decision                         | what it costs now                                                                                                                                                                                                                                                                                                                                                                        | evidence   |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| `ip link add` in a private netns | 2 failing Native jobs. **Costed**: a bare private netns for privileged steps would break ten of the thirteen `RUN --privileged` in the corpus, because ten are `RUN_EARTH` running an inner build that fetches. It needs a netns with connectivity - veth and NAT - which is work, not a flag.                                                                                           | E882, E887 |
| `/run` listing expectation       | 1 failing Native job. **Not what its name says** (E885): a step's root is identical under both engines and lists `run/` from `/home`, so it is not "native omits /run". The failing case is inside the integration test image under an inner invocation, which cannot be built locally - blocked by the remote `FROM DOCKERFILE` failure in the nits file. Unscoped until that is fixed. | E882, E885 |
| dockerd pre-script semantics     | 1 failing Native job, the only one that is neither the cgroup privilege nor a documented limitation (E910). **Reproduces locally**, where `WITH DOCKER` works (E911). Passing the test is easy and would not implement the feature: the hook configures the daemon that starts next, and here the daemon runs beside the step rather than in it (E368).                                  | E913       |

These are the only Native CI failures traceable to a decision. The rest of that
suite's failures are cross-architecture work the engine states it does not do,
or the harness.

## Speed, with prices

| decision                                                    | gain                                                  | price                                                                                |
| ----------------------------------------------------------- | ----------------------------------------------------- | ------------------------------------------------------------------------------------ |
| cache the registry token across builds                      | 0.40s of a 0.48s incremental rebuild (E900)           | a bearer token on disk                                                               |
| layers on tmpfs (`EARTH_IMAGE_CACHE_DIR` splits the store)  | 1.42x on a 30-step build                              | ~1.1GB of RAM for a golang base, and a build that exceeds it fails rather than slows |
| a guest that listens, instead of `container exec` per build | 165ms of every macOS build                            | a listening service inside the sandbox - a different security posture                |
| prefetch image blobs on the host while the sandbox boots    | up to 0.58s of a 2.3s cold build                      | blobs kept on disk - 61MB a layer (E659)                                             |

`EARTH_ASYNC_RELEASE` is **no longer on this list**. It defers a cost that belongs
to the store's filesystem - 19.5ms on ext4, 0.00ms on tmpfs for identical work -
so no single default was ever going to be right, and the switch is the correct
shape (E883).

## Tooling

| decision               | what it would settle                                                                                                                                                                                                                                                      |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| build `cmd/earth-diff` | costed at 3-4 engineer-weeks in the test plan. An hour of hand-rolling it changed the reading of the parity number three times, and finally showed that none of the 37 invocations the gate counts against this engine is a place it diverges from the reference (E882c). |

## One blocker sits under two of these

The remote `FROM DOCKERFILE` failure - recorded in the nits file, and bisected to
remoteness rather than to anything in the Dockerfile - stops any `./tests+<name>`
target building locally under `--engine=native`. That blocks the `/run` item,
which can only be reproduced inside the integration test image, and it blocks
diagnosing anything else in that suite without a CI round.

It is the cheapest thing on this page to be wrong about: everything else here is
a judgement, and that one is a defect with a known bisect and no owner.

## The gap is fully accounted for

Nothing in the parity shortfall is unexplained work. It divides into:

| part                          | nature                                                                                                     |
| ----------------------------- | ---------------------------------------------------------------------------------------------------------- |
| targets their recipe prepares | the harness lifts them out of the recipe that makes their fixture; buildkit fails them identically (E882c) |
| three behaviour decisions     | the table above                                                                                            |
| documented limitations        | `LOCALLY`, which this engine does not run, and cross-architecture emulation, which it says it does not do  |

The single divergence the differential found - `for.earth+all` - is the first of
those: `test-for-ls-locally` opens with `LOCALLY`, and the engine refuses it in
as many words. So getting to 257 needs `LOCALLY`, the three decisions, and a
harness that does not count what it cannot stage - in that order of size, and
none of them is discovery.

## What is not a decision

Worth stating so it is not re-litigated. The parity figure - `196 of 249` - counts
how many of the tree's invocations survive being lifted out of the recipe that
prepares them, not how much of the language this engine implements. Raising it by
excluding what cannot build alone was tried twice and reverted twice, both times
caught by the same check: **an exclusion moves the denominator and must leave the
numerator alone** (E880, E880b). One narrow rule survived, worth three
invocations.
