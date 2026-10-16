package interp_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// fetcher stands in for checking a repository out.
//
// The seam is the point: what a remote reference *means* - which repository,
// which revision, which directory inside it, and how many times it is fetched -
// is decided by the interpreter and is worth testing on every change. Whether
// git can reach github is not, and testing the two together would mean testing
// neither.
type fetcher struct {
	calls [][2]string
	dir   string
	err   error
}

func (f *fetcher) fetch(repo, ref string) (string, error) {
	f.calls = append(f.calls, [2]string{repo, ref})

	return f.dir, f.err
}

// remoteRepo is a checkout with one target in it.
func remoteRepo(t *testing.T, at string) *fetcher {
	t.Helper()

	files := map[string]string{
		at + testEarthfile: versioned + "\nbuild:\n    FROM alpine:3.22\n    RUN from-the-remote\n",
	}

	return &fetcher{dir: ctxWith(t, files)}
}

func TestARemoteReferenceIsFetchedAndBuilt(t *testing.T) {
	t.Parallel()

	f := remoteRepo(t, "")

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM github.com/org/repo+build\n    RUN local-step\n",
		testMain, interp.WithRemotes(f.fetch))
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "from-the-remote") {
		t.Errorf("the remote target's steps are not in the graph:\n%s", got)
	}

	if len(f.calls) != 1 || f.calls[0] != [2]string{testRepo, ""} {
		t.Errorf("fetched %v, want github.com/org/repo at its default revision", f.calls)
	}
}

// `github.com/org/repo:<rev>+target` pins the revision, and the revision is
// most of the point: an unpinned remote build is not reproducible, and the
// engine must pass through what was written rather than resolve it to
// something else.
func TestARemoteReferenceCarriesItsRevision(t *testing.T) {
	t.Parallel()

	for _, rev := range []string{"v1.2.3", testMain, "51fe8fb974fd27cac120487c04948bd3295683c9"} {
		t.Run(rev, func(t *testing.T) {
			t.Parallel()

			f := remoteRepo(t, "")

			_, err := interp.Build(versioned+
				"\nmain:\n    FROM github.com/org/repo:"+rev+"+build\n",
				testMain, interp.WithRemotes(f.fetch))
			if err != nil {
				t.Fatal(err)
			}

			if len(f.calls) != 1 || f.calls[0] != [2]string{testRepo, rev} {
				t.Errorf("fetched %v, want the revision as written", f.calls)
			}
		})
	}
}

// A path past the repository names a directory inside the checkout, not a
// different repository.
func TestARemoteReferenceCanNameASubdirectory(t *testing.T) {
	t.Parallel()

	f := remoteRepo(t, "sub/dir/")

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM github.com/org/repo/sub/dir+build\n",
		testMain, interp.WithRemotes(f.fetch))
	if err != nil {
		t.Fatal(err)
	}

	if len(f.calls) != 1 || f.calls[0][0] != testRepo {
		t.Errorf("fetched %v, want the repository rather than the path within it", f.calls)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "from-the-remote") {
		t.Errorf("the Earthfile in the subdirectory was not used:\n%s", got)
	}
}

// A repository named twice is fetched once.
//
// A fetch is a clone: doing it per reference turns a file that mentions a
// dependency three times into three clones of it, which is the difference
// between a build tool and a shell loop.
func TestARepositoryIsFetchedOnce(t *testing.T) {
	t.Parallel()

	f := remoteRepo(t, "")

	_, err := interp.Build(versioned+`
main:
    FROM github.com/org/repo+build
    BUILD github.com/org/repo+build
`, testMain, interp.WithRemotes(f.fetch))
	if err != nil {
		t.Fatal(err)
	}

	if len(f.calls) != 1 {
		t.Errorf("the repository was fetched %d times, want 1: %v", len(f.calls), f.calls)
	}
}

