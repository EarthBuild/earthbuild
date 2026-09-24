package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A layer gets a short name that resolves to it.
//
// The farm is the whole reason a 41-layer stack mounts, so what matters is both
// halves: the name is short, and it leads to the layer. A shortening that lost
// the target would be a mount of the wrong filesystem, which is worse than a
// mount that fails.
func TestAShortNameResolvesToItsLayer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "layers", strings.Repeat("a", 64))

	err := os.MkdirAll(target, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(target, "marker"), []byte("here"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	farm := filepath.Join(dir, "l")
	got := link(farm, target, strings.Repeat("a", 64))

	if got == target {
		t.Fatalf("no short name was made: %s", got)
	}

	if len(got) >= len(target) {
		t.Errorf("the short name is not shorter: %s", got)
	}

	b, err := os.ReadFile(filepath.Join(got, "marker"))
	if err != nil {
		t.Fatalf("the short name does not lead to the layer: %v", err)
	}

	if string(b) != "here" {
		t.Errorf("the short name leads somewhere else: %q", string(b))
	}
}

// Asking twice is asking once.
//
// Layers are shared - a base image is under every target in a build - so this
// runs constantly, and from several mounts at the same moment. It must be
// idempotent and it must not race: Materialise is called concurrently, which is
// stated as an obligation on the executor and is just as true here.
func TestTheSameLayerAsksForTheSameName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	id := strings.Repeat("b", 64)
	target := filepath.Join(dir, "layers", id)

	err := os.MkdirAll(target, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	farm := filepath.Join(dir, "l")

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[string]int{}
	)

	for range 16 {
		wg.Go(func() {
			got := link(farm, target, id)

			mu.Lock()
			seen[got]++
			mu.Unlock()
		})
	}

	wg.Wait()

	if len(seen) != 1 {
		t.Errorf("concurrent callers got %d different answers: %v", len(seen), seen)
	}

	for name := range seen {
		if name == target {
			t.Error("a caller fell back to the long path, so the farm raced with itself")
		}
	}
}

// A short name already meaning another layer is not reused.
//
// 48 bits will not collide in a build, and "will not" is not a thing to mount a
// filesystem on. The fallback is the full path, which always works and is only
// slower to write down.
func TestAClashingShortNameIsNotReused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	farm := filepath.Join(dir, "l")

	first := filepath.Join(dir, "layers", "one")
	second := filepath.Join(dir, "layers", "two")

	for _, d := range []string{first, second} {
		err := os.MkdirAll(d, 0o750)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Both ids start the same way and are different layers.
	id := strings.Repeat("c", shortNameLen)

	got := link(farm, first, id+"1111")
	if got == first {
		t.Fatalf("the first layer got no short name: %s", got)
	}

	clash := link(farm, second, id+"2222")
	if clash != second {
		t.Errorf("a clashing short name was reused for a different layer: %s", clash)
	}
}

// An option string the kernel will not read whole is refused before the mount.
//
// The kernel's own answer is ENOENT with no path in it, arrived at by
// truncating the list and failing to find the half-a-directory at the end. That
// is a description of the symptom; this is the cause, and it says what to do.
func TestAnOverlongOptionStringIsRefusedFirst(t *testing.T) {
	t.Parallel()

	err := tooLong(strings.Repeat("x", maxMountOptions), 7, true)
	if err != nil {
		t.Errorf("options that fit were refused: %v", err)
	}

	err = tooLong(strings.Repeat("x", maxMountOptions+1), 41, true)
	if err == nil {
		t.Fatal("options too long for the kernel were passed to it anyway")
	}

	for _, want := range []string{"41 layers", "flatten", "not on how many layers"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%v", want, err)
		}
	}
}
