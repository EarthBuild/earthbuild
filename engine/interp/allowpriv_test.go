package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `COPY --allow-privileged` grants a permission this engine never uses.
//
// The flag lets a *referenced* target run privileged. This engine refuses
// privileged execution by name wherever it appears, so granting the permission
// changes nothing that can happen - and refusing the flag rejects a file over a
// feature it cannot exercise.
//
// The argument is already written in `ignoredFeatures` for
// `--allow-privileged-from-dockerfile`, and it is the safe direction of E34's
// asymmetry: refusing something already implemented costs a working build,
// accepting something not implemented costs a wrong one, and nothing is accepted
// here that was not already refused at the point of use (E420).
func TestAllowPrivilegedIsAPermissionThisEngineNeverUses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    COPY --allow-privileged ./a.txt /x
`, "build", interp.WithContext(dir)); err != nil {
		t.Errorf("a COPY granting a permission this engine cannot use was refused: %v", err)
	}
}

// And privileged execution is still refused, by name.
//
// The half that makes accepting the permission honest. If this ever stops
// refusing, the flag becomes a grant of something real that nothing checked.
func TestPrivilegedExecutionIsStillRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    RUN --privileged true
`, "build")
	if err == nil {
		t.Fatal("a privileged RUN was accepted")
	}

	if !strings.Contains(err.Error(), "--privileged") {
		t.Errorf("the refusal does not name it: %v", err)
	}
}
