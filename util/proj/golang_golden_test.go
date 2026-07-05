package proj_test

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/util/proj"
	"github.com/google/go-cmp/cmp"
)

//go:embed testdata/golang_base.out
var goOutBase string

//go:embed testdata/golang_named.out
var goOutNamed string

const version = "VERSION --arg-scope-and-set 0.7\n\n"

var update = flag.Bool("update", false, "Update the testdata for golden tests")

func saveGoldenFile(t *testing.T, path string, b []byte) {
	t.Helper()

	if !*update {
		return
	}

	// Golden files are tracked in version control and need to be readable by other processes/CI, so 0644 is appropriate.
	// #nosec G306
	err := os.WriteFile(path, b, 0o644)
	if err != nil {
		t.Fatalf("write golden file: %v", err)
	}

	t.Skip("golden file updated")
}

func TestGolang_Targets_Base(t *testing.T) {
	t.Parallel()

	buf := bytes.NewBufferString(version)
	g := proj.NewGolang(proj.StdFS(), proj.StdExecer())

	tgts, err := g.Targets()
	if err != nil {
		t.Fatalf("failed to load golang targets: %v", err)
	}

	for i, tgt := range tgts {
		tgt.SetPrefix("")

		if i > 0 {
			buf.WriteString("\n")
		}

		err := tgt.Format(buf, "    ")
		if err != nil {
			t.Fatalf("failed to format code: %v", err)
		}
	}

	got := buf.String()

	saveGoldenFile(t, "./testdata/golang_base.out", []byte(got))

	diff := cmp.Diff(goOutBase, got)
	if diff != "" {
		t.Fatal(diff)
	}
}

func TestGolang_Targets_Named(t *testing.T) {
	t.Parallel()

	buf := bytes.NewBufferString(version)
	g := proj.NewGolang(proj.StdFS(), proj.StdExecer())

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	pfx := g.Type(ctx)

	tgts, err := g.Targets()
	if err != nil {
		t.Fatalf("failed to load golang targets: %v", err)
	}

	for i, tgt := range tgts {
		tgt.SetPrefix(pfx)

		if i > 0 {
			buf.WriteString("\n")
		}

		err := tgt.Format(buf, "    ")
		if err != nil {
			t.Fatalf("failed to format code: %v", err)
		}
	}

	got := buf.String()

	saveGoldenFile(t, "./testdata/golang_named.out", []byte(got))

	if diff := cmp.Diff(goOutNamed, got); diff != "" {
		t.Fatal(diff)
	}
}
