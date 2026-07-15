package earthfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	ver, err := parseVersion("VERSION 0.6", "Earthfile")
	r := require.New(t)
	r.NoError(err)
	r.Len(ver.Args, 1)
	r.Equal("0.6", ver.Args[0])
	r.Nil(ver.SourceLocation)
}

func TestParseVersionFile_Error(t *testing.T) {
	t.Parallel()

	_, err := ParseVersionFile("non-existent-file")
	r := require.New(t)
	r.Error(err)
	r.ErrorContains(err, "earthfile: unable to open file")
}

func TestParseFile_Version(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "Earthfile")
	require.NoError(t, os.WriteFile(path, []byte("VERSION 0.8\n"), 0o600))

	tree, err := ParseFile(path)
	r := require.New(t)
	r.NoError(err)
	r.NotNil(tree.Version)
	r.Equal([]string{"0.8"}, tree.Version.Args)
}

// TestVersionVariants covers the accepted spellings of the VERSION statement:
// comments and blank lines before it, trailing comments, feature-flag args,
// and line continuations (including continuation lines that hold only a
// comment). Invalid VERSION values live in TestParseErrors.
func TestVersionVariants(t *testing.T) {
	t.Parallel()

	requireVersion := func(t *testing.T, input string, wantArgs ...string) {
		t.Helper()
		t.Parallel()

		tree, err := Parse("Earthfile", input)
		r := require.New(t)
		r.NoError(err)
		r.NotNil(tree.Version)
		r.Equal(wantArgs, tree.Version.Args)
	}

	// All of these parse to Version.Args == {"0.8"}.
	plain := map[string]string{
		"single line": `VERSION 0.8

test:
    FROM alpine:3.24.1
`,
		"single line with comment": `VERSION 0.8 # make sure a comment here works
`,
		"comment then version": `# test a comment before
VERSION 0.8
`,
		"comments and whitespace before version": `


# welcome to my

# spacious



# test
VERSION 0.8
`,
		"whitespace then version": `


VERSION 0.8
`,
		"version only": `VERSION 0.8
`,
		"version then import": `VERSION 0.8
IMPORT ./subdir AS other
`,
		"multi line": `VERSION \
    0.8
`,
		"multi line with comment no space after hash": `VERSION \ #with a comment that doesn't have a space after the hash.
    0.8
`,
		"multi line with comment": `VERSION \ # with a comment
    0.8
`,
		"multi line with hash run comment": `VERSION \       ##########################
    0.8
`,
		"multi line with comment-only continuation": `VERSION \
    # don't count this as the continued line
    0.8
`,
		"multi line with commented-out arg": `VERSION \ # comment out a single feature
    #--try \
    0.8
`,
		"multi line with empty newline": `VERSION \


    0.8
`,
	}
	for name, input := range plain {
		t.Run(name, func(t *testing.T) { requireVersion(t, input, "0.8") })
	}

	// Feature flags precede the version number and survive continuations.
	withFlag := map[string]string{
		"single line with args": `VERSION --try 0.8
`,
		"multi line with args": `VERSION \ #with a comment that doesn't have a space after the hash.
    --try \
    0.8
`,
	}
	for name, input := range withFlag {
		t.Run(name, func(t *testing.T) { requireVersion(t, input, "--try", "0.8") })
	}
}
