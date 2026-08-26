package interp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `SAVE ARTIFACT main.o` saves the file the step just made, wherever it works.
//
// A relative path is relative to the working directory, exactly as it is for a
// RUN and for a COPY destination. `WORKDIR /code` then `SAVE ARTIFACT main.o`
// means /code/main.o - and taking it from the filesystem root instead produced
// "no such file" against a path the Earthfile never wrote, two targets away in
// whatever consumed the artifact.
func TestARelativeArtifactFollowsTheWorkdir(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, source, want string }{
		{"a relative artifact", `
build:
    FROM alpine:3.22
    WORKDIR /code
    RUN gcc -c main.cpp
    SAVE ARTIFACT main.o
`, testObject},
		{"an absolute artifact is untouched", `
build:
    FROM alpine:3.22
    WORKDIR /code
    SAVE ARTIFACT /etc/hostname
`, "/etc/hostname"},
		{"no workdir", `
build:
    FROM alpine:3.22
    SAVE ARTIFACT main.o
`, "/main.o"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+tc.source, "build")
			if err != nil {
				t.Fatal(err)
			}

			if len(p.Artifacts) == 0 {
				t.Fatal("nothing was saved")
			}

			if got := p.Artifacts[0].Path; got != tc.want {
				t.Errorf("the artifact is taken from %q, want %q", got, tc.want)
			}
		})
	}
}

// And the artifact a later target copies is the one that was saved.
func TestACopiedArtifactMatchesWhatWasSaved(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /code
    RUN gcc -c main.cpp
    SAVE ARTIFACT main.o

link:
    FROM alpine:3.22
    COPY +build/main.o .
`, "link")
	if err != nil {
		t.Fatal(err)
	}

	text := describe(p.Graph.Nodes())
	if !strings.Contains(text, "main.o") {
		t.Errorf("the copy is not in the graph:\n%s", text)
	}
}

// The path a copy reads is the one the producing target saved.
//
// `SAVE ARTIFACT main.o` under `WORKDIR /code` puts the file at /code/main.o
// and names it `main.o`; `COPY +build/main.o .` names it the same way. The
// consumer was taking the name for a path and reading /main.o - a file the
// Earthfile never mentions - and the failure landed in the *consuming* target,
// two steps from the line that decided it.
func TestACopyReadsWhereTheArtifactWasSaved(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /code
    RUN gcc -c main.cpp
    SAVE ARTIFACT main.o

link:
    FROM alpine:3.22
    COPY +build/main.o .
`, "link")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile || len(n.Op.Args) != 2 {
			continue
		}

		if got := n.Op.Args[0]; got != testObject {
			t.Errorf("the copy reads %q, want /code/main.o - where it was saved", got)
		}

		return
	}

	t.Errorf("no copy in the plan:\n%s", describe(p.Graph.Nodes()))
}

