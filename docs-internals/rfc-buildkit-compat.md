# EARTH_BUILDKIT_COMPAT

A switch that makes this engine answer as buildkit does, so the places it
deliberately answers differently can be told from the places it cannot answer at
all.

## The distinction that shapes it

A compat switch can flip a **choice**. It cannot grant a **capability**. Today's
divergences are three kinds and only two of them are in scope:

| kind                 | examples                                                                                                        | in scope |
| -------------------- | --------------------------------------------------------------------------------------------------------------- | -------- |
| deliberate refusal   | `RUN --privileged` refused by name wherever it appears; `host-bind`; `BUILD --auto-skip`; a `WITH DOCKER` cache | yes      |
| behaviour difference | `/run` absent from a listing; `WITH DOCKER --load` as two steps; message wording                                | yes      |
| capability gap       | `LOCALLY`; cross-architecture emulation                                                                         | **no**   |

The third kind is why the switch must **fail loudly rather than fall back**. A
compat mode that quietly builds something else when it meets `LOCALLY` is worse
than one that refuses, because the whole point is to make divergence visible.

## What it would actually move

Measured 2026-08-29. Of the 53 invocations the parity gate counts against this
engine, a differential under both engines found **none** that this engine fails
and buildkit builds (E882c). So the switch does not move the parity number: what
it moves is the seven targets already outside the denominator as
`refused on purpose`, and the three behaviour decisions that account for eleven
failing Native CI jobs.

That is the honest size of it. It is worth having for what it *proves* rather
than for what it fixes: with the switch on, any remaining difference is a defect
rather than a policy, which is a much sharper thing to test than "these two
engines disagree somewhere".

## Privilege belongs in its own switch

`RUN --privileged` is refused by name wherever it appears, and that refusal is
the engine's most load-bearing safety property. Folding it into a general compat
flag means a developer who wanted matching *listings* also gets privileged
execution, which is not a trade anyone chose knowingly.

Two switches, not one:

* `EARTH_BUILDKIT_COMPAT` - wording, listings, mount scope, ordering. Cheap and
  safe to leave on.
* a separate, louder opt-in for privilege, which is what `--allow-privileged`
  already almost is: it is accepted today and grants nothing, so the name exists
  and would only need to start meaning something under compat.

## The cost, stated

Every compat-able difference doubles the behaviour surface: two answers to test,
two to document, and a test suite that must say which one it expects. That is the
real price, and it argues for keeping the list short and closed rather than
letting it grow to cover each new disagreement.

It also wants `cmd/earth-diff` to be worth anything. Without a differential, the
switch's claim - "with this on, we match" - is untested. With one, it is a gate.
The tool is costed at 3-4 engineer-weeks in the test plan and an hour of
hand-rolling it already changed the reading of the parity number three times.

## Recommendation

Worth building, in this order, and not before:

1. `cmd/earth-diff`, because the switch cannot be verified without it.
2. The behaviour differences - listings, mount scope - which are small and where
   matching costs nothing anyone values.
3. Privilege, separately, loudly, and only if somebody wants it.
