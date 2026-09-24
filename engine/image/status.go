package image

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// statusError is a registry answering with a status this engine did not want.
//
// A type rather than a formatted string, so that deciding whether to try again
// is a question about a number instead of a question about wording. The retry
// predicate below is the only reason this exists; without it the choice would
// be between retrying every failure - including a 404, which will still be a
// 404 in two seconds - and matching text this package itself prints.
type statusError struct {
	URL    string
	Code   int
	Status string

	// Detail explains a status whose cause is unambiguous, and is empty for the
	// ones with several. See the 406 note in `get`.
	Detail string
}

func (e *statusError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s returned %s\n  %s", e.URL, e.Status, e.Detail)
	}

	return fmt.Sprintf("%s returned %s", e.URL, e.Status)
}

// retryableRegistryError reports whether another attempt could answer
// differently.
//
// **Two answers are worth waiting for and the rest are not.** 429 is the
// registry saying "not now", and 5xx is it saying "not me, not yet"; both are
// statements about this moment. A 4xx is a statement about the request - the
// reference, the credentials, the formats offered - and will be the same answer
// however long anybody waits.
//
// Anything that is not a status at all reached here as a transport or read
// failure, which is the fault this retry exists for, so it is retried.
func retryableRegistryError(err error) bool {
	if se, ok := errors.AsType[*statusError](err); ok {
		return se.Code == http.StatusTooManyRequests || se.Code >= http.StatusInternalServerError
	}

	return true
}

// unsupportedFormats is the 406 detail, kept here beside the type that carries it.
func unsupportedFormats() string {
	return "it has none of the formats this engine reads: " + strings.Join(accepts, ", ") +
		"\n  the image may be in a format this engine does not support yet"
}
