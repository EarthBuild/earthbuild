package layer

import "github.com/EarthBuild/earthbuild/engine/ir"

// The REAPI Command, Platform and Action messages.
//
// **These carry the key**, where Directory carries the tree. Κₜ is ℋ over an
// Action (plan-remote-execution R2b), so a byte wrong here is not a malformed
// message - it is every cache entry in the store named wrongly, and the two
// halves of a fleet disagreeing about what they have already built.
//
// Exported, unlike the Directory encoding, because the key is derived in core
// and the tree is not.
const (
	fieldArguments  = 1 // Command.arguments
	fieldEnv        = 2 // Command.environment_variables
	fieldWorkingDir = 6 // Command.working_directory
	fieldOutputs    = 7 // Command.output_paths

	fieldCommandDigest = 1  // Action.command_digest
	fieldInputRoot     = 2  // Action.input_root_digest
	fieldDoNotCache    = 7  // Action.do_not_cache
	fieldSalt          = 9  // Action.salt
	fieldPlatform      = 10 // Action.platform

	fieldPlatformProps = 1 // Platform.properties
)

// Property is one name/value pair, which both Platform and Command's
// environment use.
type Property struct{ Name, Value string }

// Command is what an action runs.
//
// A step of this engine is not always a command - `file`, `image` and `local`
// operations have no argv - so a caller synthesises one for them and must not
// hand the result to a service that would try to execute it. See
// plan-remote-execution R2b.
type Command struct {
	Arguments        []string
	Env              []Property // in name order, which REAPI requires
	WorkingDirectory string
	OutputPaths      []string // empty until a step can declare what it produces
}

// Action is a Command over an input tree, and its digest is Κₜ.
type Action struct {
	Command     ir.NodeID
	CommandSize int64
	InputRoot   ir.NodeID
	InputSize   int64
	DoNotCache  bool
	// Salt is the cache generation. REAPI has this field (9) so an
	// implementation can retire one, which is exactly ζ's job - so ζ is not
	// approximated by it, it is it.
	Salt     []byte
	Platform []Property // in name order
}

// EncodeCommand writes a Command message.
func EncodeCommand(c Command) []byte {
	var out []byte

	for _, a := range c.Arguments {
		out = appendString(out, fieldArguments, a)
	}

	for _, e := range c.Env {
		out = appendMessage(out, fieldEnv, encodeProperty(e))
	}

	out = appendString(out, fieldWorkingDir, c.WorkingDirectory)

	for _, p := range c.OutputPaths {
		out = appendString(out, fieldOutputs, p)
	}

	return out
}

// EncodeAction writes an Action message.
func EncodeAction(a Action) []byte {
	var out []byte

	out = appendMessage(out, fieldCommandDigest, encodeDigest(&scratch{}, a.Command, a.CommandSize))
	out = appendMessage(out, fieldInputRoot, encodeDigest(&scratch{}, a.InputRoot, a.InputSize))

	if a.DoNotCache {
		out = appendVarintField(out, fieldDoNotCache, 1)
	}

	if len(a.Salt) > 0 {
		out = appendBytes(out, fieldSalt, a.Salt)
	}

	if len(a.Platform) > 0 {
		var props []byte

		for _, p := range a.Platform {
			props = appendMessage(props, fieldPlatformProps, encodeProperty(p))
		}

		out = appendMessage(out, fieldPlatform, props)
	}

	return out
}

// encodeProperty writes a name/value pair, which Platform.Property,
// NodeProperty and Command.EnvironmentVariable all are.
func encodeProperty(p Property) []byte {
	out := appendString(nil, fieldPropertyName, p.Name)

	return appendString(out, fieldPropertyValue, p.Value)
}
