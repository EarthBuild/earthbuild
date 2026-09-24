package exec

import (
	"slices"
	"strings"
)

// awsCredentialNames are the AWS variables that authorise something, and so the
// only ones registered with the secret scanner.
//
// **Registering a variable as a secret is not free.** A secret's value is
// redacted from the build log and, if it appears in a layer, *fails the build* -
// and the scanner matches literal values with no length or entropy guard. So
// `AWS_DEFAULT_REGION=us-east-1` registered as a secret would fail any build
// whose layers contain the string `us-east-1`, which is a config file, a README
// or an SDK default away.
//
// These four are long and high-entropy, so a spurious match is not a practical
// concern. `AWS_ACCESS_KEY_ID` is an identifier rather than a secret, and is
// included because it is still not a thing to publish and it costs nothing:
// twenty characters that appear nowhere by accident.
//
// `AWS_SECURITY_TOKEN` is the pre-2014 spelling of the session token, still
// honoured by every SDK and still a credential.
var awsCredentialNames = []string{
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SECURITY_TOKEN",
	"AWS_SESSION_TOKEN",
}

// awsEnv is what `RUN --aws` adds to a step: every AWS variable the invocation
// held, and the names of those the scanner should watch for.
//
// **Everything travels, only the credentials are scanned.** An SDK that cannot
// find its region is no more use than one that cannot find its key, so the
// region, the profile and any endpoint override go too - as ordinary
// environment, because that is what they are.
//
// Sorted, because two builds differing only in map iteration order are two
// builds this engine cannot tell apart, and the environment reaches a key.
func awsEnv(creds map[string]string) (env, secret []string) {
	if len(creds) == 0 {
		return nil, nil
	}

	for name, value := range creds {
		if !strings.HasPrefix(name, "AWS_") || value == "" {
			continue
		}

		env = append(env, name+"="+value)

		if slices.Contains(awsCredentialNames, name) {
			secret = append(secret, name)
		}
	}

	slices.Sort(env)
	slices.Sort(secret)

	return env, secret
}