// Two revisions of one repository are two checkouts.
//
// They are different code, and collapsing them to one fetch would build one
// revision while reporting the other - which is worse than fetching twice.
func TestTwoRevisionsAreTwoFetches(t *testing.T) {
	t.Parallel()

	f := remoteRepo(t, "")

	_, err := interp.Build(versioned+`
main:
    FROM github.com/org/repo:v1+build
    BUILD github.com/org/repo:v2+build
`, testMain, interp.WithRemotes(f.fetch))
	if err != nil {
		t.Fatal(err)
	}

	if len(f.calls) != 2 {
		t.Errorf("fetched %d times, want one per revision: %v", len(f.calls), f.calls)
	}
}

// Without a fetcher, a remote reference is refused by name.
//
// This is the plan-only path, and it must not clone a repository to answer a
// question about a graph.
func TestWithoutAFetcherARemoteReferenceIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nmain:\n    FROM github.com/org/repo+build\n", testMain)
	if err == nil {
		t.Fatal("a remote reference was accepted with no way to fetch it")
	}

	for _, want := range []string{"github.com/org/repo+build", "remote repository"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// A fetch that fails says which reference could not be fetched.
func TestAFailingFetchNamesTheReference(t *testing.T) {
	t.Parallel()

	f := &fetcher{err: errors.New("no such host")}

	_, err := interp.Build(versioned+"\nmain:\n    FROM github.com/org/repo:v9+build\n",
		testMain, interp.WithRemotes(f.fetch))
	if err == nil {
		t.Fatal("a reference that could not be fetched was accepted")
	}

	for _, want := range []string{testRepo, "v9", "no such host"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%s", want, err)
		}
	}
}

// A reference that tries to escape the checkout cache is refused.
//
// The repository and revision come from an Earthfile, and an Earthfile is
// untrusted input - it may itself have arrived from a repository this build
// just cloned. They are used to build a path that gets removed and recreated,
// so a `..` in either is a delete outside the cache, and a leading `-` is an
// argument to git rather than a revision.
func TestAReferenceCannotEscapeTheCache(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"github.com/../../etc+build",
		"github.com/org/../../../tmp+build",
		"github.com/org/repo:../../../etc+build",
		"github.com/org/repo:../escape+build",
		// No space: with one, the line tokenises as an image name long before it
		// is a reference, so this is the shape an attack would actually take.
		"github.com/org/repo:--upload-pack=id+build",
		"github.com/org/repo:-x+build",
		"github.com/org/repo:a/b+build",
		"github.com/./org/repo+build",
	} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			f := remoteRepo(t, "")

			_, err := interp.Build(versioned+"\nmain:\n    FROM "+ref+"\n",
				testMain, interp.WithRemotes(f.fetch))
			if err == nil {
				t.Fatalf("%q was accepted", ref)
			}

			if len(f.calls) != 0 {
				t.Errorf("it reached the fetcher as %v", f.calls)
			}
		})
	}
}

// A fetched Earthfile is confined to its own checkout.
//
// `FROM ../../..+target` is legitimate in an Earthfile on this machine - the
// corpus is full of it, and a developer's own repository may sprawl over
// several directories. In an Earthfile that arrived from somewhere else it is
// something quite different: the checkout lives under the build cache, so
// climbing out of it reaches the host's filesystem, and the remote repository
// gets to name any Earthfile on this machine and have it built.
//
// The rule is therefore about provenance rather than about the path: a unit
// that came from a remote checkout may only refer within it.
func TestAFetchedEarthfileCannotClimbOutOfItsCheckout(t *testing.T) {
	t.Parallel()

	f := &fetcher{dir: ctxWith(t, map[string]string{
		"repo/Earthfile": versioned +
			"\nbuild:\n    FROM ../../../../..+anything\n",
		// Reachable only by climbing out of the checkout.
		testEarthfile: versioned + "\nanything:\n    FROM alpine:3.22\n    RUN host-side\n",
	})}
	f.dir = filepath.Join(f.dir, "repo")

	_, err := interp.Build(versioned+"\nmain:\n    FROM github.com/org/repo+build\n",
		testMain, interp.WithRemotes(f.fetch))
	if err == nil {
		t.Fatal("a fetched Earthfile referred to one outside its own checkout")
	}

	if !strings.Contains(err.Error(), "checkout") {
		t.Errorf("the refusal does not say what is wrong:\n%s", err)
	}
}

