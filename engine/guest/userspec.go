package guest

import "strings"

// splitUserSpec takes a `USER` spec apart into its user and group, and says
// whether both are numbers.
//
// **The forms are Docker's**, because the Earthfile inherits them: `name`,
// `uid`, `name:group`, `uid:gid`, and the mixed pairs. An empty spec means the
// step keeps the identity it already has.
//
// `numeric` is true only when *everything* named is a number, because that is
// the case a lookup cannot improve on: no `/etc/passwd` is consulted, and a
// step whose image has no passwd file at all - a scratch image, a distroless
// one - can still say `USER 1000`.
func splitUserSpec(spec string) (user, group string, numeric bool) {
	if spec == "" {
		return "", "", false
	}

	user, group, _ = strings.Cut(spec, ":")

	return user, group, allDigits(user) && (group == "" || allDigits(group))
}

// allDigits reports whether s is a non-empty run of ASCII digits.
//
// Not `strconv.Atoi`: a uid is compared and passed on rather than arithmetic,
// and Atoi would accept a leading sign, which no uid has.
func allDigits(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
