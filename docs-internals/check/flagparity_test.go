package check_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// notInNative are build flags the native front end deliberately does not take,
// each with the reason it does not.
//
// **A list, not a pattern.** Every entry is a decision somebody made once and
// can be argued with; a rule would let the next divergence in without anybody
// noticing, which is the thing this test exists to stop.
var notInNative = map[string]string{
	"engine": "chooses between the two engines, and this binary is one of them",
	"cache-from": "a BuildKit cache import, which the native engine's store " +
		"does not have an equivalent of yet",
}

// TestTheTwoFrontEndsTakeTheSameBuildFlags.
//
// `earth-native`'s own header says what it is for: "it will become `earthly
// --engine=native` once the flag is wired through the existing CLI". The flag
// is wired. Until the binary goes, two front ends put the same argument in
// front of the same engine - and its `-build-arg` comment already names the
// hazard:
//
//	a flag one takes and the other refuses is a script that works until
//	somebody changes which binary they call
//
// That was written about one flag. This checks all of them, because the way it
// was found was a user asking for `--secret` and getting a usage message.
func TestTheTwoFrontEndsTakeTheSameBuildFlags(t *testing.T) {
	t.Parallel()

	shared, err := os.ReadFile("../../cmd/earth/subcmd/build_flags.go")
	if err != nil {
		t.Skipf("no shared build flags to compare against: %v", err)
	}

	native, err := os.ReadFile("../../cmd/earth-native/main.go")
	if err != nil {
		t.Fatal(err)
	}

	// `Name: "secret",` in the urfave definitions.
	names := regexp.MustCompile(`Name:\s+"([a-z][a-z0-9-]*)"`)

	var missing []string

	for _, m := range names.FindAllStringSubmatch(string(shared), -1) {
		flag := m[1]
		if _, ok := notInNative[flag]; ok {
			continue
		}

		// The native front end registers with the standard library, so the flag
		// appears as its quoted name in a `flag.` call.
		if !strings.Contains(string(native), `"`+flag+`"`) {
			missing = append(missing, flag)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("earthly takes these build flags and earth-native does not: %v"+
			"\n  add them, or give each a reason in notInNative"+
			"\n  a flag one front end takes and the other refuses is a script that"+
			"\n  works until somebody changes which binary they call", missing)
	}
}
