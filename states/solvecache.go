package states

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/syncutil/unbounded"
	"github.com/pkg/errors"
)

// SolveCache is a formal version of the cache we keep mapping targets to their LLB state.
type SolveCache = unbounded.Cache[StateKey, pllb.State]

// StateKey is a type for a key in SolveCache. These keys seem to be highly convention based,
// and used elsewhere too (LocalFolders?). so this is a step at formalizing that convention,
// since we sometimes need one key, and sometimes another. It may give us some toeholds to
// help with some refactoring later.
type StateKey string

// NewSolveCache gives a new SolveCache instance.
func NewSolveCache() *SolveCache {
	return unbounded.NewCache[StateKey, pllb.State]()
}

// KeyFromHashAndTag builds a state key from a given target state and a docker tag.
// This is useful when you want to reference the same image but with a different name.
func KeyFromHashAndTag(target *SingleTarget, dockerTag string) (StateKey, error) {
	hash, err := target.TargetInput().Hash()
	if err != nil {
		return StateKey(""), errors.Wrap(err, "target input hash")
	}

	key := fmt.Sprintf("%s-%s", dockerTag, hash)

	return StateKey(key), nil
}

// KeyFromState is a simple wrapper to get a key from a given state using the hash of its target.
func KeyFromState(target *SingleTarget) (StateKey, error) {
	hash, err := target.TargetInput().Hash()
	if err != nil {
		return StateKey(""), errors.Wrap(err, "target input hash")
	}

	return StateKey(hash), nil
}