// Within its own checkout, a fetched Earthfile refers freely.
func TestAFetchedEarthfileRefersWithinItsCheckout(t *testing.T) {
	t.Parallel()

	f := &fetcher{dir: ctxWith(t, map[string]string{
		testEarthfile:     versioned + "\nbuild:\n    FROM ./inner+thing\n",
		"inner/Earthfile": versioned + "\nthing:\n    FROM alpine:3.22\n    RUN from-inner\n",
	})}

	p, err := interp.Build(versioned+"\nmain:\n    FROM github.com/org/repo+build\n",
		testMain, interp.WithRemotes(f.fetch))
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "from-inner") {
		t.Errorf("a reference within the checkout was not followed:\n%s", got)
	}
}

// `IMPORT github.com/org/repo AS lib` names a repository, and `lib+target`
// then builds in it.
//
// The machinery to fetch a repository already existed; IMPORT was still
// refusing at the line that declares the alias. An import is only a name for a
// reference, so it should mean whatever writing the reference out in full
// means.
func TestARemoteImportResolves(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		decl string
		want [2]string
	}{
		{"unpinned", "IMPORT github.com/org/repo AS lib", [2]string{testRepo, ""}},
		{"pinned", "IMPORT github.com/org/repo:v2 AS lib", [2]string{testRepo, "v2"}},
		{"named by its last element", "IMPORT github.com/org/repo", [2]string{testRepo, ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := remoteRepo(t, "")

			alias := "lib"
			if !strings.Contains(tc.decl, " AS ") {
				alias = "repo"
			}

			p, err := interp.Build(versioned+"\n"+tc.decl+
				"\n\nmain:\n    FROM "+alias+"+build\n", testMain, interp.WithRemotes(f.fetch))
			if err != nil {
				t.Fatal(err)
			}

			if got := describe(p.Graph.Nodes()); !strings.Contains(got, "from-the-remote") {
				t.Errorf("the imported target's steps are not in the graph:\n%s", got)
			}

			if len(f.calls) != 1 || f.calls[0] != tc.want {
				t.Errorf("fetched %v, want %v", f.calls, tc.want)
			}
		})
	}
}

// Without a fetcher a remote import is refused, like any other remote
// reference.
func TestARemoteImportWithoutAFetcherIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nIMPORT github.com/org/repo AS lib\n\nmain:\n    FROM lib+build\n", testMain)
	if err == nil {
		t.Fatal("a remote import was accepted with no way to fetch it")
	}

	if !strings.Contains(err.Error(), "remote repository") {
		t.Errorf("the refusal does not say what is wrong:\n%s", err)
	}
}

// An import with no AS is named after the repository, not after the revision.
//
// `IMPORT github.com/org/repo:main` is called `repo`. The last path element is
// `repo:main` only if you forget that the revision is not part of the name, and
// this repository's own example Earthfile carries a comment saying what the
// alias should be - so the expected behaviour was written down beside the line
// that broke on it.
func TestARemoteImportIsNamedAfterTheRepository(t *testing.T) {
	t.Parallel()

	for _, decl := range []string{
		"IMPORT github.com/org/repo:main",
		"IMPORT github.com/org/repo",
		"IMPORT github.com/org/repo:v1.2.3",
	} {
		t.Run(decl, func(t *testing.T) {
			t.Parallel()

			f := remoteRepo(t, "")

			p, err := interp.Build(versioned+"\n"+decl+"\n\nmain:\n    FROM repo+build\n",
				testMain, interp.WithRemotes(f.fetch))
			if err != nil {
				t.Fatal(err)
			}

			if got := describe(p.Graph.Nodes()); !strings.Contains(got, "from-the-remote") {
				t.Errorf("the import was not reachable as `repo`:\n%s", got)
			}
		})
	}
}
