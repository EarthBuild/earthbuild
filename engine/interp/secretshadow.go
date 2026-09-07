package interp

import "strings"

// secretNamesIn is the environment names a RUN's own flags introduce as
// secrets.
//
// **Read from the unexpanded arguments, because that is the point.** A secret
// shadows a build argument of the same name for the length of the command, so
// `$foo` in a `RUN --secret foo` must reach the shell rather than being
// replaced here with the argument's value - which is what
// `tests/secrets-args-precedence.earth` asserts in six lines.
//
// Only the environment form. `--mount=type=secret,id=X` puts a secret at a
// *path*, introduces no name, and shadows nothing.
func secretNamesIn(args []string) map[string]bool {
	out := map[string]bool{}

	for i, a := range args {
		var spec string

		switch {
		case strings.HasPrefix(a, "--secret="):
			spec = strings.TrimPrefix(a, "--secret=")
		case a == "--secret" && i+1 < len(args):
			spec = args[i+1]
		default:
			continue
		}

		// `NAME=SOURCE` introduces NAME; `NAME` introduces NAME and takes its
		// value from a secret of that name.
		if name, _, ok := strings.Cut(spec, "="); ok {
			out[name] = true

			continue
		}

		out[spec] = true
	}

	return out
}

// withoutSecrets is the scope with those names taken out.
//
// A copy: the names are hidden for one command's expansion and the scope itself
// is unchanged, because the argument still exists and every later line still
// sees it.
func (s scope) withoutSecrets(names map[string]bool) scope {
	if len(names) == 0 {
		return s
	}

	out := make(scope, len(s))

	for k, v := range s {
		if !names[k] {
			out[k] = v
		}
	}

	return out
}
