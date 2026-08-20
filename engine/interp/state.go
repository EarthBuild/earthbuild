package interp

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// state is what a recipe accumulates as it is read: things that change the
// meaning of later commands without being steps themselves.
//
// Per-recipe, and copied rather than shared, because a target that inherits the
// base recipe inherits its *state* - not a channel back into it. Two targets
// setting different working directories must not see each other's.
type state struct {
	args scope
	// declared are the names this *recipe* has an ARG line for.
	//
	// ARG declares a name and a default; it does not assign. So a second ARG for
	// a name this recipe already declared keeps the first value, while an ARG in
	// a target for a name inherited from elsewhere sets it - the two look
	// identical in `args` and differ only in who wrote them, which is what this
	// records (E438).
	declared map[string]bool
	// supplied are values given from outside this recipe: the command line for a
	// target, the call for a function.
	//
	// Kept apart from args because ARG *declares* and a declaration must not
	// overwrite what was passed in - which it did, so `DO +GREET --name=world`
	// ran a function whose ARG default silently replaced the argument.
	supplied map[string]string
	env      map[string]string
	dir      string
	user     string
	platform string
	// mounts are directories declared by CACHE, carried by every step after the
	// line that declared them.
	mounts []ir.Mount
	// hosts are name-to-address entries declared by HOST, carried the same way
	// and for the same reason: they change what a later step resolves.
	hosts []string
	// host says the recipe has passed a LOCALLY, so its steps run on the
	// invoking machine.
	host bool
	// globals are the arguments declared `ARG --global`, which reach every
	// recipe of this file including the inside of a function.
	//
	// Separate from args because the distinction is the flag's whole purpose: a
	// function is a unit with its own interface and must not see its caller's
	// locals, and `--global` is the author saying "this one, everywhere" rather
	// than "everything" (E425).
	globals map[string]string
	// target is the name of the target being interpreted, for the builtin
	// `EARTH_TARGET_NAME` and its relatives.
	//
	// On the state rather than the Plan because a function inlined into a target
	// is still that target's build, and a nested resolution must not leave the
	// name of the target it wandered into behind it.
	target string
	cfg    Config
}

func newState() *state {
	return &state{
		args: scope{}, env: map[string]string{}, dir: "/", globals: map[string]string{},
		// A recipe's declarations, which the base recipe has as much as a target
		// does. Left nil, `declare` read a nil map and wrote to nothing, so the
		// one-declaration-per-name rule did not apply to the commands before the
		// first target - and the mutation sweep found it by collapsing the scope
		// distinction and watching nothing fail (E456).
		declared: map[string]bool{},
		cfg:      Config{Labels: map[string]string{}, Env: map[string]string{}},
	}
}

func (s *state) clone() *state {
	out := &state{
		args: scope{}, supplied: s.supplied, env: map[string]string{},
		declared: maps.Clone(s.declared),
		dir:      s.dir, user: s.user, platform: s.platform, host: s.host, cfg: s.cfg.clone(),
		target: s.target, globals: s.globals,
		hosts: slices.Clone(s.hosts),
	}

	maps.Copy(out.args, s.args)

	maps.Copy(out.env, s.env)

	return out
}

// envFor copies the environment for one step.
//
// Copied rather than shared: a step holds the environment as it stood when the
// step was reached, and a later ENV must not reach backwards into a node already
// built. Sharing the map made every step in a recipe see the last declaration.
func (s *state) envFor() map[string]string {
	if len(s.env) == 0 {
		return nil
	}

	out := make(map[string]string, len(s.env))
	maps.Copy(out, s.env)

	return out
}

// resolveDir applies a WORKDIR to the current one.
//
// A relative path resolves against the current directory, as it does in every
// shell and in Dockerfiles. Treating it as absolute would silently run commands
// somewhere the author did not name.
func resolveDir(current, next string) string {
	if next == "" {
		return current
	}

	if strings.HasPrefix(next, "/") {
		return path.Clean(next)
	}

	return path.Clean(path.Join(current, next))
}

// baseRecipe evaluates the commands before the first target, once.
//
// Memoised: the base is shared by every target, so it is one subgraph rather
// than one per target. Node identity would collapse the copies anyway, but
// re-evaluating it per target makes a large file quadratic to plan.
func (p *Plan) baseRecipe(u *unit) (*ir.Node, *state, error) {
	if u.baseDone {
		return u.baseNode, u.baseState, nil
	}

	// Marked done before evaluating, so a base recipe that somehow refers to a
	// target cannot recurse back into itself.
	u.baseDone = true
	u.baseState = newState()
	u.baseState.supplied = p.opt.args

	prev := p.here
	p.here = u

	n, err := p.block(u.tree.BaseRecipe, nil, u.baseState)

	p.here = prev

	if err != nil {
		return nil, nil, err
	}

	u.baseNode = n

	return u.baseNode, u.baseState, nil
}

// forTarget is the state a target starts from, given the base recipe's.
//
// A target inherits the base recipe's image, working directory, user,
// environment and platform - and of its *arguments*, only the ones declared
// `--global`. That is what the flag is for: without it the name belongs to the
// recipe that declared it, and this engine passed every base-recipe argument to
// every target, so `--global` decided nothing (E438).
//
// The declarations do not travel either. A target writing `ARG FOO = baz` for a
// name the base recipe declared global is overriding it, which is allowed and is
// what the corpus asserts; a second ARG *within* one recipe is not.
func (s *state) forTarget() *state {
	out := s.clone()
	out.args = scope{}
	out.declared = map[string]bool{}

	for name, v := range s.globals {
		out.args[name] = v
	}

	return out
}
