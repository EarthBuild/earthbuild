# EarthBuild for VS Code

This extension recognizes `Earthfile` and `*.earth` files and launches the
integrated EarthBuild language server. Diagnostics, hover, go-to-definition,
completion, semantic highlighting, and the outline all come from the canonical
Go implementation behind `earth lsp`.

The bundled TextMate grammar is deliberately shallow. It marks comments,
target and function boundaries, and command keywords, so an Earthfile still
reads sensibly before the server attaches. Everything finer than that arrives
as semantic tokens.

## Prerequisites

Build or install EarthBuild and make `earth` available:

~~~text
earth --version
~~~

The extension runs `earth lsp`; it does not download a second language-server
binary.

### If the server does not start on macOS

An application launched from Finder or the Dock does not inherit a login
shell's `PATH`, so `earth` can be missing from VS Code's environment even
though `earth --version` works in your terminal. Either launch VS Code from a
terminal with `code .`, or set an absolute path:

~~~json
{
  "earthbuild.lsp.path": "/absolute/path/to/earth"
}
~~~

## Settings

| Setting | Default | Purpose |
| --- | --- | --- |
| `earthbuild.lsp.path` | `""` | Absolute path to `earth`. Empty means search `PATH`. |
| `earthbuild.lsp.arguments` | `["lsp"]` | Arguments used to start the server. |
| `earthbuild.trace.server` | `"off"` | Log the traffic between VS Code and the server. |

Unlike Zed, VS Code needs no opt-in for semantic tokens or for the outline: it
consumes both as soon as the server advertises them.

The command **EarthBuild: Restart Language Server** restarts the server after
installing a new `earth` build.

## Development

~~~text
npm install
npm run compile
~~~

Then press <kbd>F5</kbd> in VS Code to open an Extension Development Host with
the extension loaded, and open an Earthfile in it.

`npm run package` type-checks, lints, bundles, and produces a `.vsix`. The
`+vscode-extension` Earthfile target runs the same steps in CI.

The grammar's keyword list is generated from the canonical command set in
`internal/earthfile`, and `TestTextMateKeywordParity` fails if the two drift
apart.
