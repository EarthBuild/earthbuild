package statsstreamparser

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/containerd/go-runc"
	"github.com/stretchr/testify/require"
)

// frame builds a valid stats stream frame: [version=1][uint32 LE len][JSON].
func frame(t *testing.T, s runc.Stats) []byte {
	t.Helper()

	j, err := json.Marshal(s)
	require.NoError(t, err)

	var b bytes.Buffer
	b.WriteByte(0x01)
	require.NoError(t, binary.Write(&b, binary.LittleEndian, uint32(len(j)))) //nolint:gosec // test frame length is tiny
	b.Write(j)

	return b.Bytes()
}

func TestParseValidFrame(t *testing.T) {
	t.Parallel()

	p := New()
	stats, err := p.Parse(frame(t, runc.Stats{}))
	require.NoError(t, err)
	require.Len(t, stats, 1)
}

// A malformed frame (raw JSON, first byte '{' = 0x7B = 123) is the exact CI
// failure: the daemon's runc stats collector hit EOF and emitted raw bytes.
// It must be reported as an error, and Reset must let the parser recover so a
// transient desync does not permanently wedge stats collection.
func TestParserRecoversAfterGarbageFrame(t *testing.T) {
	t.Parallel()

	p := New()

	_, err := p.Parse(frame(t, runc.Stats{}))
	require.NoError(t, err)

	_, err = p.Parse([]byte(`{"malformed":true}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "protocol version 123")

	p.Reset()

	stats, err := p.Parse(frame(t, runc.Stats{}))
	require.NoError(t, err, "parser must recover after Reset")
	require.Len(t, stats, 1)
}
