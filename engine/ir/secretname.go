package ir

import "strings"

// ProjectSecretPrefix marks a secret held in a project's secret store.
const ProjectSecretPrefix = "+secrets/"

// SecretName is what a secret is called, whichever way it was named.
//
// **The prefix says where a secret lives, not what it is called.**
// `RUN --secret=TOKEN=+secrets/TOKEN` names a project store's TOKEN and
// `--secret TOKEN=value` supplies one without that store; they are the same
// secret under two spellings, and an engine that matches only the second
// refuses a value it is holding.
//
// Here rather than at each caller because there are three - the interpreter
// checks a RUN's secrets and its mounts, the executor supplies both - and a
// rule written out three times is maintained once. The guest's copy code says
// the same thing about itself, having learned it the hard way.
func SecretName(s string) string {
	return strings.TrimPrefix(s, ProjectSecretPrefix)
}
