# EarthBuild for Zed

This extension recognizes `Earthfile` and `*.earth` files, provides
Tree-sitter syntax highlighting, and launches the integrated EarthBuild
language server.

## Prerequisites

Build or install EarthBuild and ensure `earth` is available in the environment
Zed inherits:

~~~text
earth --version
~~~

The extension runs `earth lsp`; it does not download a second language-server
binary.

## Development installation

1. Open Zed's Extensions page.
2. Select **Install Dev Extension**.
3. Choose this `editors/zed` directory.
4. Open an Earthfile and verify that `EarthBuild Language Server` appears in
   the language-server status menu.

Zed compiles procedural extensions to WebAssembly. The Rust adapter contains
only the Zed host integration; parsing, diagnostics, hover, and navigation stay
in the shared Go implementation.
