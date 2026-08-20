package exec

import "testing"

// The client warning stays a client warning.
//
// `DockerNote` is consumed by `warnNoDockerClient`, whose whole job is to
// explain a step that will say `docker: not found` (E146). A routine
// explanation of which daemon a block got is not that, and putting one there
// makes a build warn about a client that is present and fine.
//
// The first version of `dockerPlanFor` did exactly that, on the reasoning that
// the step should be told which daemon it got - true, and this is not the
// channel for it (E392).
//
// *Failure class: a channel repurposed past the assumption it was written
// under.* The tell is that the reader's name still describes the old meaning.
func TestTheDecisionContributesNoNote(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		isolate bool
		cache   string
		inside  bool
		socket  bool
	}{
		{name: "bare, sharing", inside: true, socket: true},
		{name: "bare, nothing around it"},
		{name: "isolated", isolate: true},
		{name: "a named cache", cache: "layers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := dockerPlanFor(tc.isolate, tc.cache, tc.inside, tc.socket, false)

			if got.Note != "" {
				t.Errorf("deciding which daemon a block gets produced a warning"+
					" about the docker client:\n  %s", got.Note)
			}
		})
	}
}
