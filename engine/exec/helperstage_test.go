package exec

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// storeSeeingSandbox shares exactly its store with the guest, as a real one does.
//
// `GuestPath` on the sandboxes that share rather than copy maps the store and
// the build directory and nothing else, so a path outside both is not visible -
// which is the whole of the bug this guards.
type storeSeeingSandbox struct {
	plainSandbox

	store string
}

func (s *storeSeeingSandbox) StoreDir() string { return s.store }

func (s *storeSeeingSandbox) GuestPath(host string) (string, bool) {
	rel, err := filepath.Rel(s.store, host)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return filepath.Join("/guest-store", rel), true
}

// A helper is staged where the guest can reach it.
//
// **The failure this had was invisible twice over.** The module was written to
// the host's own temporary directory, which a sharing sandbox does not map into
// the guest, so `placeBlob` refused it - correctly, and with a message naming
// the file. Nobody saw the message: `shareCaches` discards what the share hook
// returns, and the hook returned the error instead of reporting it, so every
// build with `CACHE --helper` shared nothing and said nothing.
//
// Asserted on reachability rather than on the literal directory, because where
// scratch belongs is a detail and "the guest can open it" is the requirement.
func TestAStagedHelperIsSomewhereTheGuestCanRead(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	st, err := blob.New(store)
	if err != nil {
		t.Fatal(err)
	}

	module := []byte("a module, as bytes")

	id, _, err := st.Put(strings.NewReader(string(module)))
	if err != nil {
		t.Fatal(err)
	}

	sb := &storeSeeingSandbox{store: store}
	e := &Executor{sb: sb, Mounts: filepath.Join(store, "mounts")}

	at, clean, err := e.stageHelper(context.Background(),
		ir.Mount{ID: "k", Portable: true, HelperID: id.String()})
	defer clean()

	if err != nil {
		t.Fatalf("the helper could not be handed to the guest: %v", err)
	}

	if at == "" {
		t.Fatal("the helper staged to nowhere, so the guest has no route to it")
	}

	if !strings.HasPrefix(at, "/guest-store/") {
		t.Errorf("the helper is at %q, which is not a path inside the guest\n"+
			"  a module the guest cannot open is a cache that shares nothing,"+
			" and the refusal is thrown away by shareCaches", at)
	}
}
