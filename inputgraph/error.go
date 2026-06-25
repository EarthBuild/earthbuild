package inputgraph

import (
	"errors"
	"fmt"
	"strings"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// Error represents an auto-skip error that can include the source file name and
// associated line number.
type Error struct {
	err    error
	srcLoc *earthfile.SourceLocation
	msg    string
}

// Error implements [error] interface.
func (e *Error) Error() string {
	parts := []string{}
	if e.msg != "" {
		parts = append(parts, e.msg)
	}

	if e.err != nil {
		parts = append(parts, e.err.Error())
	}

	return strings.Join(parts, ": ")
}

// FormatError looks for a wrapped instance of Error in the error list. If one
// is found, it will prefix the error message with source file information
// associated with the error.
func FormatError(err error) string {
	e := &Error{}
	if errors.As(err, &e) {
		return fmt.Sprintf("%s:%d:%d %s", e.srcLoc.File, e.srcLoc.StartLine, e.srcLoc.StartColumn, err)
	}

	return e.Error()
}

func newError(srcLoc *earthfile.SourceLocation, format string, args ...any) error {
	return &Error{
		srcLoc: srcLoc,
		msg:    fmt.Sprintf(format, args...),
	}
}

func wrapError(err error, srcLoc *earthfile.SourceLocation, format string, args ...any) error {
	e := &Error{
		srcLoc: srcLoc,
		err:    err,
	}
	if format != "" {
		e.msg = fmt.Sprintf(format, args...)
	}

	return e
}

func addErrorSrc(err error, srcLoc *earthfile.SourceLocation) error {
	return wrapError(err, srcLoc, "")
}
