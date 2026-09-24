package exec

import "strings"

// entrypointArgv is what a `RUN --entrypoint` runs, once the image's entrypoint
// is known.
//
// **Shell form is a command line, not an argv**, and the reference says so: its
// `withShell` is `!ExecMode` and `--entrypoint` does not override it, so the
// entrypoint is prepended and the whole thing is handed to `/bin/sh -c`.
//
// It matters where the line is a line. `tests/Earthfile` writes
// `-- --no-output <target> && ls /tmp/x`, and as an argv the `&&` and what
// follows are arguments to the entrypoint - which reported
// `invalid arguments <target> && ls /tmp/x` and failed a whole CI job (E941).
//
// Exec form keeps its boundaries: an author who wrote a list meant a list, which
// is what exec form is for.
func entrypointArgv(entry, args []string, viaShell bool) []string {
	out := append(append([]string{}, entry...), args...)

	if !viaShell {
		return out
	}

	return []string{"/bin/sh", "-c", strings.Join(out, " ")}
}
