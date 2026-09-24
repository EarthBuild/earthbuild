package core

import (
	"sort"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Property and Command are the remote execution API's, re-exported so a caller
// deriving a key need not import the encoding to read one.
type (
	Property = layer.Property
	Command  = layer.Command
)

// opProperty is the one platform property that carries everything the API has
// no field for.
//
// **One digest, not twenty-five named properties.** Every field of an operation
// must reach the key - TestEveryOperationFieldReachesTheKey enforces that by
// reflection - and most have no REAPI field: privilege, a docker daemon, an
// ssh agent, a user, hosts, mounts, secret names. Exploding each into a string
// would be a second encoding of the operation to keep in step with the first,
// and this repository has spent a day removing one of those. A digest over the
// rest is total, injective, and adds nothing to maintain.
//
// Opaque to anybody else, which is honest: these are the parts of a step that
// are this engine's business. What a client we did not write would read - the
// argv, the environment, the working directory, the machine - is in the fields
// that exist for them.
const opProperty = "earthbuild.operation"

// CommandOf is the step as the API's Command message.
//
// The argv is the argv: nothing prefixed, wrapped or namespaced, because a
// client this engine did not write declares actions and expects `arguments` to
// be a command line. A step with no argv at all - a copy, an image, a context -
// simply has none, and is told apart by the operation digest instead.
func CommandOf(n *ir.Node) Command {
	keys := make([]string, 0, len(n.Op.Env))
	for k := range n.Op.Env {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	env := make([]Property, 0, len(keys))
	for _, k := range keys {
		env = append(env, Property{Name: k, Value: n.Op.Env[k]})
	}

	return Command{
		Arguments:        n.Op.Args,
		Env:              env,
		WorkingDirectory: n.Op.Dir,
		// Empty until a step can declare what it produces. Nothing depends on
		// it: our own worker returns the whole delta, and no worker we do not
		// own runs our steps.
		OutputPaths: nil,
	}
}

// PlatformOf is the machine this step needs, and one digest for the rest.
//
// In name order, which the API requires and which a digest over them depends
// on. A property whose value is empty is left out, so a step that says nothing
// about the machine produces a platform that says nothing.
func PlatformOf(n *ir.Node, refs []ir.NodeID) []Property {
	out := make([]Property, 0, 4)

	for _, p := range []Property{
		{Name: "arch", Value: n.Platform.Arch},
		{Name: "os", Value: n.Platform.OS},
		{Name: "variant", Value: n.Platform.Variant},
	} {
		if p.Value != "" {
			out = append(out, p)
		}
	}

	rest := ir.NewHasher()
	hashOperation(rest, n, refs)

	out = append(out, Property{Name: opProperty, Value: rest.Sum().String()})

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// ActionOf is the step as the API's Action message, given what its base holds.
//
// ℋ over this is Κₜ (plan-remote-execution R2b): the same number an `Action`
// carries, so a key this engine derives is a key another tool asks for.
func ActionOf(n *ir.Node, refs []ir.NodeID, tree ir.NodeID) layer.Action {
	cmd := layer.EncodeCommand(CommandOf(n))

	return layer.Action{
		Command:     ir.DigestOf(cmd),
		CommandSize: int64(len(cmd)),
		InputRoot:   tree,
		// **The generation, in the field the API has for exactly this.** `salt`
		// exists so an implementation can retire a generation of entries, which
		// is the whole of what ζ does - so ζ is not approximated by it.
		Salt:       []byte{byte(cacheEpoch)},
		DoNotCache: n.Op.NoCache,
		Platform:   PlatformOf(n, refs),
	}
}
