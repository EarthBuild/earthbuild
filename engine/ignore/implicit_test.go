package ignore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ignore"
)

// TestTheEarthfileIsPartOfItsOwnContext.
//
// **`--no-implicit-ignore` is `enabled_in_version:"0.6"`**, so for every version
// this engine accepts, the reference does not exclude the Earthfile, its ignore
// file, `build.earth` or `.tmp-earth-out/` from a context. This engine excluded
// all five unconditionally, and `tests/no-implicit-ignore.earth` does `COPY . .`
// and then `RUN ls Earthfile`.
//
// The exclusions a file *asks* for still apply, which the same corpus target
// asserts with `RUN ! ls ignored/`.
func TestTheEarthfileIsPartOfItsOwnContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, name := range []string{"Earthfile", ".earthlyignore", "build.earth", "kept.txt"} {
		err := os.WriteFile(filepath.Join(dir, name), []byte("ignored/\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := os.Mkdir(filepath.Join(dir, "ignored"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	m, err := ignore.Read(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Earthfile", ".earthlyignore", "build.earth", "kept.txt", ".tmp-earth-out"} {
		if m.Excludes(name) {
			t.Errorf("%s is left out of the context, and nothing asked for that", name)
		}
	}

	// What the file itself excludes still goes.
	if !m.Excludes("ignored") {
		t.Error("`ignored/` is named in .earthlyignore and must still be excluded")
	}
}
