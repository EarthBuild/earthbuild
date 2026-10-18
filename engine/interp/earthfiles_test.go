package interp_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// **A build says which Earthfiles it read.**
//
// Told rather than worked out: the interpreter loads each one and keeps them by
// directory already, so the set is free. Anything that needs to know what a
// build depends on - a job-level skip key, which must move when any of them
// changes - would otherwise have to follow references itself, expanding
// arguments to resolve names, which is a second interpreter.
func TestAPlanSaysWhichEarthfilesItRead(t *testing.T) {
	t.Parallel()

	// Resolved, because the interpreter resolves it: on macOS /var is a symlink
	// to /private/var and the paths would differ for that alone.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	write := func(dir, body string) {
		t.Helper()

		made := os.MkdirAll(filepath.Join(root, dir), 0o750)
		if made != nil {
			t.Fatal(made)
		}

		wrote := os.WriteFile(filepath.Join(root, dir, "Earthfile"), []byte(body), 0o600)
		if wrote != nil {
			t.Fatal(wrote)
		}
	}

	write(".", "VERSION 0.8\n\nbuild:\n    FROM scratch\n    COPY ./sub+thing/x /\n")
	write("sub", "VERSION 0.8\n\nthing:\n    FROM scratch\n    RUN true\n    SAVE ARTIFACT /x\n")
	write("unused", "VERSION 0.8\n\nother:\n    FROM scratch\n")

	src, err := os.ReadFile(filepath.Join(root, "Earthfile"))
	if err != nil {
		t.Fatal(err)
	}

	plan, err := interp.Build(string(src), "build", interp.WithContext(root))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	got := plan.Earthfiles()

	for _, want := range []string{
		filepath.Join(root, "Earthfile"),
		filepath.Join(root, "sub", "Earthfile"),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is not among the Earthfiles the plan read: %v", want, got)
		}
	}

	// An Earthfile nothing referenced is not an input, or every file in a
	// monorepo would rebuild every target in it.
	if slices.Contains(got, filepath.Join(root, "unused", "Earthfile")) {
		t.Error("an Earthfile nothing referenced is named as read")
	}

	// Sorted and unique, because this reaches a digest.
	if !slices.IsSorted(got) {
		t.Errorf("not sorted: %v", got)
	}
}

// A build over one file names that one.
func TestAPlanWithOneEarthfileSaysSo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := interp.Build("VERSION 0.8\n\nbuild:\n    FROM scratch\n    COPY src.txt /\n",
		"build", interp.WithContext(dir))
	if err != nil {
		t.Fatal(err)
	}

	if got := plan.Earthfiles(); len(got) != 1 {
		t.Errorf("a one-file build read %v", got)
	}
}
