package states

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/internal/synccache"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
)

// SolveCache is a formal version of the cache we keep mapping targets to their LLB state.
type SolveCache = synccache.Cache[StateKey, pllb.State]

// StateKey is a type for a key in SolveCache. These keys seem to be highly convention based,
// and used elsewhere too (LocalFolders?). so this is a step at formalizing that convention,
// since we sometimes need one key, and sometimes another. It may give us some toeholds to
// help with some refactoring later.
type StateKey string

// NewSolveCache gives a new SolveCache instance.
func NewSolveCache() *SolveCache {
	return synccache.NewCache[StateKey, pllb.State]()
}

// KeyFromHashAndTag builds a state key from a given target state and a docker tag.
// This is useful when you want to reference the same image but with a different name.
func KeyFromHashAndTag(target *SingleTarget, dockerTag string) (StateKey, error) {
	hash, err := target.TargetInput().Hash()
	if err != nil {
		return StateKey(""), fmt.Errorf("target input hash: %w", err)
	}

	key := fmt.Sprintf("%s-%s", dockerTag, hash)

	return StateKey(key), nil
}
