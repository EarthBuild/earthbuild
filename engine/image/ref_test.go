package image_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// Reference parsing has corner cases that a hand-rolled splitter gets wrong,
// and getting one wrong sends a pull to a registry the user never named.
//
// These are the cases the canonical parser (distribution/reference, already a
// dependency and used by the BuildKit path) handles and a `strings.Cut` does
// not.
func TestReferenceCornerCases(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, registry, repo, tag, digest string }{
		{"alpine", testRegistry, testRepoPath, "latest", ""},
		{"alpine:3.22", testRegistry, testRepoPath, "3.22", ""},
		{"myorg/tool:v1", testRegistry, "myorg/tool", "v1", ""},
		{"ghcr.io/org/tool:v2", "ghcr.io", "org/tool", "v2", ""},
		{"localhost:5000/x:1", "localhost:5000", "x", "1", ""},
		// A port on the host, and a colon in the tag, in one reference.
		{"registry.example.com:5000/ns/img:1.2", "registry.example.com:5000", "ns/img", "1.2", ""},
		// A digest instead of a tag.
		{
			"alpine@sha256:0000000000000000000000000000000000000000000000000000000000000000",
			testRegistry, testRepoPath, "",
			"sha256:0000000000000000000000000000000000000000000000000000000000000000",
		},
		// Both, which is legal and means "this digest, labelled thus".
		{
			"alpine:3.22@sha256:1111111111111111111111111111111111111111111111111111111111111111",
			testRegistry, testRepoPath, "3.22",
			"sha256:1111111111111111111111111111111111111111111111111111111111111111",
		},
		// A deep path, where a naive split on the first slash takes the wrong
		// piece as the registry.
		{"quay.io/a/b/c:t", "quay.io", "a/b/c", "t", ""},
	} {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			r, err := image.ParseRef(tc.in)
			if err != nil {
				t.Fatal(err)
			}

			if r.Registry != tc.registry {
				t.Errorf("registry is %q, want %q", r.Registry, tc.registry)
			}

			if r.Repository != tc.repo {
				t.Errorf("repository is %q, want %q", r.Repository, tc.repo)
			}

			if r.Tag != tc.tag {
				t.Errorf("tag is %q, want %q", r.Tag, tc.tag)
			}

			if r.Digest != tc.digest {
				t.Errorf("digest is %q, want %q", r.Digest, tc.digest)
			}
		})
	}
}

// An uppercase first component is a *domain*, not a repository.
//
// A path component must be lowercase, so an uppercase one can only be a host -
// and hostnames are case-insensitive. Written by hand this would have been
// rejected as malformed, refusing a legal reference; the canonical parser knows
// the rule, which is the argument for using it rather than the obvious one.
func TestUppercaseFirstComponentIsADomain(t *testing.T) {
	t.Parallel()

	r, err := image.ParseRef("MYHOST/name")
	if err != nil {
		t.Fatal(err)
	}

	if r.Registry != "MYHOST" || r.Repository != "name" {
		t.Errorf("parsed as registry %q repository %q", r.Registry, r.Repository)
	}
}

// Something that is not a reference is refused rather than silently becoming
// one.
func TestMalformedReferencesAreRefused(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "has spaces", ":justatag", "@sha256:zz"} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			_, err := image.ParseRef(in)
			if err == nil {
				t.Errorf("%q was accepted as a reference", in)
			}
		})
	}
}
