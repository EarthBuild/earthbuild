package layer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// TestASecretThatReachedALayerIsFound.
//
// **A secret is mounted outside the step's filesystem so it cannot be captured,
// and then the step copies it.** `RUN --secret TOKEN=... sh -c 'echo $TOKEN >
// /app/.env'` puts the credential in the delta, the delta becomes a layer, and
// the layer is cached, exported and possibly pushed. Nothing noticed.
//
// The engine holds the values while the step runs, so it can look. What it must
// never do is say what it found: the path and the secret's *name* are the
// report, and the value appears nowhere - not in the error, not in a log.
func TestASecretThatReachedALayerIsFound(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "app", "config"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	write := func(at, body string) {
		t.Helper()

		if werr := os.WriteFile(filepath.Join(root, at), []byte(body), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}

	write("app/harmless.txt", "nothing to see")
	write("app/config/.env", "API_TOKEN=hunter2-swordfish-battery\nDEBUG=1\n")

	found, err := layer.FindSecrets(root, []layer.Secret{
		{Name: "NOT_USED", Value: "a-value-that-is-absent"},
		{Name: "API_TOKEN", Value: "hunter2-swordfish-battery"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(found) != 1 {
		t.Fatalf("found %d leaks, want 1: %+v", len(found), found)
	}

	if found[0].Name != "API_TOKEN" {
		t.Errorf("blamed %q, want API_TOKEN", found[0].Name)
	}

	if got := filepath.ToSlash(found[0].Path); got != "app/config/.env" {
		t.Errorf("found it at %q, want app/config/.env", got)
	}

	// **The value must not travel with the finding.** A report that quotes the
	// secret has published it to every log the build writes to.
	if strings.Contains(found[0].String(), "hunter2") {
		t.Errorf("the finding quotes the secret: %s", found[0])
	}
}

// TestASecretIsFoundAcrossAReadBoundary.
//
// A file is scanned in chunks, and a credential does not agree to sit inside
// one. Split across two reads it is still in the layer, and a scanner that
// misses it reports a clean build - which is worse than not scanning, because
// somebody trusted it.
func TestASecretIsFoundAcrossAReadBoundary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	secret := "border-straddling-credential"

	// Padding chosen so the secret starts a few bytes before the end of the
	// first chunk, whatever the chunk is, by making the file span several.
	for _, pad := range []int{1 << 16, (1 << 16) - 7, (1 << 17) - 3} {
		body := strings.Repeat("x", pad) + secret + strings.Repeat("y", 100)

		at := filepath.Join(root, "f")
		if err := os.WriteFile(at, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}

		found, err := layer.FindSecrets(root, []layer.Secret{{Name: "S", Value: secret}})
		if err != nil {
			t.Fatal(err)
		}

		if len(found) != 1 {
			t.Errorf("a secret starting at offset %d was not found (%d results)"+
				"\n  a scanner that misses one reports a clean build, which is"+
				"\n  worse than not scanning", pad, len(found))
		}
	}
}

// A tree with nothing to hide costs nothing and reports nothing.
func TestACleanLayerHasNoFindings(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ordinary"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	// A symlink pointing at something unreadable must not stop the walk: a
	// layer is full of them and none has contents of its own.
	err = os.Symlink("/nowhere/at/all", filepath.Join(root, "dangling"))
	if err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	found, err := layer.FindSecrets(root, []layer.Secret{{Name: "S", Value: "absent"}})
	if err != nil {
		t.Fatalf("a dangling symlink stopped the scan: %v", err)
	}

	if len(found) != 0 {
		t.Errorf("found %d leaks in a clean tree", len(found))
	}
}
