# EarthBuild editor support

This directory contains thin editor adapters around the native language
server exposed by:

~~~text
earth lsp
~~~

The canonical lexer/parser and reusable language analysis are implemented in
Go under `internal/earthfile` and `internal/earthfile/analyzer`. Editor
adapters should launch the native server instead of reimplementing Earthfile
semantics.

## Layout

- `tree-sitter-earthfile/` contains a deliberately shallow, error-tolerant
  structural grammar shared by editors that require Tree-sitter.
- `zed/` contains the Zed extension, language configuration, and queries.
- Future editor integrations should use their own sibling directories, such
  as `vscode/` and `neovim/`.

Tree-sitter recognizes target and function boundaries, commands, comments,
and opaque argument lines. It deliberately does not reproduce shell quoting
or other Earthfile semantics. Detailed highlighting, diagnostics, hover, and
navigation come from `earth lsp`, backed by the canonical Go implementation.
A parity suite ensures every valid canonical parser fixture produces an
error-free structural tree without losing target boundaries.

Adapters pin a reviewed revision of the in-repository grammar. VS Code-style
TextMate consumers can migrate the existing
[`earthfile-grammar`](https://github.com/EarthBuild/earthfile-grammar)
assets into a future adapter here.

## Language conventions

- Language name: `Earthfile`
- LSP language ID: `earth`
- Recognized files: `Earthfile` and `*.earth`
- Language server command: `earth lsp`

Keep these conventions consistent when adding an editor.
