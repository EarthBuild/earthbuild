package decl

import (
	"os"
	"strings"
)

// Fold applies declarations to an environment, in order.
//
// **The one place this composition happens.** What an image declares and what an
// Earthfile declares are the same kind of thing said about the same step, and
// composing them by two rules is how the two came to disagree. Green paper
// §3.2a.
//
// Later wins, and a value is expanded against everything set before it and
// nothing set after - which is also why a declaration stores its text
// unexpanded (3.10): the fold is the only place the value of `$PATH` is known,
// since it is whatever the elements before it left.
func Fold(base []string, ds ...Declaration) []string {
	out := slicesClone(base)

	for _, d := range ds {
		for _, e := range d.Env {
			name, value, set := strings.Cut(e, "=")
			if !set {
				out = remove(out, name)

				continue
			}

			out = assign(out, name, expand(value, out))
		}
	}

	return out
}

// remove takes a name out of the environment.
//
// **A name with no value is a removal**, which is what a layer's whiteout is for
// a path: an encoding that can only add needs a way to say "not this". The two
// cannot be confused, because POSIX forbids `=` in a name, so an entry without
// one is not an assignment anybody could have meant.
//
// Removed rather than emptied. `os.LookupEnv` tells the two apart and so does
// anything that enumerates, so a step scanning for a prefix sees a name set to
// nothing where it should see no name at all.
func remove(env []string, name string) []string {
	out := env[:0]

	for _, have := range env {
		if had, _, _ := strings.Cut(have, "="); had == name {
			continue
		}

		out = append(out, have)
	}

	return out
}

// assign sets a name, replacing any entry of the same name in place.
//
// In place, so an override does not reorder the environment: two builds that
// differ only in the order their environment happens to be serialised in are
// two keys for one step.
func assign(env []string, name, value string) []string {
	for i, have := range env {
		if had, _, _ := strings.Cut(have, "="); had == name {
			env[i] = name + "=" + value

			return env
		}
	}

	return append(env, name+"="+value)
}

// expand replaces `$NAME` and `${NAME}` with what the environment holds.
//
// `$$` is a literal dollar and is the only escape. A name nothing defines
// becomes empty, exactly as a shell would leave it - and exactly as a name this
// fold removed does, which is what makes removal mean what it says.
func expand(value string, env []string) string {
	seen := make(map[string]string, len(env))

	for _, e := range env {
		if name, v, ok := strings.Cut(e, "="); ok {
			seen[name] = v
		}
	}

	return os.Expand(value, func(name string) string {
		// os.Expand calls this with "$" for a literal `$$`.
		if name == "$" {
			return "$"
		}

		return seen[name]
	})
}

// slicesClone copies so a fold never edits its caller's environment.
func slicesClone(xs []string) []string {
	out := make([]string, len(xs))
	copy(out, xs)

	return out
}

// Literal is a declaration whose values are already expanded.
//
// **An image's environment is not a template.** A Dockerfile's `ENV` is resolved
// when the image is built, so a value reaching this engine from an OCI
// configuration means the characters it contains - and a declaration stores text
// *before* expansion (3.10), so handing one straight to the fold would expand it
// a second time and turn an image's literal dollar into something else.
//
// Escaping `$` as `$$` says "these characters", using the fold's only escape, so
// there is still one rule about what a declaration means rather than a flag
// saying which rule applies.
//
// A removal carries no value and is left alone: escaping a bare name would make
// "remove this" into "set this to nothing", which is exactly the distinction
// removal exists to draw.
func Literal(env []string) Declaration {
	out := make([]string, 0, len(env))

	for _, e := range env {
		name, value, set := strings.Cut(e, "=")
		if !set {
			out = append(out, e)

			continue
		}

		out = append(out, name+"="+strings.ReplaceAll(value, "$", "$$"))
	}

	return Declaration{Env: out}
}

// Compose is the declaration a stack leaves: every element applied in order.
//
// The environment folds - later wins, and a name with no value removes it. The
// rest overrides only when the later declaration says something, because a
// declaration that is silent about the user leaves the user alone, exactly as a
// Dockerfile omitting `USER` inherits it. Silence and emptiness are the same
// thing for these fields: OCI has no way to say "no entrypoint" either.
//
// One operation over whole declarations, so a caller wanting the working
// directory a stack settled on does not invent its own rule for it.
func Compose(ds ...Declaration) Declaration {
	var out Declaration

	for _, d := range ds {
		out.Env = Fold(out.Env, Literal(nil), d)

		if d.WorkingDir != "" {
			out.WorkingDir = d.WorkingDir
		}

		if d.User != "" {
			out.User = d.User
		}

		if len(d.Entrypoint) > 0 {
			out.Entrypoint = d.Entrypoint
		}

		if len(d.Cmd) > 0 {
			out.Cmd = d.Cmd
		}
	}

	return out
}
