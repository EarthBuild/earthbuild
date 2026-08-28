# Conventions

## Naming

The project is being renamed from Earthly to EarthBuild. When writing new code,
comments, docs or commit messages:

* Use `earth` for the CLI / build tool (not `earthly`).
* Use `EarthBuild` for the project name (not `Earthly`).
* Leave existing `earthly` identifiers alone where they are literal: Earthfile
  target names (`+earthly-docker`) and published image/repo names. Those change
  only as part of the rename itself.
* Already renamed, do not reintroduce: the installed binary is `/usr/bin/earth`
  and the image entrypoint is `/usr/bin/earth-entrypoint.sh` (each with an
  `earthly`-spelled symlink kept for one deprecation cycle), the in-image config
  lives at `/etc/.<installation name>/config.yml` (the old
  `/etc/.earthly/config.yml` is still honoured if mounted), and the env var
  prefix is `EARTH_*` (the `EARTHLY_*` spelling is still read, with a warning).

## Golang

* Use the concepts and capabilities of Go version declared in `go.mod`. Read more here: <https://go.dev/blog/go1.26>
* Prefer std packages over 3rd party packages, where possible.
* Ensure all exposed interfaces and types are documented.

## Naming & Terminology

* The `earthly` naming is deprecated and being systematically eliminated.
* Use `earth` when referring to the CLI command.
* Use `EarthBuild` when referring to organization or project context.

## Earthfile Parser

* When making changes to the parser, it is critical to keep [earthfile.abnf](file:///Users/jhorsts/projects/earthbuild/earthbuild/internal/earthfile/earthfile.abnf) up to date. And ensure the parser's implementation matches the ABNF grammar.

## Definition of Done

After making changes to the codebase, verify the following and rectify any issues reported:

* All linting passes (`earth +lint`)

## Repository Layout

```text
<workspace>/
├── cmd/           # CLI commands
├── examples/      # Examples in different languages
└── www/           # Website
```

## Tooling

The primary development lifecycle tool is `earth`.

* `earth +lint` lints the project code quality.
* `earth +test` runs the tests.
* `earth +for-darwin-m1` builds the project for macOS (darwin-arm64).
* `earth doc` shows all other targets and a description of what they do.

## Diagnosing a CI failure

**Run it locally. Do not wait on GitHub Actions.** The suites CI runs are
Earthfile targets, so `earth` runs them on your machine. A local run answers in
minutes and can be re-asked with one variable changed; a CI round takes about an
hour behind `Fast Check & Build` and its queue.

A CI job named `+test-no-qemu-group4` is `BUILD ./tests+ga-no-qemu-group4`, which
is a list of `BUILD +<name>-test` lines in `tests/Earthfile`. Run one directly:

```bash
go build -o /tmp/earth ./cmd/earth
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/guestd ./cmd/earth-guestd

EARTH_GUESTD=/tmp/guestd /tmp/earth -P --engine=native ./tests+copy-tilde-test

EARTH_BUILDKIT_IMAGE=ghcr.io/earthbuild/earthbuild:buildkitd-v0.8.17-fix.1 \
  /tmp/earth -P --engine=buildkit ./tests+copy-tilde-test
```

Run both engines. A difference between them is the finding; a failure in one
alone says nothing until you know what the other does.

Four symptoms that each cost an hour before being understood:

| Symptom                                       | Cause                                                     |
| --------------------------------------------- | --------------------------------------------------------- |
| `unknown request <name>`                      | stale `earth-guestd`; rebuild it and set `$EARTH_GUESTD`  |
| `RUN --privileged ... refused by design`      | pass `-P`; the harness uses `RUN --privileged`            |
| `docker: manifest unknown` starting buildkitd | set `EARTH_BUILDKIT_IMAGE` to the ghcr image above        |
| a queued CI run vanishing                     | pushing cancels it; batch commits while awaiting a result |

Reading habits this branch has paid for:

* Read a job to **its own fatal error**, not its most frequent one. The frequent
  line is usually the harness reacting; the cause is upstream of it.
* A fix that removes an error message has not necessarily fixed anything.
  Compare which jobs *pass* before and after, by name.
* Filter job selectors by suite. Every suite has a `+test-no-qemu-group4`, so a
  substring match silently answers about a different one.
* `grep` is a hypothesis generator, not evidence: most messages here are built
  with a format verb, so the literal never appears in the source. Confirm by
  running the case.

## Guardrails

* Do not add golang dependencies unless asked by user explicitly.
