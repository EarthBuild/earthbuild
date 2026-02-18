package hint

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errInternal = errors.New("internal")

func TestWrapf(t *testing.T) {
	t.Parallel()

	t.Run("without args", func(t *testing.T) {
		t.Parallel()

		res := Wrapf(errInternal, "some hint")
		assert.Equal(t, &Error{
			err:   errInternal,
			hints: []string{"some hint"},
		}, res)
	})
	t.Run("with args", func(t *testing.T) {
		t.Parallel()

		res := Wrapf(errInternal, "some hint with arg %s", "my-arg")
		assert.Equal(t, &Error{
			err:   errInternal,
			hints: []string{"some hint with arg my-arg"},
		}, res)
	})
}

func TestWrap(t *testing.T) {
	t.Parallel()

	t.Run("with one hint", func(t *testing.T) {
		t.Parallel()

		res := Wrap(errInternal, "some hint")
		assert.Equal(t, &Error{
			err:   errInternal,
			hints: []string{"some hint"},
		}, res)
	})
	t.Run("with multiple hints", func(t *testing.T) {
		t.Parallel()

		res := Wrap(errInternal, "some hint", "another hint")
		assert.Equal(t, &Error{
			err:   errInternal,
			hints: []string{"some hint", "another hint"},
		}, res)
	})
}

func TestReceivers(t *testing.T) {
	t.Parallel()

	err := Wrap(errInternal, "some hint", "another hint")

	t.Run("test Error", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "internal:Hint: some hint\nanother hint\n", err.Error())
	})

	t.Run("test Message", func(t *testing.T) {
		t.Parallel()

		var hintErr *Error

		require.ErrorAs(t, err, &hintErr)
		assert.Equal(t, "internal", hintErr.Message())
	})

	t.Run("test Hint", func(t *testing.T) {
		t.Parallel()

		var hintErr *Error

		require.ErrorAs(t, err, &hintErr)
		assert.Equal(t, "internal", hintErr.Message())
		assert.Equal(t, "some hint\nanother hint\n", hintErr.Hint())
	})
}

func TestFromError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err                 error
		expectedErr         *Error
		expectedIsHintError bool
	}{
		"err is nil": {},
		"err is not a hint err (but close)": {
			err: errors.New("some error: Hint 123"),
		},
		"err is a hint error": {
			err:                 Wrap(errInternal, "some hint"),
			expectedErr:         wrap(errInternal, "some hint\n"),
			expectedIsHintError: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := FromError(tc.err)
			assert.Equal(t, tc.expectedErr, res)
		})
	}
}
