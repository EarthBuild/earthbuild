//go:build linux

package exec

import (
	"path/filepath"
	"strings"
	"testing"
)

// Two sandboxes do not share a console.
//
// **A console shared is a console that lies.** It lived in the store
// directory, which is one fixed path for the machine, and `os.Create`
// truncates - so every guest a build starts wrote over the last one's account
// of itself. A run that started thirty-two guests in sequence kept one console,
// and quoting it in a failure attributes one guest's last words to another.
//
// Found by the diagnostic that reads it: the quoted console said
// "earth-vmboot: ready" for a guest that had just failed its handshake, which
// is either a very interesting bug or the wrong guest's console. It was the
// wrong guest's console.
func TestTwoSandboxesDoNotShareAConsole(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	one := &Firecracker{Root: filepath.Join(t.TempDir(), "one"), Store: store}
	two := &Firecracker{Root: filepath.Join(t.TempDir(), "two"), Store: store}

	first, err := one.consolePath()
	if err != nil {
		t.Fatal(err)
	}

	second, err := two.consolePath()
	if err != nil {
		t.Fatal(err)
	}

	if first == second {
		t.Fatalf("both sandboxes write their console to %s", first)
	}

	// Under the sandbox, not under the store: the store is one directory for
	// the machine and every guest on it would land in the same file again.
	if !strings.HasPrefix(first, one.Root) {
		t.Errorf("the console is not kept with its own sandbox: %s is not under %s", first, one.Root)
	}
}
