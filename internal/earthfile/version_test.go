package earthfile_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	namedReader := namedStringReader{strings.NewReader("VERSION 0.6")}
	ver, err := earthfile.ParseVersionOpts(earthfile.FromReader(&namedReader))
	r := require.New(t)
	r.NoError(err)
	r.Len(ver.Args, 1)
	r.Equal("0.6", ver.Args[0])
	r.Nil(ver.SourceLocation)
}
