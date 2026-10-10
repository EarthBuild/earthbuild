package exec

import (
	"slices"
	"testing"
)

// `RUN --entrypoint` written in shell form is a shell command line.
//
// The arguments were handed to the image's entrypoint as an argv, on the
// reasoning that an entrypoint is a program and not a shell. The reference
// disagrees and its choice is the one the corpus is written against: shell form
// sets WithShell, the image's entrypoint is prepended, and the whole line is
// joined and given to `/bin/sh -c`.
//
// It matters where the line is a line. `tests/Earthfile` writes
//
//	RUN --privileged --entrypoint -- --no-output <remote-target> && ls /tmp/x
//
// and as an argv the `&&` and what follows become arguments to the entrypoint,
// which reported `invalid arguments <target> && ls /tmp/x` - the whole cause of
// the +test-no-qemu-group10 CI job (E941).
//
// Exec form is the control and must not be wrapped: `RUN --entrypoint ["a b"]`
// is an author saying these are the arguments, which is what exec form is for.
func TestAnEntrypointInShellFormIsAShellCommandLine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		entry    []string
		args     []string
		viaShell bool
		want     []string
	}{{
		name:     "shell form joins the entrypoint and the arguments",
		entry:    []string{"/usr/bin/earth-entrypoint.sh"},
		args:     []string{"--no-output", "+t", "&&", "ls", "/tmp/x"},
		viaShell: true,
		want:     []string{"/bin/sh", "-c", "/usr/bin/earth-entrypoint.sh --no-output +t && ls /tmp/x"},
	}, {
		name:     "exec form keeps the boundaries the author wrote",
		entry:    []string{"echo"},
		args:     []string{"hello world"},
		viaShell: false,
		want:     []string{"echo", "hello world"},
	}, {
		name:     "no arguments is the entrypoint alone",
		entry:    []string{"echo", "hello world"},
		viaShell: true,
		want:     []string{"/bin/sh", "-c", "echo hello world"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := entrypointArgv(tc.entry, tc.args, tc.viaShell)
			if !slices.Equal(got, tc.want) {
				t.Errorf("argv is %q, want %q", got, tc.want)
			}
		})
	}

	// The entrypoint is prepended, not replaced: an argv that lost it would run
	// the arguments as a command of their own.
	got := entrypointArgv([]string{"a"}, []string{"b"}, false)
	if len(got) != 2 || got[0] != "a" {
		t.Errorf("argv is %q, and the entrypoint is meant to come first", got)
	}
}
