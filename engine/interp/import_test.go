package interp_test

import (
	"strings"
	"testing"
)

// IMPORT gives another Earthfile a name, and `name+target` uses it.
func TestImportAlias(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + `
IMPORT ./lib AS mylib

main:
    FROM alpine:3.22
    BUILD mylib+build
`,
		testLibEarthfile: versioned + "\nbuild:\n    FROM alpine:3.22\n    RUN lib-step\n",
	})

	p, err := buildIn(t, root, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "lib-step") {
		t.Errorf("the imported target was not built:\n%s", got)
	}
}

// Without AS, the alias is the last element of the path - which is how every
// example in the documentation is written.
func TestImportDefaultsToTheLastPathElement(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + `
IMPORT ./tools

main:
    FROM alpine:3.22
    BUILD tools+build
`,
		"tools/Earthfile": versioned + "\nbuild:\n    FROM alpine:3.22\n    RUN tools-step\n",
	})

	p, err := buildIn(t, root, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "tools-step") {
		t.Errorf("the imported target was not built:\n%s", got)
	}
}

// `IMPORT .. AS tests` is the commonest form in this repository: a directory
// naming its parent.
func TestImportOfTheParentDirectory(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + "\nshared:\n    FROM alpine:3.22\n    RUN parent-step\n",
		"sub/Earthfile": versioned + `
IMPORT .. AS up

main:
    FROM alpine:3.22
    BUILD up+shared
`,
	})

	p, err := buildIn(t, root+"/sub", testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "parent-step") {
		t.Errorf("the parent's target was not built:\n%s", got)
	}
}

// An import is visible to every target in the file, because it is declared at
// the top of it.
func TestImportsAreVisibleToEveryTarget(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + `
IMPORT ./lib AS mylib

first:
    FROM alpine:3.22
    BUILD mylib+build

second:
    FROM mylib+build
    RUN second-step
`,
		testLibEarthfile: versioned + "\nbuild:\n    FROM alpine:3.22\n    RUN lib-step\n",
	})

	for _, target := range []string{"first", "second"} {
		p, err := buildIn(t, root, target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}

		if got := describe(p.Graph.Nodes()); !strings.Contains(got, "lib-step") {
			t.Errorf("%s did not see the import:\n%s", target, got)
		}
	}
}

// A name that was never imported is not silently treated as a directory.
//
// `mylib+build` with no IMPORT is a typo or a missing line, and reading it as a
// relative path produces "no Earthfile in ./mylib" - which is true, and unhelpful.
func TestAnUnimportedNameIsNamedAsSuch(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + "\nmain:\n    FROM alpine\n    BUILD mylib+build\n",
	})

	_, err := buildIn(t, root, testMain)
	if err == nil {
		t.Fatal("a reference to an unimported name was accepted")
	}

	if !strings.Contains(err.Error(), "mylib") || !strings.Contains(err.Error(), "IMPORT") {
		t.Errorf("the error does not say the name was never imported:\n%s", err)
	}
}

// A remote import needs a checkout, and is refused where it is written rather
// than where it is used.
func TestRemoteImportsAreRefused(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{
		testEarthfile: versioned + `
IMPORT github.com/org/lib:1.0 AS lib

main:
    FROM alpine:3.22
    BUILD lib+build
`,
	})

	_, err := buildIn(t, root, testMain)
	if err == nil {
		t.Fatal("a remote import was accepted")
	}

	if !strings.Contains(err.Error(), "github.com/org/lib") {
		t.Errorf("the refusal does not quote the reference:\n%s", err)
	}
}
