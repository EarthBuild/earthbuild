package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// layerWith makes a layer directory holding files and symlinks, and returns its
// name. Paths are absolute as a step would write them.
func layerWith(t *testing.T, dir, name string, files, links map[string]string) string {
	t.Helper()

	root := filepath.Join(dir, "layers", name)

	for p, body := range files {
		at := filepath.Join(root, p)

		err := os.MkdirAll(filepath.Dir(at), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(at, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	for p, target := range links {
		at := filepath.Join(root, p)

		err := os.MkdirAll(filepath.Dir(at), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.Symlink(target, at)
		if err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}
	}

	return name
}

// A symlink is resolved through the whole stack, not inside one layer.
//
// A layer stack is a filesystem and a symlink in it points into the *merged*
// view. `update-ca-certificates` makes `/etc/ssl/certs/ca-cert-...pem` a link
// into `/usr/local/share/ca-certificates/`, which an earlier `COPY` put in a
// different layer, and `tests/git-webserver+certs` then saves that path as an
// artifact. Following the link inside the layer that happens to hold *the link*
// finds whatever that one layer holds - which for an absolute target is almost
// never the answer, and here was nothing at all (E954).
//
// The layer directory in the message was the whole diagnosis:
//
//	COPY /etc/ssl/certs/ca-cert-...pem: stat
//	  <layers>/1b08875c.../usr/local/share/ca-certificates/...: no such file
func TestASymlinkIsResolvedThroughTheStack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	older := layerWith(t, dir, "older",
		map[string]string{"/usr/local/share/ca-certificates/x.crt": "the certificate\n"}, nil)
	newer := layerWith(t, dir, "newer", nil,
		map[string]string{"/etc/ssl/certs/x.pem": "/usr/local/share/ca-certificates/x.crt"})

	s := &Server{LayerDir: dir}

	from := []string{older, newer}

	found, err := s.findInStack(from, "/etc/ssl/certs/x.pem")
	if err != nil {
		t.Fatalf("the link itself is not in the stack: %v", err)
	}

	got, err := s.resolveLastInStack(from, found[len(found)-1])
	if err != nil {
		t.Fatalf("resolving a link into an older layer: %v", err)
	}

	body, err := os.ReadFile(got.path)
	if err != nil {
		t.Fatalf("the resolved path does not exist: %v", err)
	}

	if string(body) != "the certificate\n" {
		t.Errorf("resolved to %q holding %q", got.path, body)
	}
}

// The cases either side of it, so the rule is not "always look in the oldest".
func TestResolvingALinkThroughTheStackKeepsTheOrdinaryCases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Both in one layer: the link resolves without leaving it, which is what
	// every build did before an absolute link crossed a layer boundary.
	one := layerWith(t, dir, "one",
		map[string]string{"/a/real": "same layer\n"},
		map[string]string{"/a/link": "/a/real", "/a/rel": "real"})

	// A newer layer replacing the target: the newest wins, as a mount would.
	two := layerWith(t, dir, "two", map[string]string{"/a/real": "newer layer\n"}, nil)

	s := &Server{LayerDir: dir}

	for _, tc := range []struct{ name, link, want string }{
		{"an absolute link", "/a/link", "newer layer\n"},
		{"a relative link", "/a/rel", "newer layer\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			from := []string{one, two}

			found, err := s.findInStack(from, tc.link)
			if err != nil {
				t.Fatalf("finding the link: %v", err)
			}

			got, err := s.resolveLastInStack(from, found[len(found)-1])
			if err != nil {
				t.Fatalf("resolving: %v", err)
			}

			body, err := os.ReadFile(got.path)
			if err != nil {
				t.Fatalf("the resolved path does not exist: %v", err)
			}

			if string(body) != tc.want {
				t.Errorf("resolved to %q, want %q", body, tc.want)
			}
		})
	}

	// A link naming something no layer has is still an error, and the error
	// names the target rather than only the link - the message this whole
	// finding turned on.
	broken := layerWith(t, dir, "broken", nil, map[string]string{"/a/dangling": "/nowhere/at/all"})

	from := []string{broken}

	found, err := s.findInStack(from, "/a/dangling")
	if err != nil {
		t.Fatalf("finding the link: %v", err)
	}

	_, err = s.resolveLastInStack(from, found[len(found)-1])
	if err == nil {
		t.Fatal("a link naming nothing in the stack resolved")
	}

	if !strings.Contains(err.Error(), "/nowhere/at/all") {
		t.Errorf("the error does not name the target it could not find: %v", err)
	}
}
