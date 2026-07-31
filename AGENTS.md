# Conventions

## Naming

The project is being renamed from Earthly to EarthBuild. When writing new code,
comments, docs or commit messages:

* Use `earth` for the CLI / build tool (not `earthly`).
* Use `EarthBuild` for the project name (not `Earthly`).
* Leave existing `earthly` identifiers alone where they are literal: Earthfile
  target names (`+earthly-docker`), installed binary paths (`/usr/bin/earthly`),
  env var prefixes (`EARTHLY_*`), and published image/repo names. Those change
  only as part of the rename itself.

## Golang

* Use the concepts and capabilities of Go version declared in `go.mod`. Read more here: <https://go.dev/blog/go1.26>
* Prefer std packages over 3rd party packages, where possible.
* Ensure all exposed interfaces and types are documented.

## Earthfile Parser

* When making changes to the parser, it is critical to keep [earthfile.abnf](file:///Users/jhorsts/projects/earthbuild/earthbuild/internal/earthfile/earthfile.abnf) up to date. And ensure the parser's implementation matches the ABNF grammar.

## Definition of Done

After making changes to the codebase, verify the following and rectify any issues reported:

* All linting passes (`earth +lint`)

# Repository Layout

```
<workspace>/
├── cmd/           # CLI commands
├── examples/      # Examples in different languages
└── www/           # Website
```

# Tooling

The primary development lifecycle tool is `earth`.

* `earth +lint` lints the project code quality.
* `earth +test` runs the tests.
* `earth +for-darwin-m1` builds the project for macOS (darwin-arm64).
* `earth doc` shows all other targets and a description of what they do.

# Guardrails

* Do not add golang dependencies unless asked by user explicitly.