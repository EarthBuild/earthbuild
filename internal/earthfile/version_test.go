package earthfile

import (
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
