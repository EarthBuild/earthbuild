package fleet

import (
	"errors"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ErrNotDelegable marks a step that cannot be sent to a worker.
//
// A refusal rather than a degradation, and the distinction is the point: the
// step is still built, here, by the machine that has what it needs. What is
// refused is *delegation*, not the work.
var ErrNotDelegable = errors.New("this step cannot be delegated")

// Delegate turns a step into an assignment, or refuses it.
//
// The conversion is where the wire's poorer vocabulary is enforced. It reads as
// bureaucracy and is the security property: `fleet.Op` has no word for `host`,
// so the only way to handle `ir.OpHost` here is to refuse it, and there is
// nowhere to write the mistake.
//
// **Poverty is a refusal, not a filter.** A step carrying a secret, a mount or a
// docker daemon cannot be described in an assignment, and the tempting answer -
// send what fits - would hand a worker a step missing an input it depends on.
// That is a wrong answer rather than a slow one, which is the failure this
// engine exists to prevent (I3).
func Delegate(n *ir.Node, base []ir.NodeID, sources [][]ir.NodeID) (Assignment, error) {
	kind, err := kindOf(n.Op.Kind)
	if err != nil {
		return Assignment{}, err
	}

	err = expressible(n.Op)
	if err != nil {
		return Assignment{}, err
	}

	return Assignment{
		Version: Version,
		Base:    base,
		Sources: sources,
		Op: Op{
			Kind: kind,
			Args: n.Op.Args,
			Env:  n.Op.Env,
			Dir:  n.Op.Dir, User: n.Op.User,
			NoNetwork: n.Op.NoNetwork,
			Scratch:   scratch(n.Op.Mounts),
		},
		Platform: n.Platform.String(),
	}, nil
}

// kindOf is the wire's word for an operation, if it has one.
//
// A switch with no default that guesses. Every kind the IR has appears here, and
// a new one fails to compile into a wire kind rather than arriving as an empty
// string - which is the same decision the type forces one level up.
func kindOf(k ir.OpKind) (Kind, error) {
	switch k {
	case ir.OpImage:
		return KindImage, nil

	case ir.OpExec:
		return KindExec, nil

	case ir.OpFile:
		return KindFile, nil

	case ir.OpBuild:
		return KindBuild, nil

	case ir.OpHost:
		// The one C.3 names. A delegate is not the invoking machine, so it
		// cannot satisfy host locality; the wire has no word for this and that
		// is deliberate.
		return "", fmt.Errorf("%w: %s runs on the invoking machine, which a"+
			" worker is not", ErrNotDelegable, k)

	case ir.OpLocal:
		return "", fmt.Errorf("%w: %s reads the invoking machine's filesystem,"+
			" which a worker cannot see", ErrNotDelegable, k)

	case ir.OpMerge:
		return "", fmt.Errorf("%w: %s is not in the wire vocabulary", ErrNotDelegable, k)

	case ir.OpPackImage:
		return "", fmt.Errorf("%w: %s writes into this machine's layer store",
			ErrNotDelegable, k)

	case ir.OpScratch:
		// The empty base. Expressible in principle - a worker could produce
		// nothing as well as anybody - and refused because shipping it costs a
		// round trip for no work, which is a decision rather than a gap (E468).
		return "", fmt.Errorf("%w: %s produces nothing, so sending it costs a"+
			" round trip and saves none", ErrNotDelegable, k)

	default:
		return "", fmt.Errorf("%w: %s is an operation this engine has no wire"+
			" word for\n  a new opcode must be decided delegable or refused",
			ErrNotDelegable, k)
	}
}

// expressible reports whether an assignment can carry everything this step needs.
func expressible(op ir.Op) error {
	// The list lives in `ir` and the scheduler reads the same one.
	//
	// This is the guarantee - a driver that sent one of these anyway would
	// produce a step failing for a reason nobody can see - and `eligibleFor` is
	// the model of it used at placement time. They were separate lists and
	// disagreed about three of the five, so the schedule charged workers for
	// work they would refuse (E430).
	if only, why := op.OnInvokerOnly(); only {
		return fmt.Errorf("%w: %s", ErrNotDelegable, why)
	}

	return nil
}

// scratch is the targets of the mounts a worker can make for itself.
//
// Reached only after `expressible` has passed, where any mount that is not a
// private cache has already refused the step - so this is a projection and not a
// filter. Written as its own function so that the difference is visible: a
// filter here would be the send-what-fits answer, and it would send a step
// missing an input.
func scratch(mounts []ir.Mount) []string {
	out := make([]string, 0, len(mounts))

	for _, m := range mounts {
		out = append(out, m.Target)
	}

	return out
}
