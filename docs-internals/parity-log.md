# Parity log

Where the native engine is against the reference, one row per day.

**Planning is not the interesting column any more.** The corpus reports no
unimplemented construct - 491 targets across 193 Earthfiles, none blocked on
something this engine has not built - so the `linux`/`darwin` counts move only
when the corpus itself grows. Execution is the number still in question, and it
is the one to read.

`built / plan` is `linux-earthtests-run` over `linux-earthtests` from
`corpus-ratchet.txt`: how many of the `tests/*.earth` targets that the engine can
plan actually build under it.

**A row is the state at the *start* of that date**, not the end - so a row says
what the day was handed, and the difference between two rows is what the day in
between did. Recording the end of the day instead puts a day's work in the row
that already carries its date, which reads as though the day started where it
finished.

⚠︎ **`built` is a floor, not a measurement.** The gate stores one below the best
seen and only moves when somebody records a rise, so a flat column means "nobody
has bumped it", which is not the same as "nothing improved". A row whose figure
came from a real sweep says so in Notes; anything else is inherited from the
previous day.

| date       | plan linux | plan darwin | built / plan  | parity    |
| ---------- | ---------- | ----------- | ------------- | --------- |
| 2026-08-20 | 476        | 484         | 156 / 251     | 62.2%     |
| 2026-08-21 | 476        | 484         | 156 / 251     | 62.2%     |
| 2026-08-22 | 476        | 490         | 156 / 251     | 62.2%     |
| 2026-08-24 | 476        | 491         | 156 / 251     | 62.2%     |
| 2026-08-26 | 483        | 491         | 156 / 252     | 61.9%     |
| 2026-08-27 | 483        | 493         | 156 / 252     | 61.9%     |
| 2026-08-28 | 486        | 494         | 156 / 257     | 60.7%     |
| 2026-08-29 | 486        | 494         | 156 / 257     | 60.7%     |
| 2026-08-29 | 486        | 494         | **196 / 252** | **77.8%** |

## Notes

| date       | note                                                                                                                                                                                                                                                                                                   |
| ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 2026-08-20 | First row. `built` 156 recorded here and unchanged since.                                                                                                                                                                                                                                              |
| 2026-08-27 | Parity falls to 60.7% without execution regressing: the denominator grew from 251 to 257 as more targets planned.                                                                                                                                                                                      |
| 2026-08-29 | Nine days at 156. Sweep run to find whether the floor understates it.                                                                                                                                                                                                                                  |
| 2026-08-29 | It did, by forty. A sweep on the x86 box reports **196 of 252**; the 156 floor had not been raised since 2026-08-20, so every parity figure quoted from this file before today was 17 points low.                                                                                                      |
| 2026-08-29 | The three largest failure groups are targets whose wrapper makes their fixture first (a rename, a touch, a sed on the VERSION line), so they cannot build standalone. Ten of the 56 shortfall are denominator, not engine (E879).                                                                      |
| 2026-08-29 | `WITH DOCKER --load` fixed (E886b): storage is now scoped to the block rather than the step. Eight of the thirteen failing Native CI jobs turn on it, and it also unblocked local integration testing, which had been failing on a remote `FROM DOCKERFILE` that turned out to be the same bug (E888). |
| 2026-08-29 | Rows recomputed as start-of-day; an earlier draft of this file used end-of-day and was one row out.                                                                                                                                                                                                    |

## What the remaining gap is made of

Measured 2026-08-29, from a sweep on x86 and the Native suite in CI.

| part                                 | size           | nature                                                                                                                                                             |
| ------------------------------------ | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| not standalone-buildable             | 19 of 56       | the target's wrapper makes its fixture first - a touch, a rename, a generated Dockerfile, a `sed` on the VERSION line. Five families, each checked by hand (E879). |
| awaiting a decision                  | 3 causes       | `/run` listing, `ip link add` in a private netns, `WITH DOCKER --load`. Every Native failure in CI so far is one of these.                                         |
| refused on purpose                   | 7 targets      | already out of the denominator by the gate's own rule                                                                                                              |
| needs an option the gate cannot pass | 21 invocations | already excluded by the gate                                                                                                                                       |

**The Native suite is 2 of 16, not the near-miss an early sample suggested.**
Thirteen jobs fail and the causes are varied rather than one thing:

| n   | cause                                                                                                                       |
| --- | --------------------------------------------------------------------------------------------------------------------------- |
| 9   | `cache initialization failed: Operation not permitted` - a *nested* docker daemon inside a step, not the engine's own cache |
| 5   | `RUN --privileged --mount=type=tmpfs,target=/tmp/earthbuild-tmpfs`                                                          |
| 3   | `the step producing /earthly/build/earthly did not run`                                                                     |
| 3   | `BUILD --pass-args`, already specified and unfixed                                                                          |
| 2   | `ip link add dummy0` (decision)                                                                                             |
| 2   | buildkit connect timeout                                                                                                    |
| 1   | `/run` listing (decision); 1 `docker load` (decision)                                                                       |

⚠︎ **The obvious unifying theory is wrong.** Twelve of the thirteen carry
`mount /sys/fs/cgroup for the step: operation not permitted`, which reads as one
root cause behind all of it - until a *passing* job turns out to carry it too. It
is ubiquitous on this runner and discriminates nothing.

**Grouping those by the first `Error:` line was also wrong**, and the correction
is worth keeping. `Error: BUILD --pass-args` looked like a defect in an argument
feature; the message's last clause is `this step is for linux/arm64 and this
build has linux/amd64`, which is cross-architecture emulation the engine
documentedly does not do (E596). The first line of an error chain is the
outermost frame, and this tree wraps almost everything in `RUN_EARTH`, so the
outer frame is nearly always the harness.

By root cause instead:

| n   | root cause                                                       | area                                                         |
| --- | ---------------------------------------------------------------- | ------------------------------------------------------------ |
| 14  | `RUN --privileged --mount=type=tmpfs ... /tmp/earthbuild-script` | still the harness frame; the cause is inside the inner build |
| 8   | `docker load`, `docker inspect`, `docker images`, prescript      | `WITH DOCKER` - decision                                     |
| 3   | `this step is for linux/arm64 and this build has linux/amd64`    | cross-arch, not supported by design                          |
| 3   | `the step producing /earthly/build/earthly did not run`          | unread                                                       |
| 2   | `ip link add dummy0`; 1 `/run` listing                           | decisions                                                    |

So: a large share is `WITH DOCKER`, three are a limitation the engine states
plainly, and the largest bucket is still a harness frame that has to be opened
one build at a time. `+test-qemu` in particular cannot pass and is worth moving
out of a suite that reports it as a failure.

The order is: fix the denominator; take the decisions; move what cannot pass out
of the suite; then read what remains.

## Adding a row

Read `corpus-ratchet.txt` and append. To know whether `built` is real rather than
inherited, run the sweep - it is Linux-only, pulls images and reaches the
network, and prints its failures grouped by reason with the largest group first:

```bash
EARTH_TEST_NETWORK=1 go test -tags integration ./engine/cli/ \
    -run TestHowManyEarthTestsBuild -timeout 120m -v
```

Both the tag and the variable are required and neither is optional-looking:
without `-tags integration` the file is not compiled and `go test` reports
`no tests to run` and **passes**, and without `EARTH_TEST_NETWORK=1` the test
skips itself. Either way the run is green and measures nothing.

That grouping is the work list: the biggest group is the next thing to fix.
