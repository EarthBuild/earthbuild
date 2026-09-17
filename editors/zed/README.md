# EarthBuild for Zed

This extension recognizes `Earthfile` and `*.earth` files and launches the
integrated EarthBuild language server. Its Tree-sitter grammar only provides
error-tolerant document structure; semantic highlighting, diagnostics, hover,
navigation, completion, and the outline come from the canonical Go
implementation behind `earth lsp`.

## Prerequisites

Build or install EarthBuild and ensure `earth` is available in the environment
Zed inherits:

~~~text
earth --version
~~~

The extension runs `earth lsp`; it does not download a second language-server
binary.

Zed prefers its own grammar over the language server by default: it disables
LSP semantic tokens, and it builds outlines and breadcrumbs from Tree-sitter
queries. This extension ships neither, because both come from the canonical Go
implementation instead. Point Zed at the language server for both in Zed
settings. To use a particular build instead of the first `earth` in the
project PATH, configure its absolute path there too:

~~~json
{
  "languages": {
    "Earthfile": {
      "semantic_tokens": "combined",
      "document_symbols": "on"
    }
  },
  "lsp": {
    "earth-lsp": {
      "binary": {
        "path": "/absolute/path/to/earth",
        "arguments": ["lsp"]
      }
    }
  }
}
~~~

Restart the language server after changing these settings.

## Development installation

1. Open Zed's Extensions page.
2. Select **Install Dev Extension**.
3. Choose this `editors/zed` directory.
4. Open an Earthfile and verify that `EarthBuild Language Server` appears in
   the language-server status menu.

Zed compiles procedural extensions to WebAssembly. The Rust adapter contains
only the Zed host integration; language semantics stay in the shared Go
implementation.
