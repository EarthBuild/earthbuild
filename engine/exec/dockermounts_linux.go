//go:build linux

package exec

// dockerFor decides which daemon a WITH DOCKER step gets on this backend.
//
// The sandbox filesystem is this machine's, so the daemon around this build - if
// there is one - is either an outer step's, when this build is itself running in
// a container, or the machine's own. `dockerPlanFor` tells them apart and takes
// only the first without asking (E380).
//
// Where there is nothing to share, or the block asked for its own, the step
// starts one. That replaces the refusal E354 recorded: there is a third answer
// now and it is better than either of the two that were available then.
func dockerFor(isolate bool, cache string) (dockerPlan, error) {
	// statSocket, not lookHostDocker. The latter is `exec.LookPath`, which asks
	// whether something is an *executable*; a unix socket is not one, so it
	// would answer "nothing to inherit" on every machine and sharing would
	// silently never happen (E383).
	socket := statSocket(hostDockerSocket)

	plan := dockerPlanFor(isolate, cache, hereInContainer(), socket, hostDockerAllowed())

	// The socket an inheriting step reaches its daemon through, and the client
	// either kind needs. Separate from the decision because they are separate
	// concerns, and joined here because a decision without its consequence is
	// indistinguishable from no decision at all (E385).
	client, note := clientMounts(lookHostDocker)
	plan.Note = note

	return withSocket(plan, client), nil
}
