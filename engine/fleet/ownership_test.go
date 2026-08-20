package fleet_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A layer stores under its own digest on a worker that cannot chown.
//
// **The whole of E313 at the level it broke.** Two machines, two users: the
// driver packs a layer it owns, the worker unpacks it unprivileged, every chown
// is refused, and `Layers.Put` captured what landed on disk - a different layer.
// `Provision` then rejected it as "asked for X and got Y" and the worker
// reported that the driver did not hold what it had just sent.
//
// Same-user, same-OS runs pass with or without the fix, which is why every
// in-repo test was green through six experiments (E313). The seam is what makes
// the two-user case reachable from one.
func TestALayerStoresUnderItsOwnDigestWhenUnpackedAsAnotherUser(t *testing.T) {
	// Not parallel: the seam is a package variable in engine/layer.
	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644)
	if err != nil {
		t.Fatalf("%v", err)
	}

	want, err := layer.Take(src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var packed bytes.Buffer

	if err = layer.Pack(src, &packed); err != nil {
		t.Fatalf("%v", err)
	}

	// The unpack landed as somebody else, which is what an unprivileged
	// restore of another user's layer does.
	layer.ObservedOwnerForTest(t, func(uid, gid uint32) (uint32, uint32) {
		return uid + 1, gid + 1
	})

	store := &fleet.Layers{Root: t.TempDir()}

	got, _, err := store.Put(&packed)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if got != want.ID {
		t.Errorf("a layer sent as %v filed itself as %v"+
			"\n  a worker that cannot chown cannot share a base with anybody"+
			" (E313)", want.ID, got)
	}
}

// A layer passed on by a worker is still the same layer.
//
// **The second hop, and the half that a receiving-side fix alone leaves
// broken.** `Get` packs a layer on demand by walking it, so a worker that
// stored a layer it could not chown would declare *its own* ownership to the
// next machine - and that machine, doing exactly the right thing, would reject
// the result as not the layer it asked for.
//
// This is the mesh C.4 exists to allow: the machine that produced a layer is
// the closest copy of it, and a fleet where only the driver can serve a base is
// a star with the driver's uplink as its whole bandwidth.
func TestALayerPassedOnKeepsItsIdentity(t *testing.T) {
	// Not parallel: the seam is a package variable in engine/layer.
	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644)
	if err != nil {
		t.Fatalf("%v", err)
	}

	want, err := layer.Take(src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var packed bytes.Buffer

	if err = layer.Pack(src, &packed); err != nil {
		t.Fatalf("%v", err)
	}

	layer.ObservedOwnerForTest(t, func(uid, gid uint32) (uint32, uint32) {
		return uid + 1, gid + 1
	})

	first := &fleet.Layers{Root: t.TempDir()}

	id, _, err := first.Put(&packed)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// Now the worker serves it on, and a third machine files what arrives.
	body, err := first.Get(id)
	if err != nil {
		t.Fatalf("%v", err)
	}

	second := &fleet.Layers{Root: t.TempDir()}

	onward, _, err := second.Put(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%v", err)
	}

	if onward != want.ID {
		t.Errorf("a layer served on by a worker arrived as %v, want %v"+
			"\n  only the driver can seed a fleet if a layer changes identity"+
			" every time it is relayed (E313)", onward, want.ID)
	}
}

// A fragment of a relayed layer proves the layer it came from.
//
// The lazy path has the same fault as the whole one and needed saying
// separately, because the manifest is what makes a fragment checkable: it
// hashes ownership exactly as the layer digest does (§3.3), so a manifest taken
// off a relayed worker's disk authenticates a layer nobody asked for.
//
// It would fail *safe* - the fragment would be refused - which is why it can sit
// unnoticed behind a flag that is off by default (E314). It is still the
// difference between a lazy base that works between machines and one that never
// does.
func TestAFragmentOfARelayedLayerProvesTheOriginal(t *testing.T) {
	// Not parallel: the seam is a package variable in engine/layer.
	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644)
	if err != nil {
		t.Fatalf("%v", err)
	}

	want, err := layer.Manifest(src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var packed bytes.Buffer

	if err = layer.Pack(src, &packed); err != nil {
		t.Fatalf("%v", err)
	}

	layer.ObservedOwnerForTest(t, func(uid, gid uint32) (uint32, uint32) {
		return uid + 1, gid + 1
	})

	store := &fleet.Layers{Root: t.TempDir()}

	id, _, err := store.Put(&packed)
	if err != nil {
		t.Fatalf("%v", err)
	}

	got, _, err := store.Fragment(id, []string{"a.txt"})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if layer.ManifestID(got) != layer.ManifestID(want) {
		t.Errorf("a relayed layer's manifest is %v, want %v"+
			"\n  a fragment checked against it belongs to a layer nobody has"+
			" (E313)", layer.ManifestID(got), layer.ManifestID(want))
	}
}

// Two steps fetching the same layer at once both get it.
//
// **`directory not empty`, in a real run.** `Put` unpacks beside the store,
// checks whether the layer is already filed, and renames it into place. Between
// the check and the rename another goroutine can win, and the loser's rename
// fails against a directory that now exists - so a step that fetched its input
// perfectly well reports that it could not.
//
// *Failure class: TOCTOU on a check-then-act*, met in E323 on a channel close
// and here on a filesystem. It appeared the moment a driver began fetching
// concurrently (E347), which is to say the moment the mechanism that needed it
// started working.
func TestTwoStepsFetchingTheSameLayerBothGetIt(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644)
	if err != nil {
		t.Fatalf("%v", err)
	}

	want, err := layer.Take(src)
	if err != nil {
		t.Fatalf("%v", err)
	}

	packs := make([][]byte, 8)

	for i := range packs {
		var buf bytes.Buffer

		if err = layer.Pack(src, &buf); err != nil {
			t.Fatalf("%v", err)
		}

		packs[i] = buf.Bytes()
	}

	store := &fleet.Layers{Root: t.TempDir()}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		bad  []error
		open = make(chan struct{})
	)

	for i := range packs {
		wg.Add(1)

		go func() {
			defer wg.Done()

			<-open

			id, _, err := store.Put(bytes.NewReader(packs[i]))
			if err != nil {
				mu.Lock()
				bad = append(bad, err)
				mu.Unlock()

				return
			}

			if id != want.ID {
				mu.Lock()
				bad = append(bad, errWrongLayer)
				mu.Unlock()
			}
		}()
	}

	close(open)
	wg.Wait()

	if len(bad) > 0 {
		t.Errorf("%d of %d concurrent fetches of one layer failed: %v"+
			"\n  a step that got its input perfectly well reported that it"+
			" could not (E347)", len(bad), len(packs), bad[0])
	}
}

var errWrongLayer = errors.New("a layer stored under the wrong digest")
