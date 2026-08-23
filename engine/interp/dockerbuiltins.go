package interp

// dockerPredefined are the arguments Docker defines for every build.
//
// The same eight facts as an Earthfile's built-ins and a different rule about
// them, which is why this is a second function and not a second copy: in an
// Earthfile a built-in reaches a command only once `ARG` has declared it, and in
// a Dockerfile the predefined ones are available in the global scope - so a
// `FROM` line uses them without declaring anything, and every multi-platform
// Dockerfile does.
//
// Docker calls the builder's platform BUILD*; an Earthfile calls the same thing
// NATIVE*. The values come from builtinArgs so the two front ends cannot drift
// about what "the platform" is, which is the whole reason that function exists
// (E46: where two paths must agree, the fix is usually to have one).
func dockerPredefined(target, native string) map[string]string {
	// The target name and locality are irrelevant here: a Dockerfile has no
	// EARTH_* builtins, and this function reads only the platform four.
	from := builtinArgs(target, native, "", "", false)

	out := map[string]string{}

	for _, name := range []string{"PLATFORM", "OS", "ARCH", "VARIANT"} {
		out["TARGET"+name] = from["TARGET"+name]
		out["BUILD"+name] = from["NATIVE"+name]
	}

	return out
}

// expandPredefined substitutes Docker's predefined arguments in a stage
// reference.
//
// Only those eight. A Dockerfile's own `ARG`s are handled where an `ARG` is,
// and a `$name` this engine does not define is left exactly as written - an
// engine that expanded everything would eat the names a Dockerfile means
// literally, and it would do it in the one place a mistake becomes a pull from
// a registry.
func expandPredefined(ref, target, native string) string {
	return expandWith(ref, dockerPredefined(target, native))
}
