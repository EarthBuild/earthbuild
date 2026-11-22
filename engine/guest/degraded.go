package guest

// DegradedError reports that resource limits could not be applied, and why.
//
// It is *returned*, not swallowed. The first version of the cgroup code
// degraded silently on every failure path, and the result was a memory ceiling
// that was written, never enforced, and reported by nothing - the bug was
// invisible until a test allocated 256 MiB under a 16 MiB cap and was not
// stopped.
//
// Silent degradation is not graceful; it is undiagnosable. Limits are not a
// correctness property, so the caller may proceed unbounded - but it proceeds
// knowingly. Contrast ErrCannotIsolate, which is refused rather than degraded,
// because isolation *is* a correctness property (green paper A3).
type DegradedError struct{ Reason string }

func (e *DegradedError) Error() string { return "resource limits not applied: " + e.Reason }

func degraded(reason string) (*cgroup, error) { return nil, &DegradedError{Reason: reason} }
