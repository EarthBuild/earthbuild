package interp

import "testing"

// A target named with its plus is stripped once, not never and not twice.
//
// The name reaches `builtinArgs` with the `+` the caller wrote: `earth +build`
// and `interp.Build(src, "+build")` both arrive that way, and so does a caller
// who wrote no plus at all. It is stripped once and put back where the reference
// wants it - `EARTH_TARGET` carries the plus, `EARTH_TARGET_NAME` does not.
//
// Without the strip, `EARTH_TARGET` came out `++build` and `EARTH_TARGET_NAME`
// kept a plus that no comparison in any Earthfile expects (E423). The mutant
// deleting it survived a whole sweep, so nothing was checking either spelling.
//
// Both spellings of the input are the point: the function has to be indifferent
// to whether the caller wrote the plus, and a test of one spelling would pass
// with the strip deleted.
func TestATargetNameIsStrippedOfItsPlusExactlyOnce(t *testing.T) {
	t.Parallel()

	for _, given := range []string{"build", "+build"} {
		got := builtinArgs("linux/arm64", "linux/arm64", given, "/somewhere", false, false)

		if got["EARTH_TARGET_NAME"] != "build" {
			t.Errorf("given %q, EARTH_TARGET_NAME is %q, want %q - a name with"+
				" a plus in it matches nothing an Earthfile compares against",
				given, got["EARTH_TARGET_NAME"], "build")
		}

		if got["EARTH_TARGET"] != "+build" {
			t.Errorf("given %q, EARTH_TARGET is %q, want %q",
				given, got["EARTH_TARGET"], "+build")
		}
	}
}
