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
- `vscode/` contains the VS Code extension, its TextMate grammar, and the
  language client that starts `earth lsp`.
- Future editor integrations should use their own sibling directories, such
  as `neovim/`.

## Structure grammars

An adapter provides document structure in whichever form its host accepts, and
nothing more:

- Tree-sitter hosts use `tree-sitter-earthfile/`. `TestTreeSitterParity`
  checks that every valid canonical fixture parses without error and keeps its
  target boundaries.
- VS Code has no Tree-sitter, so `vscode/` carries a TextMate grammar of the
  same shallow scope. `TestTextMateKeywordParity` pins its keyword list to the
  canonical command set.

Neither reproduces shell quoting or any other Earthfile semantics, and
adapters pin a reviewed revision of the in-repository Tree-sitter grammar.

Both grammars stop at comments, declaration boundaries, and command keywords.
Anything that needs to understand an Earthfile — diagnostics, hover,
navigation, completion, semantic highlighting, and the outline — belongs in
`internal/earthfile/analyzer` so that every editor gets it at once.

## Host defaults differ

Editors disagree about whether to prefer their own grammar or the language
server, and an adapter has to say so explicitly:

- VS Code consumes semantic tokens and `documentSymbol` as soon as the server
  advertises them.
- Zed prefers Tree-sitter for both. Its README asks for
  `"semantic_tokens": "combined"` and `"document_symbols": "on"`.

When adding an editor, check its defaults before concluding a core feature is
missing.

## Language conventions

- Language name: `Earthfile`
- LSP language ID: `earth`
- Recognized files: `Earthfile` and `*.earth`
- Language server command: `earth lsp`
- Server features: diagnostics, hover, definition, completion, document
  symbols, and semantic tokens

Keep these conventions consistent when adding an editor.
