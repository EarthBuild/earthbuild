package earthfile

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type errorReader struct{}

func (e *errorReader) Name() string {
	return "errorFile"
}

func (e *errorReader) Seek(_ int64, _ int) (int64, error) {
	return 0, nil
}

func (e *errorReader) Read(_ []byte) (n int, err error) {
	return 0, errors.New("simulated read error")
}

func TestParseVersion(t *testing.T) {
	t.Parallel()

	namedReader := namedStringReader{strings.NewReader("VERSION 0.6")}
	ver, err := ParseVersionOpts(FromReader(&namedReader))
	r := require.New(t)
	r.NoError(err)
	r.Len(ver.Args, 1)
	r.Equal("0.6", ver.Args[0])
	r.Nil(ver.SourceLocation)
}

func TestParseVersion_Error(t *testing.T) {
	t.Parallel()

	reader := &errorReader{}
	_, err := ParseVersionOpts(FromReader(reader))
	r := require.New(t)
	r.Error(err)
	r.ErrorContains(err, "simulated read error")
}
