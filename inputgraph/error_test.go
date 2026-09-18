package inputgraph

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testEarthfile = "Earthfile"

func TestError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  *Error
		name string
		want string
	}{
		{
			name: "nil receiver",
			err:  nil,
			want: "",
		},
		{
			name: "message only",
			err:  &Error{msg: "failed to parse"},
			want: "failed to parse",
		},
		{
			name: "underlying error only",
			err:  &Error{err: io.EOF},
			want: "EOF",
		},
		{
			name: "message and underlying error",
			err:  &Error{msg: "read error", err: io.EOF},
			want: "read error: EOF",
		},
		{
			name: "empty error",
			err:  &Error{},
			want: "",
		},
		{
			name: "message and location",
			err: &Error{
				srcLoc: earthfile.SourceLocation{
					File:        testEarthfile,
					StartLine:   12,
					StartColumn: 4,
				},
				msg: "failed to parse",
			},
			want: "Earthfile:12:4: failed to parse",
		},
		{
			name: "underlying error and location",
			err: &Error{
				srcLoc: earthfile.SourceLocation{
					File:        testEarthfile,
					StartLine:   5,
					StartColumn: 1,
				},
				err: io.EOF,
			},
			want: "Earthfile:5:1: EOF",
		},
		{
			name: "message, underlying error, and location",
			err: &Error{
				srcLoc: earthfile.SourceLocation{
					File:        testEarthfile,
					StartLine:   12,
					StartColumn: 4,
				},
				msg: "read error",
				err: io.EOF,
			},
			want: "Earthfile:12:4: read error: EOF",
		},
		{
			name: "location only",
			err: &Error{
				srcLoc: earthfile.SourceLocation{
					File:        testEarthfile,
					StartLine:   12,
					StartColumn: 4,
				},
			},
			want: "Earthfile:12:4",
		},
		{
			name: "newError with location",
			err: newError(earthfile.SourceLocation{
				File:        testEarthfile,
				StartLine:   12,
				StartColumn: 4,
			}, "syntax error").(*Error),
			want: "Earthfile:12:4: syntax error",
		},
		{
			name: "wrapError with location",
			err: wrapError(io.EOF, earthfile.SourceLocation{
				File:        testEarthfile,
				StartLine:   5,
				StartColumn: 1,
			}, "unexpected eof").(*Error),
			want: "Earthfile:5:1: unexpected eof: EOF",
		},
		{
			name: "addErrorSrc with location",
			err: addErrorSrc(errors.New("base failure"), earthfile.SourceLocation{
				File:      testEarthfile,
				StartLine: 20,
			}).(*Error),
			want: "Earthfile:20: base failure",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}

func TestError_Unwrap(t *testing.T) {
	t.Parallel()

	t.Run("nil receiver", func(t *testing.T) {
		t.Parallel()

		var e *Error

		assert.NoError(t, e.Unwrap())
	})

	t.Run("returns cause", func(t *testing.T) {
		t.Parallel()

		cause := errors.New("underlying cause")
		e := &Error{err: cause, msg: "context"}

		require.Equal(t, cause, e.Unwrap())
		require.ErrorIs(t, e, cause)
	})

	t.Run("chain unwrapping with errors.Is and errors.As", func(t *testing.T) {
		t.Parallel()

		customErr := fmt.Errorf("custom: %w", io.ErrUnexpectedEOF)
		e := wrapError(customErr, earthfile.SourceLocation{}, "wrapped")

		require.ErrorIs(t, e, io.ErrUnexpectedEOF)

		var target *Error
		require.ErrorAs(t, e, &target)
		require.Equal(t, "wrapped", target.msg)
	})

	t.Run("wrapError with nil error returns nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, wrapError(nil, earthfile.SourceLocation{}, "context"))
	})

	t.Run("addErrorSrc with nil error returns nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, addErrorSrc(nil, earthfile.SourceLocation{}))
	})
}
