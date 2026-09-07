package trace

// Unobserved is what a step whose syscalls nobody watched looked at.
//
// Not "nothing". A step that ran untraced read whatever it read, and an empty
// observation reported as complete would say it read nothing at all - which
// serves an L2 hit against a base where those files differ. The whole value of
// `Incomplete` is that a source can be honest about being lossy (§3.4, I3), and
// this is the most lossy a source gets.
//
// `cause` names why, and may be nil for a platform that simply has no source.
func Unobserved(cause error) Sightings {
	why := "this step ran without a tracer"
	if cause != nil {
		why += ": " + cause.Error()
	}

	return Sightings{Incomplete: true, Why: []string{why}}
}
