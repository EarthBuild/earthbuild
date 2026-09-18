package inputgraph

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// Error represents an error that occurred while loading an Earthfile target graph,
// with an associated source location.
type Error struct {
	err    error
	msg    string
	srcLoc earthfile.SourceLocation
}

// Error implements [error] interface, formatting the source location and message.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	var text string

	switch {
	case e.msg != "" && e.err != nil:
		text = e.msg + ": " + e.err.Error()
	case e.msg != "":
		text = e.msg
	case e.err != nil:
		text = e.err.Error()
	}

	if e.srcLoc.IsZero() {
		return text
	}

	loc := e.srcLoc.String()
	if text == "" {
		return loc
	}

	return loc + ": " + text
}

// Unwrap returns the underlying cause of the error.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.err
}

func newError(srcLoc earthfile.SourceLocation, format string, args ...any) error {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	return &Error{
		srcLoc: srcLoc,
		msg:    msg,
	}
}

func wrapError(err error, srcLoc earthfile.SourceLocation, format string, args ...any) error {
	if err == nil {
		return nil
	}

	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	return &Error{
		srcLoc: srcLoc,
		err:    err,
		msg:    msg,
	}
}

func addErrorSrc(err error, srcLoc earthfile.SourceLocation) error {
	return wrapError(err, srcLoc, "")
}
