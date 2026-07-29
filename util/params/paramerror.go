package params

import (
	"fmt"
)

// Error represents an error with an associated parent error and cause.
type Error struct {
	cause error
	msg   string
}

// Errorf returns an Error configured with format message.
func Errorf(format string, args ...any) error {
	return &Error{
		msg: fmt.Sprintf(format, args...),
	}
}

// Wrapf returns an Error wrapping the provided error with format message.
func Wrapf(err error, format string, args ...any) error {
	return &Error{
		msg:   fmt.Sprintf(format, args...),
		cause: err,
	}
}

// Error implements [error] interface.
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Errorf("%s: %w", e.msg, e.cause).Error()
	}

	return e.msg
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error {
	return e.cause
}

// Is checks if the err is an Error.
func (e *Error) Is(err error) bool {
	_, ok := err.(*Error)
	return ok
}

// ParentError returns the parent error message.
func (e *Error) ParentError() string {
	return e.msg
}
