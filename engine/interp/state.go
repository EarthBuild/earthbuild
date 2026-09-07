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
	// stage resolves a Dockerfile stage by name, building it if it has not been
	// built yet. Nil outside a FROM DOCKERFILE, which is what makes
	// `--mount=type=bind,from=x` refusable in an Earthfile: there are no stages
	// there, so there is nothing the name could mean.
	//
	// A function rather than a map because a stage is built on demand, and
	// because the loop detection lives in the builder rather than in the state.
	stage func(name string) (*ir.Node, error)
	args  scope
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
	// saved is what this target's SAVE IMAGE declared, if it declared one.
	//
	// **Recorded because a node cannot answer for it.** A graph deduplicates,
	// so two targets that build one filesystem are one node - and the image a
	// target saves is not a property of that node: `SAVE IMAGE` and `SAVE IMAGE
	// --without-earthly-labels` produce the same layers with different
	// configurations. Resolving "the image this node saves" returns whichever
	// was declared first, so `--load` on the second target packed the first
	// target's image, under an archive named from a hash the two shared (E926).
	saved *Config
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
	// envUnreadable is why this stage's environment is incomplete, when the base
	// image could not be asked what it declares.
	//
	// Carried rather than raised at the point it happens: it matters only if
	// something later actually reads the environment, and a stage that never
	// names a variable does not care that a registry was briefly unreachable.
	envUnreadable error
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
	if len(s.env) == 0 && len(s.declared) == 0 {
		return nil
	}

	out := make(map[string]string, len(s.env)+len(s.declared))

	// **A build argument is an environment variable inside the step**, which the
	// reference does and this did not. Substituting `$GOOS` into a command is
	// not the same thing and looks the same wherever the command names the
	// argument - and cross-compilation is the case where it does not: `ARG
	// GOOS` beside a `go build` that mentions neither, because the toolchain
	// reads the environment. `+all-binaries` built five platforms, reported
	// success and wrote five identical linux/arm64 binaries (E580).
	//
	// The names this recipe declared, not everything in scope: an ARG line is
	// what says a name belongs to this step's world, and exporting inherited
	// values a recipe never mentioned would put a caller's vocabulary into a
	// command that never asked for it.
	// The keys carry the scope that declared them - `local:` or `global:` - and
	// the environment wants the name.
	for key := range s.declared {
		name := key[strings.IndexByte(key, ':')+1:]
		if v, ok := s.args[name]; ok {
			out[name] = v
		}
	}

	// ENV last, because it is the stronger statement: `ARG` declares an input
	// and `ENV` sets the image's own environment, so where both name one thing
	// the image's is what a step - and anything built from it - should see.
	maps.Copy(out, s.env)

	if len(out) == 0 {
		return nil
	}

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

	maps.Copy(out.args, s.globals)

	return out
}
