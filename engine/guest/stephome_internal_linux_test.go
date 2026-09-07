package guest

import "testing"

// Resolving a named user yields its home directory as well as its ids.
//
// `resolveUser` calls `user.Lookup`, whose result carries `HomeDir` beside `Uid`
// and `Gid`. It was fetched and never read, which is why `USER testuser` left
// `HOME` at the floor's `/root` (E865).
//
// **A numeric id offers none, and must not.** `USER 1000` needs no passwd file -
// that is what lets a scratch or distroless image use it - so there is nothing
// to look a home up in, and the caller keeps the floor rather than being handed
// an empty string to set.
func TestResolvingAUserYieldsItsHome(t *testing.T) {
	t.Parallel()

	t.Run("a named user brings its home", func(t *testing.T) {
		t.Parallel()

		// root is the one name every image with a passwd file has.
		uid, _, home, err := resolveUser("root", "", false)
		if err != nil {
			t.Fatal(err)
		}

		if uid != 0 {
			t.Errorf("root resolved to uid %d", uid)
		}

		if home == "" {
			t.Error("root resolved with no home directory, so HOME would stay at the floor")
		}
	})

	t.Run("a numeric id brings none", func(t *testing.T) {
		t.Parallel()

		uid, gid, home, err := resolveUser("1000", "", true)
		if err != nil {
			t.Fatal(err)
		}

		if uid != 1000 || gid != 1000 {
			t.Errorf("USER 1000 resolved to %d:%d", uid, gid)
		}

		if home != "" {
			t.Errorf("a numeric id offered the home %q, which no passwd entry backs", home)
		}
	})
}
