package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `SAVE ARTIFACT ./*` consumed by another target copies every match.
//
// The producing side declares a *pattern*, whose matches are known only once
// that target's filesystem exists - so the plan carries the pattern and the
// copy is where it becomes files. `tests/platform` is built on this: `+run`
// saves `./*` and `+run-all` copies `+run/*` into a directory per platform,
// fifteen times.
//
// The pattern arrived at the copy intact and landed as a single file *named*
// `*`, so every assertion after it read a path that did not exist -
// `cat: can't open './out/regular/linux/arm/v7/uname-m'`, which names a file
// nobody wrote a rule about (E960).
//
// The export side has done this since `SAVE ARTIFACT ./out-* AS LOCAL` needed
// it, and says so in its own test. One rule, written twice, maintained once.
func TestAPatternCopiesEveryMatchIntoADirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := t.TempDir()

	layer := layerWith(t, dir, "only", map[string]string{
		"/work/uname-m":  "aarch64\n",
		"/work/platform": "linux/arm64\n",
	}, nil)

	s := &Server{LayerDir: dir}

	err := s.copyIn(fixedHandle{root: root}, []string{layer}, "/work/*", "out/", copyOpts{})
	if err != nil {
		t.Fatalf("a pattern naming two artifacts was refused: %v", err)
	}

	for name, want := range map[string]string{"uname-m": "aarch64\n", "platform": "linux/arm64\n"} {
		body, readErr := os.ReadFile(filepath.Join(root, "out", name))
		if readErr != nil {
			t.Errorf("%s was not copied: %v", name, readErr)

			continue
		}

		if string(body) != want {
			t.Errorf("%s holds %q, want %q", name, body, want)
		}
	}

	// And the file the pattern used to become is not there.
	_, err = os.Lstat(filepath.Join(root, "out", "*"))
	if err == nil {
		t.Error(`the pattern was copied as a file named "*"`)
	}
}

// A pattern with several matches and a single-file destination is still an
// error, which is the sentence that makes the rule a rule.
//
// `COPY +t/*.go one.go` cannot mean anything: two files, one name. The message
// lists them, because the author is choosing among files they can see.
func TestAPatternWithManyMatchesNeedsADirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := t.TempDir()

	layer := layerWith(t, dir, "only", map[string]string{
		"/work/a.txt": "a\n",
		"/work/b.txt": "b\n",
	}, nil)

	s := &Server{LayerDir: dir}

	err := s.copyIn(fixedHandle{root: root}, []string{layer}, "/work/*", "one.txt", copyOpts{})
	if err == nil {
		t.Fatal("two files were copied to one name")
	}

	for _, want := range []string{"a.txt", "b.txt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not list %s:\n%v", want, err)
		}
	}
}
