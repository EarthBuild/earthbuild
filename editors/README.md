# EarthBuild editor support

This directory contains thin editor adapters around the native language
server exposed by:

~~~text
earth lsp
~~~

The reusable language analysis is implemented in Go under
`internal/earthfile/analyzer`. Editor adapters should launch the native
server instead of reimplementing Earthfile semantics.

## Layout

- `zed/` contains the Zed extension, language configuration, and Tree-sitter
  queries.
- Future editor integrations should use their own sibling directories, such
  as `vscode/` and `neovim/`.

Tree-sitter consumers share
[`tree-sitter-earthfile`](https://github.com/glehmann/tree-sitter-earthfile).
The grammar revision is pinned by each adapter so updates can be tested and
reviewed independently. VS Code-style TextMate consumers can migrate the
existing
[`earthfile-grammar`](https://github.com/EarthBuild/earthfile-grammar)
assets into a future adapter here.

## Language conventions

- Language name: `Earthfile`
- LSP language ID: `earth`
- Recognized files: `Earthfile` and `*.earth`
- Language server command: `earth lsp`

Keep these conventions consistent when adding an editor.
