package circbuf

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBuffer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		maxSize int
		wantErr bool
	}{
		{name: "negative size", maxSize: -1, wantErr: true},
		{name: "zero size", maxSize: 0, wantErr: true},
		{name: "valid size", maxSize: 5, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, err := NewBuffer(tt.maxSize)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidSize)
				assert.Nil(t, b)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, b)
			}
		})
	}
}

func TestWrite(t *testing.T) {
	t.Parallel()

	type step struct {
		input string
		wantN int
	}

	tests := []struct {
		name      string
		bufSize   int
		steps     []step
		wantBytes string
	}{
		{
			name:    "grow without filling",
			bufSize: 5,
			steps: []step{
				{input: "foo", wantN: 3},
			},
			wantBytes: "foo",
		},
		{
			name:    "single write overflowing buffer",
			bufSize: 5,
			steps: []step{
				{input: "foobarbaz", wantN: 9},
			},
			wantBytes: "arbaz",
		},
		{
			name:    "multiple writes wrapping around",
			bufSize: 5,
			steps: []step{
				{input: "mr", wantN: 2},
				{input: "world", wantN: 5},
				{input: "wide", wantN: 4},
			},
			wantBytes: "dwide",
		},
		{
			name:    "write transition into full ring",
			bufSize: 5,
			steps: []step{
				{input: "mr", wantN: 2},
				{input: "abcd", wantN: 4},
			},
			wantBytes: "rabcd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, err := NewBuffer(tt.bufSize)
			require.NoError(t, err)

			for _, s := range tt.steps {
				n, err := io.WriteString(b, s.input)
				require.NoError(t, err)
				assert.Equal(t, s.wantN, n)
			}

			assert.Equal(t, tt.wantBytes, string(b.Bytes()))
		})
	}
}

func TestBytesImmutability(t *testing.T) {
	t.Parallel()

	b, err := NewBuffer(5)
	require.NoError(t, err)

	_, err = io.WriteString(b, "foo")
	require.NoError(t, err)

	data := b.Bytes()
	data[0] = 'z'

	assert.Equal(t, "foo", string(b.Bytes()))
}

func TestZeroValueSafety(t *testing.T) {
	t.Parallel()

	var b Buffer

	n, err := io.WriteString(&b, "test")

	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Empty(t, b.Bytes())
}

func BenchmarkWrite(b *testing.B) {
	buf, _ := NewBuffer(8192)
	payload := []byte("hello world, this is a test write stream for circbuf performance benchmarking")

	b.ReportAllocs()

	for b.Loop() {
		_, _ = buf.Write(payload)
	}
}
