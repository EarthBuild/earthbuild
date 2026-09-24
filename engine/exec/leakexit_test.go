package exec

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every place that writes an image refuses one carrying a secret.
//
// The refusal calls itself "the exit point" and there are two of them. A layer
// holding a credential has gone nowhere while it sits in this build's store;
// writing the image is what sends it somewhere else. `exec.packImage` checks;
// `cli.writeImages`, which is the path an ordinary `SAVE IMAGE` takes, did not -
// so the guarded exit was the rarer one and the common one wrote the image out
// with the secret in it.
//
// Measured before the fix: `RUN --secret S sh -c 'printf "%s" "$S" > /leak.txt'`
// followed by `SAVE IMAGE` exited zero, and the value was in the layer and in
// the image blob. The detection had worked perfectly - a `.leaked` note sat
// beside the layer saying `S in leak.txt` - and nothing consulted it.
//
// A source guard, because the property is about *every* writer rather than
// about one behaviour: a third exit point added later is the failure this
// catches, and by then whoever adds it will not know this rule exists.
func TestEveryImageWriterRefusesALeakedSecret(t *testing.T) {
	t.Parallel()

	writes := regexp.MustCompile(`image\.WriteLayout\(|WriteArchive\(`)
	refuses := regexp.MustCompile(`efuseLeakedImage\(`)

	for _, pkg := range []string{".", "../cli", "../image"} {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			continue
		}

		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") ||
				strings.HasSuffix(name, "_test.go") {
				continue
			}

			b, err := os.ReadFile(filepath.Clean(filepath.Join(pkg, name)))
			if err != nil {
				t.Fatal(err)
			}

			body := string(b)

			// The package that *defines* the writer is not a caller of it.
			if pkg == "../image" {
				continue
			}

			// This file names the pattern in order to look for it.
			if strings.Contains(body, "regexp.MustCompile") {
				continue
			}

			if writes.MatchString(body) && !refuses.MatchString(body) {
				t.Errorf("%s/%s writes an image and never refuses a leaked"+
					" secret: a credential a step wrote into a layer would be"+
					" published from here", pkg, name)
			}
		}
	}
}
