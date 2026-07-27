package params

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errInternal = errors.New("internal")

func TestErrorf(t *testing.T) {
	t.Parallel()

	t.Run("without args", func(t *testing.T) {
		t.Parallel()

		res := Errorf("some error")
		assert.Equal(t, &Error{
			msg: "some error",
		}, res)
	})
	t.Run("with args", func(t *testing.T) {
		t.Parallel()

		res := Errorf("some error %d", 1)
		assert.Equal(t, &Error{
			msg: "some error 1",
		}, res)
	})
}

func TestWrapf(t *testing.T) {
	t.Parallel()

	t.Run("without args", func(t *testing.T) {
		t.Parallel()

		res := Wrapf(errInternal, "some error")
		assert.Equal(t, &Error{
			msg:   "some error",
			cause: errInternal,
		}, res)
	})
	t.Run("with args", func(t *testing.T) {
		t.Parallel()

		res := Wrapf(errInternal, "some error %d", 1)
		assert.Equal(t, &Error{
			msg:   "some error 1",
			cause: errInternal,
		}, res)
	})
}

func TestError(t *testing.T) {
	t.Parallel()

	t.Run("without cause", func(t *testing.T) {
		t.Parallel()

		res := Errorf("some error").Error()
		assert.Equal(t, "some error", res)
	})
	t.Run("with cause", func(t *testing.T) {
		t.Parallel()

		res := Wrapf(errInternal, "some error").Error()
		assert.Equal(t, "some error: internal", res)
	})
}

func TestIs(t *testing.T) {
	t.Parallel()

	t.Run("non param error", func(t *testing.T) {
		t.Parallel()

		var err *Error

		require.ErrorAs(t, Errorf("some error"), &err)
		res := err.Is(errInternal)
		assert.False(t, res)
	})

	t.Run("param error", func(t *testing.T) {
		t.Parallel()

		var err *Error

		require.ErrorAs(t, Errorf("some error"), &err)
		res := err.Is(err)
		assert.True(t, res)
	})
}

func TestParentError(t *testing.T) {
	t.Parallel()

	var err *Error

	require.ErrorAs(t, Wrapf(errInternal, "some error"), &err)
	res := err.ParentError()
	assert.Equal(t, "some error", res)
}
