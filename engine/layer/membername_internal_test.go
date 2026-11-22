package layer

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// dirBytes builds a Directory message by hand, names and all.
//
// The encoder cannot produce these: it walks a trie keyed on path segments, so
// a name with a separator in it has nowhere to come from. The decoder has to
// cope with one anyway, because the sender is not this engine.
func dirBytes(files, dirs, links []string) []byte {
	var out []byte

	// A real digest, as its own submessage: a zero one encodes as an empty hex
	// string, which the decoder refuses before it ever looks at the name - and
	// the test would then pass without the check it is testing existing.
	id := appendHex(nil, fieldDigestHash, ir.DigestOf([]byte("contents")))

	for _, n := range files {
		node := appendString(nil, fieldName, n)
		node = appendMessage(node, fieldDigest, id)
		out = appendMessage(out, fieldFiles, node)
	}

	for _, n := range dirs {
		node := appendString(nil, fieldName, n)
		node = appendMessage(node, fieldDigest, id)
		out = appendMessage(out, fieldDirectories, node)
	}

	for _, n := range links {
		node := appendString(nil, fieldName, n)
		node = appendString(node, fieldTarget, "elsewhere")
		out = appendMessage(out, fieldSymlinks, node)
	}

	return out
}

// A member's name is one path segment, and this refuses anything else.
//
// **The check REAPI puts on the server.** `Directory.files[].name` is defined as
// a single component, and a sender that ignores that is describing a tree that
// reaches outside the one it is sending. Nothing downstream can tell: the bytes
// hash to the name they were filed under, so verification passes and the write
// lands wherever the name says.
//
// Refused here rather than where it is written, so that holding a Directory is
// itself the guarantee - every consumer of one would otherwise need this check,
// and the one that forgets is the one that matters.
func TestAMemberNameIsOneSegment(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"..",
		".",
		"",
		"../escape",
		"a/b",
		"/absolute",
		"sub/../../escape",
		"trailing/",
		"nul\x00byte",
		`back\slash`,
	} {
		for kind, b := range map[string][]byte{
			"file":    dirBytes([]string{name}, nil, nil),
			"dir":     dirBytes(nil, []string{name}, nil),
			"symlink": dirBytes(nil, nil, []string{name}),
		} {
			if _, err := DirectoryIn(b); err == nil {
				t.Errorf("a %s named %q was accepted", kind, name)
			}
		}
	}
}

// An ordinary name still gets through.
//
// A refusal that refuses everything is not a check, and the test above cannot
// tell the difference on its own.
func TestAnOrdinaryMemberNameIsAccepted(t *testing.T) {
	t.Parallel()

	d, err := DirectoryIn(dirBytes(
		[]string{"main.go", "...", "a.b.c", "-", " leading space"},
		[]string{"sub"},
		[]string{"link"}))
	if err != nil {
		t.Fatalf("an ordinary directory was refused: %v", err)
	}

	if len(d.Files) != 5 || len(d.Dirs) != 1 || len(d.Links) != 1 {
		t.Errorf("got %d files, %d dirs, %d links", len(d.Files), len(d.Dirs), len(d.Links))
	}
}

// One name means one thing in a directory.
//
// **The only way a symlink here can be traversed.** Subdirectories are written
// into paths this engine has just created, so a member cannot be reached
// through a link that a sibling planted - unless two members share a name, and
// the second write lands on what the first one left. REAPI requires the three
// lists to be sorted and a name to appear once; this is that requirement, kept
// because something depends on it rather than because it is written down.
func TestANameAppearsOnceInADirectory(t *testing.T) {
	t.Parallel()

	for what, b := range map[string][]byte{
		"two files":         dirBytes([]string{"x", "x"}, nil, nil),
		"a file and a dir":  dirBytes([]string{"x"}, []string{"x"}, nil),
		"a link and a dir":  dirBytes(nil, []string{"x"}, []string{"x"}),
		"a file and a link": dirBytes([]string{"x"}, nil, []string{"x"}),
	} {
		_, err := DirectoryIn(b)
		if err == nil {
			t.Errorf("%s sharing a name was accepted", what)
			continue
		}

		if !strings.Contains(err.Error(), "x") {
			t.Errorf("%s: the refusal does not say which name: %v", what, err)
		}
	}
}
