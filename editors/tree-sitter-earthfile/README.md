# Earthfile structural grammar

This Tree-sitter grammar is deliberately shallow. It recognizes target and
function declarations, command names, comments, and opaque physical-line
argument bodies. It does not parse shell quoting, substitutions, build
arguments, or other Earthfile semantics.

The canonical Go lexer and parser under `internal/earthfile` remain the source
of truth. The language server uses them for diagnostics, navigation, hover,
and semantic highlighting. Keeping command bodies opaque makes Tree-sitter
safe for incremental editor structure even when a line contains incomplete or
nested shell syntax.

Run `npm test` for grammar corpus tests. The repository parity test additionally
checks all valid canonical parser fixtures for Tree-sitter errors and target
boundary mismatches.
