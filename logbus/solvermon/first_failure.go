package solvermon

import (
	"fmt"
	"time"

	"github.com/EarthBuild/earthbuild/logstream"
	"github.com/pkg/errors"
)

// FirstFailure is the first fatal BuildKit vertex failure observed on the
// status stream. It is kept separately from the final solve error because
// BuildKit may return context canceled after the original failing vertex has
// already been reported.
type FirstFailure struct {
	End         time.Time
	TargetID    string
	CommandID   string
	Error       string
	FailureType logstream.FailureType
}

// FirstFailureError wraps a solve error with the first fatal vertex failure
// observed by the solver monitor.
type FirstFailureError struct {
	Cause   error
	Failure FirstFailure
}

func (e *FirstFailureError) Error() string {
	return e.Failure.Error
}

func (e *FirstFailureError) Unwrap() error {
	return e.Cause
}

func (e *FirstFailureError) Is(target error) bool {
	return errors.Is(e.Cause, target)
}

func (e *FirstFailureError) UnwrapCause() error {
	return e.Cause
}

func NewFirstFailureError(cause error, failure FirstFailure) error {
	if failure.Error == "" {
		return cause
	}

	return &FirstFailureError{
		Cause:   cause,
		Failure: failure,
	}
}

func AsFirstFailureError(err error) (*FirstFailureError, bool) {
	var failureErr *FirstFailureError
	if errors.As(err, &failureErr) {
		return failureErr, true
	}

	return nil, false
}

func (f FirstFailure) String() string {
	if f.Error != "" {
		return f.Error
	}

	return fmt.Sprintf("build failed in target %s command %s", f.TargetID, f.CommandID)
}
