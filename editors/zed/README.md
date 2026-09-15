# EarthBuild for Zed

This extension recognizes `Earthfile` and `*.earth` files and launches the
integrated EarthBuild language server. Its Tree-sitter grammar only provides
error-tolerant document structure; semantic highlighting, diagnostics, hover,
and navigation come from the canonical Go implementation behind `earth lsp`.

## Prerequisites

Build or install EarthBuild and ensure `earth` is available in the environment
Zed inherits:

~~~text
earth --version
~~~

The extension runs `earth lsp`; it does not download a second language-server
binary.

To use a particular build instead of the first `earth` in the project PATH,
configure its absolute path in Zed settings:

~~~json
{
  "lsp": {
    "earth-lsp": {
      "binary": {
        "path": "/absolute/path/to/earth"
      }
    }
  }
}
~~~

## Development installation

1. Open Zed's Extensions page.
2. Select **Install Dev Extension**.
3. Choose this `editors/zed` directory.
4. Open an Earthfile and verify that `EarthBuild Language Server` appears in
   the language-server status menu.

Zed compiles procedural extensions to WebAssembly. The Rust adapter contains
only the Zed host integration; language semantics stay in the shared Go
implementation.
