package exec

import (
	"fmt"
	"sort"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// configSecrets names every secret whose value appears in an image's
// configuration.
//
// **A layer is not the only place a credential lands.** The delta scan catches a
// step that wrote a secret into a file; the image's own configuration is a
// separate blob, and `ENV TOKEN=$SOME_SECRET` puts the value in it - where
// `SAVE IMAGE` persists it, a registry serves it to anybody who can pull, and
// `docker inspect` prints it without being asked. Worse than a file in one
// respect: a file at least has to be read.
//
// Everything a config carries as text is looked at, because a credential ends up
// wherever the Earthfile put it: an entrypoint argument is as public as an
// environment variable.
//
// **The value never travels with the finding.** A refusal is written to the
// build's output, which is the log the credential was being kept out of.
func configSecrets(cfg ocispec.ImageConfig, secrets map[string]string) []string {
	if len(secrets) == 0 {
		return nil
	}

	where := map[string][]string{}

	note := func(field, text string) {
		for name, value := range secrets {
			// An empty secret appears in every string; a build that supplied one
			// would otherwise be told its whole config is a leak.
			if value != "" && strings.Contains(text, value) {
				where[name] = append(where[name], field)
			}
		}
	}

	for _, e := range cfg.Env {
		note("an environment variable", e)
	}

	for k, v := range cfg.Labels {
		note("the label "+k, v)
	}

	for _, a := range cfg.Entrypoint {
		note("the entrypoint", a)
	}

	for _, a := range cfg.Cmd {
		note("the command", a)
	}

	note("the working directory", cfg.WorkingDir)
	note("the user", cfg.User)

	out := make([]string, 0, len(where))

	for name, fields := range where {
		// Sorted and deduplicated: a secret in three labels is one finding with
		// three places, and two runs must report it the same way.
		sort.Strings(fields)
		out = append(out, fmt.Sprintf("the secret %s appears in %s of the image's configuration",
			name, strings.Join(dedupe(fields), ", ")))
	}

	sort.Strings(out)

	return out
}

// dedupe removes repeats from a sorted slice.
func dedupe(in []string) []string {
	out := in[:0]

	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}

	return out
}
