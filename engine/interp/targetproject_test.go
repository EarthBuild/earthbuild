package interp

import "testing"

// TestAReferenceIsQualifiedByHostAndProject.
//
// `tests/empty-git.earth` asserts both names in one target, and they differ by
// exactly the host:
//
//	EARTHLY_GIT_PROJECT_NAME == "earthly/earthly"
//	EARTHLY_TARGET_PROJECT   == "github.com/earthly/earthly"
//
// so they cannot be the same function, which is why this exists beside
// `projectFromURL` rather than inside it.
//
// **The builtins that use it are not reaching a build yet** - E722 - so this
// tests the derivation, which is the part that is finished. Both remote
// spellings give the same answer, because a repository is the same repository
// however it is cloned.
func TestAReferenceIsQualifiedByHostAndProject(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ url, want string }{
		{"https://github.com/earthly/earthly.git", "github.com/earthly/earthly"},
		{"git@github.com:earthly/earthly.git", "github.com/earthly/earthly"},
		{"ssh://git@gitlab.com/group/repo.git", "gitlab.com/group/repo"},
		{"https://github.com/earthly/earthly", "github.com/earthly/earthly"},
		// Nothing to qualify with: a repository with no remote, which is what
		// `empty-git.earth+test-empty` builds and asserts `+test-empty` for.
		{"", ""},
		{"not-a-url", ""},
	} {
		if got := qualifierFromURL(c.url); got != c.want {
			t.Errorf("qualifierFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
