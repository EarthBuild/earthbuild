package solvermon

import (
	"fmt"
	"strings"
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

const (
	recentOperationLimit = 5
	recentLogLimit       = 8
)

// OperationSnapshot is a compact, scrubbed BuildKit vertex summary used when
// the solve ends as a bare cancellation.
type OperationSnapshot struct {
	OperationStarted time.Time
	End              time.Time
	TargetID         string
	CommandID        string
	Operation        string
	Error            string
	Status           logstream.RunStatus
}

// LogSnapshot is a scrubbed recent vertex log line.
type LogSnapshot struct {
	Timestamp time.Time
	Operation string
	Text      string
}

// CancellationDetails is best-effort context for a canceled solve when
// BuildKit did not provide a fatal or cancellation-specific vertex error.
type CancellationDetails struct {
	End    time.Time
	Active []OperationSnapshot
	Recent []OperationSnapshot
	Logs   []LogSnapshot
}

func (d CancellationDetails) Empty() bool {
	return len(d.Active) == 0 && len(d.Recent) == 0 && len(d.Logs) == 0
}

func (d CancellationDetails) String() string {
	var b strings.Builder
	if len(d.Active) > 0 {
		b.WriteString("Last active operations:\n")

		for _, op := range d.Active {
			fmt.Fprintf(&b, "  - %s\n", op.String())
		}
	}

	if len(d.Recent) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}

		b.WriteString("Recent completed or canceled operations:\n")

		for _, op := range d.Recent {
			fmt.Fprintf(&b, "  - %s\n", op.String())
		}
	}

	if len(d.Logs) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}

		b.WriteString("Recent output:\n")

		for _, log := range d.Logs {
			if log.Operation != "" {
				fmt.Fprintf(&b, "  - %s: %s\n", log.Operation, log.Text)
			} else {
				fmt.Fprintf(&b, "  - %s\n", log.Text)
			}
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func (op OperationSnapshot) String() string {
	if op.Error != "" {
		return fmt.Sprintf("%s: %s", op.Operation, op.Error)
	}

	if op.Operation != "" {
		return op.Operation
	}

	if op.CommandID != "" {
		return op.CommandID
	}

	return op.TargetID
}

func appendWithLimit[T any](items []T, item T, limit int) []T {
	items = append(items, item)
	if len(items) > limit {
		return items[len(items)-limit:]
	}

	return items
}

func splitLogLines(data string) []string {
	lines := strings.Split(data, "\n")

	out := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}

	return out
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

// FirstCancellationError wraps a canceled solve with the first canceled vertex
// observed by the solver monitor.
type FirstCancellationError struct {
	Cause        error
	Cancellation FirstFailure
}

func (e *FirstCancellationError) Error() string {
	if e.Cancellation.Error != "" {
		return e.Cancellation.Error
	}

	return e.Cause.Error()
}

func (e *FirstCancellationError) Unwrap() error {
	return e.Cause
}

func (e *FirstCancellationError) Is(target error) bool {
	return errors.Is(e.Cause, target)
}

func NewFirstCancellationError(cause error, cancellation FirstFailure) error {
	if cancellation.Error == "" {
		return cause
	}

	return &FirstCancellationError{
		Cause:        cause,
		Cancellation: cancellation,
	}
}

func AsFirstCancellationError(err error) (*FirstCancellationError, bool) {
	var cancelErr *FirstCancellationError
	if errors.As(err, &cancelErr) {
		return cancelErr, true
	}

	return nil, false
}

// CancellationDetailsError wraps a canceled solve with recent progress context
// when no specific root cause was observed.
type CancellationDetailsError struct {
	Cause   error
	Details CancellationDetails
}

func (e *CancellationDetailsError) Error() string {
	if !e.Details.Empty() {
		return e.Details.String()
	}

	return e.Cause.Error()
}

func (e *CancellationDetailsError) Unwrap() error {
	return e.Cause
}

func (e *CancellationDetailsError) Is(target error) bool {
	return errors.Is(e.Cause, target)
}

func NewCancellationDetailsError(cause error, details CancellationDetails) error {
	if details.Empty() {
		return cause
	}

	return &CancellationDetailsError{
		Cause:   cause,
		Details: details,
	}
}

func AsCancellationDetailsError(err error) (*CancellationDetailsError, bool) {
	var detailsErr *CancellationDetailsError
	if errors.As(err, &detailsErr) {
		return detailsErr, true
	}

	return nil, false
}
