package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A path that never appeared says what was there instead.
//
// `the sandbox has no /usr/local/bin/docker to give this step, after waiting
// 1m30s` is the largest remaining failure in the corpus sweep (E28), and it is
// the same sentence whether the image has no docker in it, the directory does
// not exist at all, or the store was still being unpacked when the timer ran
// out. Those are three different faults with three different remedies, and a
// message that cannot tell them apart cannot be acted on - which is why five
// failures across four Earthfiles are still unattributed.
//
// Tested here rather than beside the waiting, because the waiting is Linux-only
// and this is not: a message is worth checking on the machine the developer is
// sitting at.
func TestAMissingPathSaysWhatWasThere(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// A directory that exists and holds neighbours: the image is there and the
	// file is not, so the image is the thing to look at.
	present := filepath.Join(root, "bin")

	err := os.MkdirAll(present, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"sh", "busybox"} {
		err = os.WriteFile(filepath.Join(present, name), nil, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name string
		path string
		want []string
	}{
		{
			name: "the directory holds other things",
			path: filepath.Join(present, "docker"),
			want: []string{present, "sh", "busybox"},
		},
		{
			name: "the directory is empty",
			path: filepath.Join(root, "empty", "docker"),
			want: []string{testMissingWord},
		},
		{
			name: "nothing above it exists either",
			path: filepath.Join(root, "no", "such", "tree", "docker"),
			want: []string{testMissingWord, root},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := explainMissing(tc.path)

			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the account does not mention %q:\n%s", want, got)
				}
			}
		})
	}
}

// The account is bounded, because it goes into an error message.
//
// A directory with four hundred entries listed in full is not a diagnosis, it
// is the reason people stop reading errors.
func TestTheAccountOfADirectoryIsBounded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for i := range 200 {
		err := os.WriteFile(filepath.Join(dir, string(rune('a'+i%26))+string(rune('a'+i/26))), nil, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	got := explainMissing(filepath.Join(dir, "docker"))

	if len(got) > 400 {
		t.Errorf("the account is %d characters:\n%s", len(got), got)
	}

	if !strings.Contains(got, "200") {
		t.Errorf("the account does not say how many there were:\n%s", got)
	}
}
