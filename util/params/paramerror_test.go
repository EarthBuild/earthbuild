package params

import (
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
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

		res := Errorf("some error %s", "myarg")
		assert.Equal(t, &Error{
			msg: "some error myarg",
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

		res := Wrapf(errInternal, "some error %s", "myarg")
		assert.Equal(t, &Error{
			msg:   "some error myarg",
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

func TestCause(t *testing.T) {
	t.Parallel()

	var err *Error

	assert.True(t, errors.As(Wrapf(errInternal, "some error"), &err))
	res := err.Cause()
	assert.Equal(t, errors.Cause(errInternal), res)
}

func TestIs(t *testing.T) {
	t.Parallel()

	t.Run("non param error", func(t *testing.T) {
		t.Parallel()

		var err *Error

		assert.True(t, errors.As(Errorf("some error"), &err))
		res := err.Is(errInternal)
		assert.False(t, res)
	})

	t.Run("param error", func(t *testing.T) {
		t.Parallel()

		var err *Error

		assert.True(t, errors.As(Errorf("some error"), &err))
		res := err.Is(err)
		assert.True(t, res)
	})
}

func TestParentError(t *testing.T) {
	t.Parallel()

	var err *Error

	assert.True(t, errors.As(Wrapf(errInternal, "some error"), &err))
	res := err.ParentError()
	assert.Equal(t, "some error", res)
}
