package guest

import (
	"bytes"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// secretsFrom is every credential this step was given, by a name safe to print.
//
// **Two kinds, told apart two ways.** A secret mount carries its value in the
// request and is named by where it appears; a secret environment variable is one
// entry of Env among many and is identifiable only because the host says which
// names are secret.
//
// A value that is empty is dropped rather than gathered: an empty string appears
// in every file, so a secret nobody supplied would report the whole layer.
func secretsFrom(req Request) []layer.Secret {
	var out []layer.Secret

	for _, m := range req.Mounts {
		if m.Secret != "" {
			out = append(out, layer.Secret{Name: m.Target, Value: m.Secret})
		}
	}

	if len(req.SecretEnv) == 0 {
		return out
	}

	want := make(map[string]bool, len(req.SecretEnv))
	for _, name := range req.SecretEnv {
		want[name] = true
	}

	for _, kv := range req.Env {
		name, value, ok := strings.Cut(kv, "=")
		if ok && want[name] && value != "" {
			out = append(out, layer.Secret{Name: name, Value: value})
		}
	}

	return out
}

// redactSecrets removes every credential's value from what a step printed.
//
// **A build log is the most public thing a build produces.** A step that echoes
// one - a `set -x` trace, a curl command line, a config dump on failure - puts
// it in the output, which reaches a terminal, a CI job page, and from there an
// issue somebody pastes it into.
//
// Scrubbed rather than refused, and the difference is deliberate: a secret in a
// layer is an artifact that outlives the build and has to stop it, while a
// secret in the output is already loose and the useful thing is not to repeat
// it. Refusing would also destroy the diagnostic the author needs to find out
// why their step printed it.
//
// The names of what was taken out are returned so the reader can be told.
// Silently altered output is a debugging session that goes nowhere.
func redactSecrets(out []byte, secrets []layer.Secret) ([]byte, []string) {
	if len(out) == 0 || len(secrets) == 0 {
		return out, nil
	}

	var (
		took []string
		done bool
	)

	for _, s := range secrets {
		if s.Value == "" || !bytes.Contains(out, []byte(s.Value)) {
			continue
		}

		if !done {
			// Copied only once something is actually being removed, so an
			// ordinary build does not pay for a duplicate of its own output.
			out = bytes.Clone(out)
			done = true
		}

		out = bytes.ReplaceAll(out, []byte(s.Value), []byte("[redacted:"+s.Name+"]"))
		took = append(took, s.Name)
	}

	sort.Strings(took)

	return out, took
}

// redactingSink scrubs a step's output as it streams.
//
// **A secret does not agree to sit inside one chunk.** Output arrives in
// whatever pieces the step wrote it in, so a credential can straddle two and a
// scrubber that looks at each alone lets it through - the same failure the file
// scanner has to avoid, at a different granularity.
//
// So the tail of every chunk is held back: the longest a secret could be, minus
// one byte, which is the most that can be part of a match still to come. What is
// held is released by the next chunk or by the close, so nothing is lost and the
// delay is bounded by the length of a credential rather than by time.
func redactingSink(to func([]byte), secrets []layer.Secret) (func([]byte), func()) {
	if to == nil || len(secrets) == 0 {
		return to, func() {}
	}

	keep := 0

	for _, s := range secrets {
		if len(s.Value) > keep {
			keep = len(s.Value)
		}
	}

	var held []byte

	emit := func(chunk []byte, last bool) {
		// Built rather than appended onto `held`: appending to it may reuse its
		// array, so the two would alias and the reassignment below would edit
		// what was just handed on.
		buf := make([]byte, 0, len(held)+len(chunk))
		buf = append(buf, held...)
		buf = append(buf, chunk...)

		out, _ := redactSecrets(buf, secrets)

		if last || len(out) <= keep {
			if last {
				held = nil

				if len(out) > 0 {
					to(out)
				}

				return
			}

			held = out

			return
		}

		// Everything but the tail that could still become part of a match.
		cut := len(out) - keep
		held = append([]byte(nil), out[cut:]...)

		to(out[:cut])
	}

	return func(chunk []byte) { emit(chunk, false) }, func() { emit(nil, true) }
}
