# Working in this repository

## Run CI locally. That is what this tool is for

**Do not wait on GitHub Actions to diagnose a CI failure.** The suite CI runs is
an Earthfile, and `earth` runs it on your machine. A local run answers in
minutes and can be re-asked immediately with one variable changed; a CI round
takes about an hour, because the suites sit behind `Fast Check & Build` and a
long queue.

Measured on 2026-08-28: four CI rounds produced three wrong hypotheses and one
fix. One evening of local runs found a regression that had shipped that
afternoon, characterised an engine differential, and moved six targets from red
to green.

```bash
go build -o /tmp/earth ./cmd/earth

# the native engine (the default on this branch)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/guestd ./cmd/earth-guestd
EARTH_GUESTD=/tmp/guestd /tmp/earth -P --engine=native ./tests+copy-tilde-test

# the buildkit engine, against the prebuilt image - no local image build needed
EARTH_BUILDKIT_IMAGE=ghcr.io/earthbuild/earthbuild:buildkitd-v0.8.17-fix.1 \
  /tmp/earth -P --engine=buildkit ./tests+copy-tilde-test
```

Run both. A difference between the two engines is the finding; a failure in one
alone says nothing until you know what the other does.

### What a CI job maps to

`.github/workflows/ci.yml` names targets like `+test-no-qemu-group4`, which is
`Earthfile`'s `BUILD ./tests+ga-no-qemu-group4`, which is a list of `BUILD
+<name>-test` lines in `tests/Earthfile`. Run one of those directly.

### Four things that will waste an hour if you do not know them

| Symptom                                       | Cause                                                     |
| --------------------------------------------- | --------------------------------------------------------- |
| `unknown request <name>`                      | stale `earth-guestd`; rebuild it and set `$EARTH_GUESTD`  |
| `RUN --privileged ... refused by design`      | pass `-P`; the harness uses `RUN --privileged`            |
| `docker: manifest unknown` starting buildkitd | set `EARTH_BUILDKIT_IMAGE` to the ghcr image above        |
| a queued CI run vanishing                     | pushing cancels it; batch commits while awaiting a result |

## Reading a failing job

Read each job to **its own fatal error**, not to its most frequent one. Ranking
by how often a string appears produced three wrong answers in one day: the
frequent line is usually the harness reacting, and the cause is upstream of it.
And a fix that removes an error message has not necessarily fixed anything -
compare which jobs *pass* before and after, by name.

Filter job selectors by suite. Every suite has a `+test-no-qemu-group4`, so
`'group4' in name` silently answers about a different one; use
`name.startswith('Native')`.

## Searching this codebase

Most messages are built with a format verb, so the literal never appears in the
source. `grep` is a hypothesis generator here, never evidence - confirm by
running the case. Three claims in one day died to this, including "nothing reads
`EARTH_ENGINE`", which is read via `flag.EarthEnvVars("ENGINE")`.

## Where the written record lives

| File                                        | What it holds                                  |
| ------------------------------------------- | ---------------------------------------------- |
| `docs-internals/green-paper.md`             | the specification: state, invariants, `I1..In` |
| `docs-internals/plan-native-engine.md`      | phases, sequencing, costs, decisions taken     |
| `docs-internals/experiments-adversarial.md` | measurements, kill criteria, results (`E<n>`)  |
| `docs-internals/test-plan.md`               | test mechanisms, CI gates, corpora             |

A measurement belongs in the experiments file with a number, including the ones
that refuted an earlier entry. Several of today's entries exist only to withdraw
a previous claim, which is the point of keeping them.
