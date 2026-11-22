package helper_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/helper"
)

// The engine can run a helper and hear what it says.
//
// **The gap between a contract and a mechanism.** The five verbs were designed,
// prototyped and measured; nothing in the engine could invoke one. This is the
// smallest thing that closes it: compile a module, give it a cache directory and
// nothing else, ask it who it is.
func TestTheEngineCanAskAHelperWhoItIs(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	rt, err := helper.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = rt.Close(ctx) }()

	h, err := goModHelper(t, ctx, rt)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := h.Run(ctx, t.TempDir(), []string{"ident"}, nil, &out); err != nil {
		t.Fatalf("ask a helper its name: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "earthbuild/go-mod/1" {
		t.Errorf("a helper called itself %q", got)
	}
}

// A helper sees the cache it was given and nothing else.
//
// The confinement is the point rather than a side effect: a helper is somebody
// else's program, and the only thing it needs is the directory it manages.
func TestAHelperSeesOnlyItsCache(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	rt, err := helper.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = rt.Close(ctx) }()

	h, err := goModHelper(t, ctx, rt)
	if err != nil {
		t.Fatal(err)
	}

	// An empty directory is not a module cache, and the helper says so by
	// refusing rather than by inventing an answer.
	err = h.Run(ctx, t.TempDir(), []string{"probe"}, nil, discard{})
	if err == nil {
		t.Error("a helper probed an empty directory and claimed it")
	}
}

// A helper that understands the cache accepts it.
func TestAHelperProbesACacheItKnows(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cache", "download"), 0o750); err != nil {
		t.Fatal(err)
	}

	rt, err := helper.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = rt.Close(ctx) }()

	h, err := goModHelper(t, ctx, rt)
	if err != nil {
		t.Fatal(err)
	}

	if err := h.Run(ctx, root, []string{"probe"}, nil, discard{}); err != nil {
		t.Errorf("a helper refused a cache it understands: %v", err)
	}
}

// discard is a sink for a verb whose answer this test does not read.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// helperWasm builds the prototype helper as a module, once per test binary.
//
// Built rather than committed: a 4 MiB binary in the tree would be a second
// place for this to be wrong, and the Earthfile target that ships one is the
// answer for anybody who wants it without a Go toolchain.
func helperWasm(t *testing.T) []byte {
	t.Helper()

	at := filepath.Join(t.TempDir(), "helper.wasm")

	cmd := exec.Command("go", "build", "-o", at, "../../tools/cachehelper") //nolint:gosec // a fixed argv
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("no wasip1 toolchain here: %v\n%s", err, out)
	}

	b, err := os.ReadFile(at) //nolint:gosec // a path this test just wrote
	if err != nil {
		t.Fatal(err)
	}

	return b
}

// The engine can index a cache and export a unit from it, through the helper.
//
// **The whole delegation in one test.** The engine knows nothing here about
// module paths, `@v` directories or which files belong together - it asks, and
// receives a key it never parses and a framed unit it will name by ℋ. Every
// fact about Go's module cache stays inside the module.
func TestTheEngineCanIndexAndExportThroughAHelper(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	root := t.TempDir()
	at := filepath.Join(root, "cache", "download", "example.com", "m", "@v")

	if err := os.MkdirAll(at, 0o750); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"v1.2.3.info": `{"Version":"v1.2.3"}`,
		"v1.2.3.mod":  "module example.com/m\n",
		"v1.2.3.zip":  "not really a zip, but bytes are bytes",
	} {
		if err := os.WriteFile(filepath.Join(at, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rt, err := helper.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = rt.Close(ctx) }()

	h, err := goModHelper(t, ctx, rt)
	if err != nil {
		t.Fatal(err)
	}

	var index bytes.Buffer
	if err := h.Run(ctx, root, []string{"index"}, nil, &index); err != nil {
		t.Fatalf("index: %v", err)
	}

	key, _, _ := strings.Cut(strings.TrimSpace(index.String()), "\t")
	if key != "example.com/m@v1.2.3" {
		t.Fatalf("the helper named the unit %q", key)
	}

	var unit bytes.Buffer
	if err := h.Run(ctx, root, []string{"export"},
		strings.NewReader(key+"\n"), &unit); err != nil {
		t.Fatalf("export: %v", err)
	}

	// `<key> <length>\n` then the bytes, which is all the engine needs to know:
	// where one unit ends and the next begins, so each can be named by ℋ.
	head, _, found := bytes.Cut(unit.Bytes(), []byte("\n"))
	if !found {
		t.Fatal("the exported stream carries no frame header")
	}

	name, size, _ := strings.Cut(string(head), " ")
	if name != key {
		t.Errorf("the frame answers for %q, not %q", name, key)
	}

	if size == "" || size == "0" {
		t.Errorf("the unit is framed as %q bytes", size)
	}
}

// goModHelper compiles the prototype and points it at its go-mod format.
func goModHelper(t *testing.T, ctx context.Context, rt *helper.Runtime) (*helper.Helper, error) {
	t.Helper()

	h, err := rt.Compile(ctx, "cachehelper", helperWasm(t))
	if h != nil {
		h.Prefix = []string{"go-mod"}
	}

	return h, err
}
