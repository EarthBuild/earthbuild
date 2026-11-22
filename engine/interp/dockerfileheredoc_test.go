package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A Dockerfile `RUN <<EOT` runs the heredoc's body.
//
// The body arrives beside the command line, not in it, and only the line was
// translated: the shell was handed `<<EOT` alone, read an empty here-document,
// and the step succeeded having done nothing. buildkit's own Dockerfile builds
// runc this way, so every target standing on `buildkitd/Earthfile` failed one
// stage later with `COPY /made: nothing in that target has it`.
//
// The forms are buildkit's (dispatchRun): one heredoc that is the whole command
// is the script, `<<-` strips leading tabs, a `#!` body is run as a file, and
// anything else is reconstituted and given to the shell.
func TestADockerfileHeredocRunsItsBody(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, run, want string }{
		{"whole command", "RUN <<EOT\n  echo built > /made\nEOT\n", "echo built > /made"},
		{"tab stripped", "RUN <<-EOT\n\techo built > /made\n\tEOT\n", "echo built > /made\n"},
		{"inside a command", "RUN cat <<EOT > /made\nbuilt\nEOT\n", "cat <<EOT > /made\nbuilt\nEOT"},
		{"with a mount", "RUN --mount=type=cache,target=/c <<EOT\necho built > /made\nEOT\n", "echo built > /made"},
		{"shebang", "RUN <<EOT\n#!/bin/sh\necho built > /made\nEOT\n", "echo built > /made"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := withDockerfile(t, "Dockerfile", "# syntax=docker/dockerfile:1\nFROM alpine:3.22\n"+tc.run)

			p, err := interp.Build(versioned+"\nmain:\n    FROM DOCKERFILE .\n    RUN after\n",
				testMain, interp.WithContext(dir))
			if err != nil {
				t.Fatal(err)
			}

			for _, n := range p.Graph.Nodes() {
				if n.Op.Kind != ir.OpExec || n.Meta.Description == testAfter {
					continue
				}

				script := strings.Join(n.Op.Args, " ")
				if !strings.Contains(script, tc.want) {
					t.Errorf("the step runs %q, want it to run %q", n.Op.Args, tc.want)
				}

				return
			}

			t.Fatalf("no heredoc step in the graph:\n%s", describe(p.Graph.Nodes()))
		})
	}
}
