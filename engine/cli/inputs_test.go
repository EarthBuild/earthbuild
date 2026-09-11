package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

const inputsEarthfile = `VERSION 0.8

build:
    FROM scratch
    COPY src.txt /
`

// emitInto plans the project and writes its input fingerprint.
func emitInto(t *testing.T, dir, at string) cli.Inputs {
	t.Helper()

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "build", Out: io.Discard, EmitInputs: at,
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	b, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}

	var got cli.Inputs

	err = json.Unmarshal(b, &got)
	if err != nil {
		t.Fatalf("the emitted file is not readable: %v", err)
	}

	return got
}

func checkAgainst(t *testing.T, dir, at string) error {
	t.Helper()

	return cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "build", Out: io.Discard, CheckInputs: at,
	})
}

// **What a build reads, written down.** A fingerprint over the whole plan, and
// beside it the context paths with the digest of what each held - so a reader
// told "changed" can be told which file.
func TestEmitInputsWritesAFingerprintAndWhatItCovers(t *testing.T) {
	t.Parallel()

	dir := project(t, inputsEarthfile, map[string]string{"src.txt": "one"})
	at := filepath.Join(t.TempDir(), "inputs.json")

	got := emitInto(t, dir, at)

	if got.Fingerprint == "" {
		t.Error("no fingerprint")
	}

	if got.Target != "build" {
		t.Errorf("target is %q", got.Target)
	}

	if len(got.Context) != 1 || got.Context[0].Path != "src.txt" {
		t.Fatalf("context is %v, want the one file the COPY reads", got.Context)
	}

	if got.Context[0].Digest == "" {
		t.Error("the context file has no digest")
	}
}

// Nothing changed, so the job need not run.
func TestCheckInputsPassesWhenNothingChanged(t *testing.T) {
	t.Parallel()

	dir := project(t, inputsEarthfile, map[string]string{"src.txt": "one"})
	at := filepath.Join(t.TempDir(), "inputs.json")

	emitInto(t, dir, at)

	err := checkAgainst(t, dir, at)
	if err != nil {
		t.Fatalf("an unchanged project reported %v", err)
	}
}

// **A changed context file is the case this exists for**, and the message has
// to name it: "something changed" sends a reader to the whole checkout.
func TestCheckInputsNamesTheContextFileThatChanged(t *testing.T) {
	t.Parallel()

	dir := project(t, inputsEarthfile, map[string]string{"src.txt": "one"})
	at := filepath.Join(t.TempDir(), "inputs.json")

	emitInto(t, dir, at)
	writeInto(t, dir, "src.txt", "two")

	err := checkAgainst(t, dir, at)
	if !errors.Is(err, cli.ErrInputsChanged) {
		t.Fatalf("an edited context file reported %v, want ErrInputsChanged", err)
	}

	if !strings.Contains(err.Error(), "src.txt") {
		t.Errorf("the message does not name the file that changed: %v", err)
	}
}

// The Earthfile is an input too. A context-only fingerprint goes green on an
// edited command, which is the failure that makes this worth having over a
// path filter.
func TestCheckInputsSeesAnEditedEarthfile(t *testing.T) {
	t.Parallel()

	dir := project(t, inputsEarthfile, map[string]string{"src.txt": "one"})
	at := filepath.Join(t.TempDir(), "inputs.json")

	emitInto(t, dir, at)
	writeInto(t, dir, testEarthfile, inputsEarthfile+"    COPY src.txt /again.txt\n")

	err := checkAgainst(t, dir, at)
	if !errors.Is(err, cli.ErrInputsChanged) {
		t.Fatalf("an edited Earthfile reported %v, want ErrInputsChanged", err)
	}
}

// **A build the engine knows it cannot key is never certified unchanged.**
// `--no-cache` says "run this whatever the cache holds"; a fingerprint that
// called such a build unchanged would be turning a green tick into a guess.
func TestABuildThatCannotBeKeyedIsNeverUnchanged(t *testing.T) {
	t.Parallel()

	dir := project(t, `VERSION 0.8

build:
    FROM scratch
    COPY src.txt /
    RUN --no-cache true
`, map[string]string{"src.txt": "one"})
	at := filepath.Join(t.TempDir(), "inputs.json")

	got := emitInto(t, dir, at)
	if len(got.Caveats) == 0 {
		t.Fatal("a --no-cache step produced no caveat")
	}

	err := checkAgainst(t, dir, at)
	if !errors.Is(err, cli.ErrInputsChanged) {
		t.Fatalf("a build with a caveat reported %v, want ErrInputsChanged", err)
	}

	if !strings.Contains(err.Error(), "no-cache") {
		t.Errorf("the message does not say why it cannot certify: %v", err)
	}
}

// The file is written deterministically: two emits of one project are the same
// bytes, or a CI cache never hits.
func TestEmitInputsIsByteIdentical(t *testing.T) {
	t.Parallel()

	dir := project(t, inputsEarthfile, map[string]string{"src.txt": "one"})
	tmp := t.TempDir()

	first, second := filepath.Join(tmp, "a.json"), filepath.Join(tmp, "b.json")

	emitInto(t, dir, first)
	emitInto(t, dir, second)

	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}

	if string(a) != string(b) {
		t.Error("two emits of one project differ")
	}
}

func writeInto(t *testing.T, dir, name, body string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}
