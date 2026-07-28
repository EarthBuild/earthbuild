package buildkitd

import (
	"os"
	"strconv"

	"github.com/pkg/errors"
)

// parseBoolEnv reports whether the named environment variable holds a truthy
// value. Unset and empty are false without an error; anything strconv.ParseBool
// rejects is false *with* one, so callers can warn instead of silently taking
// the wrong branch on a typo such as EARTHLY_WITH_DOCKER=yes.
func parseBoolEnv(key string) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return false, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, errors.Errorf(
			"%s is set to %q, which is not a boolean: expected one of 1, t, T, true, TRUE, True, 0, f, F, false, FALSE, False",
			key, raw)
	}

	return value, nil
}
