# Conventions

## Golang

* Use the concepts and capabilities of Go version declared in `go.mod`.
* Prefer std packages over 3rd party packages, where possible.
* Ensure all exposed interfaces and types are documented.

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