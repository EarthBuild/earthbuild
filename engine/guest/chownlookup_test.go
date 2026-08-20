package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `--chown` names are resolved against the *destination image*, not this
// machine.
//
// `COPY --chown=testuser:testgroup` means the user that image has. Resolving it
// here would give whatever the guest's own passwd file says - a different
// machine, usually a different id, and a file in the produced image owned by
// somebody who does not exist in it. A3 says a step cannot reach the guest's
// filesystem, and neither may a lookup made on its behalf.
func TestChownNamesResolveAgainstTheImage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(name, body string) {
		t.Helper()

		if err := os.WriteFile(filepath.Join(root, "etc", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("passwd", "root:x:0:0:root:/root:/bin/sh\ntestuser:x:1234:5678::/home/t:/bin/sh\n")
	write("group", "root:x:0:\ntestgroup:x:5678:\n")

	for _, tc := range []struct {
		name     string
		spec     string
		uid, gid int
	}{
		{"both names", "testuser:testgroup", 1234, 5678},
		{"both numbers", "1000:1001", 1000, 1001},
		{"a name and a number", "testuser:1001", 1234, 1001},
		// No group: the user's own, which is what chown(1) does and what an
		// author writing `--chown=testuser` means.
		{"a user alone", "testuser", 1234, 5678},
		{"a number alone", "1000", 1000, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uid, gid, err := chownIDs(root, tc.spec)
			if err != nil {
				t.Fatalf("%v", err)
			}

			if uid != tc.uid || gid != tc.gid {
				t.Errorf("%q resolved to %d:%d, want %d:%d", tc.spec, uid, gid, tc.uid, tc.gid)
			}
		})
	}
}

// A name the image does not have is refused, naming the file that was read.
//
// The alternative is a copy that silently lands as root, and an image whose
// files belong to somebody the author did not choose. Which file was consulted
// matters, because the answer is usually "the base image does not have that
// user" and that is not obvious from the Earthfile.
func TestAChownNameTheImageLacksIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "etc", "passwd"),
		[]byte("root:x:0:0:root:/root:/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := chownIDs(root, "nobody-here:nobody-here")
	if err == nil {
		t.Fatal("a user the image does not have was accepted")
	}

	for _, want := range []string{"nobody-here", "/etc/passwd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}
