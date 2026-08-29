# Decisions this engine is waiting on

Every item here is blocked on a judgement rather than on work, and each carries
the number that judgement needs. Measured 2026-08-29 unless said otherwise;
sources are the `E8xx` entries in `experiments-adversarial.md`.

## Correctness and behaviour

| decision                         | what it costs now                                                                                                                                                                                                                                                                                        | evidence   |
| -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| `WITH DOCKER --load` mount scope | 8 of the 13 failing Native CI jobs                                                                                                                                                                                                                                                                       | E882       |
| `ip link add` in a private netns | 2 failing Native jobs                                                                                                                                                                                                                                                                                    | E882       |
| `/run` listing expectation       | 1 failing Native job. **Narrower than it looks**: a step's root is identical under both engines - `ls -1 /` on alpine gives the same list - so this is not "native omits /run". It is `test-no-parent-at-root-from-home` completing `../` from `/home` inside the test image, under an inner invocation. | E882, E885 |

These are the only Native CI failures traceable to a decision. The rest of that
suite's failures are cross-architecture work the engine states it does not do,
or the harness.

## Speed, with prices

| decision                                                    | gain                                                  | price                                                                                |
| ----------------------------------------------------------- | ----------------------------------------------------- | ------------------------------------------------------------------------------------ |
| cache the registry token across builds                      | 292ms of a 446ms cached unpinned build on Linux - 65% | a bearer token on disk                                                               |
| layers on tmpfs (`EARTH_IMAGE_CACHE_DIR` splits the store)  | 1.42x on a 30-step build                              | ~1.1GB of RAM for a golang base, and a build that exceeds it fails rather than slows |
| a guest that listens, instead of `container exec` per build | 165ms of every macOS build                            | a listening service inside the sandbox - a different security posture                |

`EARTH_ASYNC_RELEASE` is **no longer on this list**. It defers a cost that belongs
to the store's filesystem - 19.5ms on ext4, 0.00ms on tmpfs for identical work -
so no single default was ever going to be right, and the switch is the correct
shape (E883).

## Tooling

| decision               | what it would settle                                                                                                                                                                                                                                                      |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| build `cmd/earth-diff` | costed at 3-4 engineer-weeks in the test plan. An hour of hand-rolling it changed the reading of the parity number three times, and finally showed that none of the 37 invocations the gate counts against this engine is a place it diverges from the reference (E882c). |

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
