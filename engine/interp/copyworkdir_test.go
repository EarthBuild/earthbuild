package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A relative COPY destination is resolved against the working directory.
//
// `WORKDIR /app` followed by `COPY . .` is the most common pair of lines in
// container builds. Ignoring the WORKDIR put the files at the filesystem root,
// and the symptom arrived two steps later as a RUN that could not find a file
// that had definitely been copied - a diagnosis pointing at the wrong line
// entirely.
//
// Resolved when the plan is made rather than inside the guest, because where a
// file lands is a static fact about the step and belongs in its identity: two
// COPYs of one file to two working directories are different operations and
// must not share a key.
func TestARelativeCopyDestinationFollowsTheWorkdir(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, source, want string }{
		{"a bare dot", `
main:
    FROM alpine:3.22
    WORKDIR /app
    COPY src.txt .
`, testWorkdir},
		{"a relative name", `
main:
    FROM alpine:3.22
    WORKDIR /app
    COPY src.txt config/
`, "/app/config"},
		{"an absolute destination is untouched", `
main:
    FROM alpine:3.22
    WORKDIR /app
    COPY src.txt /etc/
`, "/etc"},
		{"no workdir at all", `
main:
    FROM alpine:3.22
    COPY src.txt .
`, "."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+tc.source, testMain,
				interp.WithContext(ctxWith(t, map[string]string{testSourceFile: "hi"})))
			if err != nil {
				t.Fatal(err)
			}

			var dest string

			for _, n := range p.Graph.Nodes() {
				if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 {
					dest = n.Op.Args[1]
				}
			}

			if dest == "" {
				t.Fatalf("no copy in the plan:\n%s", describe(p.Graph.Nodes()))
			}

			if strings.TrimSuffix(dest, "/") != strings.TrimSuffix(tc.want, "/") {
				t.Errorf("the file lands at %q, want %q", dest, tc.want)
			}
		})
	}
}

// Two copies of one file to two working directories are different operations.
func TestTheWorkdirIsPartOfACopysIdentity(t *testing.T) {
	t.Parallel()

	key := func(workdir string) ir.NodeID {
		t.Helper()

		p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WORKDIR `+workdir+`
    COPY src.txt .
`, testMain, interp.WithContext(ctxWith(t, map[string]string{testSourceFile: "hi"})))
		if err != nil {
			t.Fatal(err)
		}

		for _, n := range p.Graph.Nodes() {
			if n.Op.Kind == ir.OpFile {
				return n.ID()
			}
		}

		t.Fatal("no copy in the plan")

		return ir.NodeID{}
	}

	if key(testWorkdir) == key("/srv") {
		t.Error("a copy into /app and a copy into /srv share a key")
	}
}

// `COPY x .` under a WORKDIR still means "into that directory".
//
// Resolving `.` against `/app` produced `/app`, and a destination with no
// trailing separator names a *file* - so the copy created /app as a regular
// file, and the next step's working directory could not be made: "mkdir /app:
// not a directory", two steps from the line that caused it.
//
// The trailing separator is not decoration. It is the difference between
// placing a file inside a directory and renaming it, and `.` carries that
// meaning without carrying the character.
func TestADotDestinationStaysADirectory(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ dest, want string }{
		{".", "/app/"},
		{"./", "/app/"},
		{"sub/", "/app/sub/"},
		{"named.txt", "/app/named.txt"},
	} {
		t.Run(tc.dest, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WORKDIR /app
    COPY src.txt `+tc.dest+`
`, testMain, interp.WithContext(ctxWith(t, map[string]string{testSourceFile: "hi"})))
			if err != nil {
				t.Fatal(err)
			}

			for _, n := range p.Graph.Nodes() {
				if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 {
					if n.Op.Args[1] != tc.want {
						t.Errorf("COPY src.txt %s lands at %q, want %q",
							tc.dest, n.Op.Args[1], tc.want)
					}

					return
				}
			}

			t.Error("no copy in the plan")
		})
	}
}
