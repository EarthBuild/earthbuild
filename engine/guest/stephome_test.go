package guest

import "testing"

// A step that names a user gets that user's home directory, unless something
// said otherwise.
//
// `stepEnv` floors `HOME` at `/root` and nothing revised it when the step ran as
// somebody else, so `USER testuser` gave `whoami=testuser HOME=/root` where the
// other engine gives `/home/testuser`. Every program that writes to `$HOME` in a
// USER step met that as a permission error against root's home rather than as a
// wrong HOME (E865).
//
// **The decision belongs here and the lookup does not.** By the time the shim
// runs, the environment is folded and a floor `/root` cannot be told from an
// image that declared `/root` on purpose. This side holds the layers, so it can
// say whether anything above the floor spoke; the shim reads the passwd entry,
// because that file is the step's own only after the chroot.
func TestHomeIsOverriddenOnlyWhenNobodyDeclaredIt(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name     string
		declared []string
		env      []string
		want     bool
	}{
		{"nothing said", nil, nil, false},
		{"the image declared it", []string{"HOME=/somewhere"}, nil, true},
		{"the Earthfile declared it", nil, []string{"HOME=/elsewhere"}, true},
		{"both did", []string{"HOME=/a"}, []string{"HOME=/b"}, true},
		{"something else entirely", []string{"PATH=/bin"}, []string{"TZ=UTC"}, false},

		// A prefix is not the name: `HOMEBREW_PREFIX` is not `HOME`, and a
		// check that missed that would silently stop overriding for anyone
		// with it set.
		{"a longer name that starts the same", []string{"HOMEBREW_PREFIX=/opt"}, nil, false},
		{"a bare name with no value", []string{"HOME"}, nil, false},
		{"empty but assigned", nil, []string{"HOME="}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := declaresHome(c.declared, c.env); got != c.want {
				t.Errorf("declaresHome(%q, %q) = %v, want %v", c.declared, c.env, got, c.want)
			}
		})
	}
}