// `COPY +target/*` copies everything that target saved.
//
// The glob names artifacts, not paths in a filesystem: `+build/*` is "whatever
// build produced". Passing the `*` through to the guest asked it to stat a file
// literally called `*`, which no layer contains.
//
// Expanded here rather than in the guest, so each artifact is its own copy in
// the plan and the key covers exactly what was taken - a build whose producer
// starts saving a second artifact is a different build, and should look like
// one.
func TestAnArtifactGlobCopiesEverythingSaved(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /code
    RUN make
    SAVE ARTIFACT main.o
    SAVE ARTIFACT notes.txt

use:
    FROM alpine:3.22
    COPY +build/* /out/
`, "use")
	if err != nil {
		t.Fatal(err)
	}

	found := map[string]bool{}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 {
			found[n.Op.Args[0]] = true
		}
	}

	for _, want := range []string{testObject, "/code/notes.txt"} {
		if !found[want] {
			t.Errorf("%q was not copied; the plan has %v", want, found)
		}
	}

	if found["/*"] {
		t.Error("the glob reached the guest, which has no file called *")
	}
}

// A target that saved nothing says so rather than copying a literal star.
func TestAGlobOverNoArtifactsIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN make

use:
    FROM alpine:3.22
    COPY +build/* /out/
`, "use")
	if err == nil {
		t.Fatal("a glob over a target that saves nothing was accepted")
	}

	if !strings.Contains(err.Error(), "+build") {
		t.Errorf("the refusal does not name the target:\n%s", err)
	}
}

// `SAVE ARTIFACT <path> <name>` gives the artifact a name of its own.
//
// `SAVE ARTIFACT target/uberjar/app-*-standalone.jar app-standalone.jar` says:
// take whatever that pattern matches, and let everyone else call it
// app-standalone.jar. The version in the filename is decided by the build; the
// name is decided by the author, and the ENTRYPOINT two lines later uses the
// name.
func TestAnArtifactMayBeGivenAName(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /var/app
    RUN package
    SAVE ARTIFACT target/app-*-standalone.jar app-standalone.jar
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Artifacts) != 1 {
		t.Fatalf("%d artifacts, want 1", len(p.Artifacts))
	}

	a := p.Artifacts[0]
	if a.Path != "/var/app/target/app-*-standalone.jar" {
		t.Errorf("the artifact is taken from %q", a.Path)
	}

	if a.Name != "app-standalone.jar" {
		t.Errorf("the artifact is called %q, want app-standalone.jar", a.Name)
	}
}

// Without one, the artifact is called after the file it is.
func TestAnArtifactIsNamedAfterItsFile(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /code
    SAVE ARTIFACT main.o
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if p.Artifacts[0].Name != "main.o" {
		t.Errorf("the artifact is called %q, want main.o", p.Artifacts[0].Name)
	}
}

// A glob copies each artifact under the name it was given.
//
// `COPY +build/*` into a directory puts app-standalone.jar there, not
// app-1.4.2-standalone.jar - which is what the ENTRYPOINT on the next line
// expects, and the whole reason for naming it.
func TestAGlobCopiesArtifactsUnderTheirNames(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /var/app
    RUN package
    SAVE ARTIFACT target/app-*-standalone.jar app-standalone.jar

docker:
    FROM alpine:3.22
    COPY +build/* .
`, "docker")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile || len(n.Op.Args) != 2 {
			continue
		}

		if !strings.HasSuffix(n.Op.Args[1], "app-standalone.jar") {
			t.Errorf("the artifact lands at %q, not under the name it was given", n.Op.Args[1])
		}

		return
	}

	t.Errorf("no copy in the plan:\n%s", describe(p.Graph.Nodes()))
}

// A directory in the artifact namespace can be copied, not only a file.
//
// `SAVE ARTIFACT index.js /dist/index.js` puts the file at /js-example/index.js
// and calls it /dist/index.js. The name is a path in a namespace of the target's
// own making, and `COPY +build/dist` names the directory in it - which holds
// index.js and nothing else.
//
// Resolved here rather than in the guest for the reason a glob is: the name is
// not a path in any layer. Passed through, `/dist` was looked for in the
// producing target's filesystem, where nothing of that name exists, and the
// build failed with `COPY /dist: nothing in that target has it` - naming a
// directory the Earthfile does mention, in the consuming target, two steps from
// the line that decided it.
//
// `examples/tutorial/js/part2` is the case, and it is not exotic: saving into a
// directory and copying the directory is how every one of the js tutorials
// hands its output to the image that runs it.
func TestAnArtifactDirectoryCanBeCopied(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /js-example
    RUN bundle
    SAVE ARTIFACT index.js /dist/index.js

docker:
    FROM alpine:3.22
    COPY +build/dist dist
`, "docker")
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 && len(n.Sources) > 0 {
			got = append(got, n.Op.Args)
		}
	}

	if len(got) != 1 {
		t.Fatalf("want one copy out of the artifact directory, got %d:\n%s",
			len(got), describe(p.Graph.Nodes()))
	}

	// Read from where the file actually is, and land under the directory's
	// name: `dist/index.js`, which is what the ENTRYPOINT beside it says.
	if got[0][0] != "/js-example/index.js" {
		t.Errorf("the copy reads %q, want /js-example/index.js", got[0][0])
	}

	if got[0][1] != "dist/index.js" {
		t.Errorf("the copy writes %q, want dist/index.js", got[0][1])
	}
}

// An artifact can also be named in full, not only by the directory holding it.
//
// `SAVE ARTIFACT index.js /dist/index.js` calls the file /dist/index.js, so
// `COPY +build/dist/index.js` names it exactly. The lookup matched only on
// where the file *is* - /js-example/index.js - so the name the Earthfile chose
// resolved to nothing and was passed through as a path.
//
// The sibling of the directory case, and the same mistake: an artifact's name
// and its path are two different things, and only one of them is in a layer.
func TestAnArtifactCanBeCopiedByItsFullName(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /js-example
    RUN bundle
    SAVE ARTIFACT index.js /dist/index.js

docker:
    FROM alpine:3.22
    COPY +build/dist/index.js app.js
`, "docker")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile || len(n.Op.Args) != 2 || len(n.Sources) == 0 {
			continue
		}

		if got := n.Op.Args[0]; got != "/js-example/index.js" {
			t.Errorf("the copy reads %q, want /js-example/index.js - where the file is", got)
		}

		return
	}

	t.Errorf("no copy in the plan:\n%s", describe(p.Graph.Nodes()))
}

// TestAPartialPatternSelectsAmongTheArtifactsSaved.
//
// `+build/main.*` is the same mechanism as `+build/*` with a narrower pattern,
// and the corpus reaches for it far more often than for the bare star:
// `COPY ./wildcard/*+test/helloworld* .` globs the directory *and* the
// artifact, and twelve `wildcard-copy` targets turn on the second half.
//
// Matched against what the producer *declared*, not against a tree - the
// artifacts are known at plan time, so a pattern over them is resolved where
// the star already is, and each match is its own copy in the key.
func TestAPartialPatternSelectsAmongTheArtifactsSaved(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    WORKDIR /code
    RUN make
    SAVE ARTIFACT main.o
    SAVE ARTIFACT main.d
    SAVE ARTIFACT notes.txt

use:
    FROM alpine:3.22
    COPY +build/main.* /out/
`, "use")
	if err != nil {
		t.Fatal(err)
	}

	found := map[string]bool{}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 {
			found[n.Op.Args[0]] = true
		}
	}

	for _, want := range []string{testObject, "/code/main.d"} {
		if !found[want] {
			t.Errorf("%q was not copied; the plan has %v", want, found)
		}
	}

	// And the pattern selects: an artifact it does not match stays behind.
	if found["/code/notes.txt"] {
		t.Error("notes.txt was copied by `main.*`, so the pattern was ignored" +
			" and every artifact taken")
	}
}

// TestAPatternMatchingNothingIsToleratedByIfExists.
//
// A pattern that selects none of a target's artifacts is ordinarily the
// author's mistake and refused - but `--if-exists` is the author saying they
// know it may match nothing, and it means that for a pattern exactly as it
// means it for a path. `if-exists.earth+artifact-copy-not-exist-wildcard`
// copies `+save/*_ok` from a target saving `ok`, and asserts the file is
// absent afterwards.
func TestAPatternMatchingNothingIsToleratedByIfExists(t *testing.T) {
	t.Parallel()

	src := versioned + `
save:
    FROM alpine:3.22
    WORKDIR /code
    RUN touch ok
    SAVE ARTIFACT ok

use:
    FROM alpine:3.22
    COPY %s +save/*_ok /out/
`

	// Without the flag the mismatch is the finding, and it names what the
	// target does save - the author is choosing among names they wrote.
	_, err := interp.Build(fmt.Sprintf(src, ""), "use")
	if err == nil {
		t.Fatal("a pattern matching no artifact was accepted; it copies nothing," +
			" and the image is quietly missing whatever was meant")
	}

	if !strings.Contains(err.Error(), "ok") {
		t.Errorf("the refusal does not say what the target saves: %v", err)
	}

	// With it, the copy is dropped and the build carries on.
	p, err := interp.Build(fmt.Sprintf(src, "--if-exists"), "use")
	if err != nil {
		t.Fatalf("--if-exists did not tolerate a pattern matching nothing: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 &&
			strings.Contains(n.Op.Args[0], "_ok") {
			t.Errorf("the plan copies %q, which no artifact matches", n.Op.Args[0])
		}
	}
}
