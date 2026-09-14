package guest

import (
	"context"
	"errors"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// RunAction executes one REAPI Action over a stack and reports what it produced.
//
// **An action is a Step, and this does not invent a second way to run one.** A
// base, a small tree over it, and a command: the three requests this guest
// already answers are materialise, exec and capture, so that is what this
// sends. Anything an action gains later - a cgroup bound, a network refused, an
// observation recorded - it gains because a step gained it, which is the only
// way the two stay the same thing.
//
// The stack is the caller's because the guest cannot resolve an image: it holds
// layers by digest and has no registry. Whoever starts the step that a client
// is running inside already knows which layers that step stands on, and that is
// the environment an action gets (plan-remote-execution R5).
//
// Every blob comes from this store, which is where the client put them:
// REAPI's contract is that a client sends its inputs and then asks. One that
// asked too early is told which blob is missing, rather than having an empty
// command succeed and be cached as this action's answer.
func (s *Server) RunAction(
	ctx context.Context, id ir.NodeID, stack []ir.NodeID,
) (layer.Result, error) {
	if s.LayerDir == "" {
		return layer.Result{}, errors.New(
			"this guest has no layer store, so there is nowhere an action's blobs could be")
	}

	st := store.DirStore(s.LayerDir)

	a, err := actionIn(st, id)
	if err != nil {
		return layer.Result{}, err
	}

	if bad := confirmable(a.Platform); bad != nil {
		return layer.Result{}, bad
	}

	cb, err := st.Node(a.Command)
	if err != nil {
		return layer.Result{}, fmt.Errorf(
			"the action's command %s is not in this store"+
				"\n  ask FindMissingBlobs and send what it names before asking for an"+
				"\n  action to be executed: %w", a.Command, err)
	}

	cmd, err := layer.CommandIn(cb)
	if err != nil {
		return layer.Result{}, fmt.Errorf("read the command %s: %w", a.Command, err)
	}

	if len(cmd.Arguments) == 0 {
		return layer.Result{}, fmt.Errorf(
			"the command %s has no arguments, so there is nothing to run", a.Command)
	}

	mat := s.handle(ctx, Request{Kind: KindMaterialise, Stack: encodeStack(stack)}, nil)
	if mat.Err != "" {
		return layer.Result{}, fmt.Errorf("materialise the action's base: %s", mat.Err)
	}

	// Released whatever happens: a handle is a mount, and one left behind
	// outlives the build that made it.
	defer s.handle(ctx, Request{Kind: KindRelease, Handle: mat.Handle}, nil)

	// **Over the base, not beside it.** The input root holds the action's own
	// inputs and the environment comes from the stack, which is the practice
	// every client follows - so this writes into the filesystem the step sees,
	// exactly as a COPY does, and the writes land in the delta.
	if matErr := st.Materialise(a.InputRoot, mat.Root); matErr != nil {
		return layer.Result{}, fmt.Errorf("materialise the input root: %w", matErr)
	}

	env := make([]string, 0, len(cmd.Env))
	for _, e := range cmd.Env {
		env = append(env, e.Name+"="+e.Value)
	}

	ran := s.handle(ctx, Request{
		Kind:    KindExec,
		Handle:  mat.Handle,
		Argv:    cmd.Arguments,
		Env:     env,
		Dir:     cmd.WorkingDirectory,
		Outputs: cmd.OutputPaths,
	}, nil)
	if ran.Err != "" {
		return layer.Result{}, errors.New(ran.Err)
	}

	// **Captured even when the command failed.** A non-zero exit is a result
	// and not an error (REAPI says so, and so does this guest's own protocol):
	// a client wants the exit code and whatever was written, and a build that
	// discarded the evidence of a failure would be harder to debug than one
	// with no cache at all.
	//
	// Narrowed to output_paths here rather than afterwards, which is R4's
	// `RUN --output` under its REAPI name: the name is what is being
	// stabilised, so a layer filtered after it was named is the wrong layer.
	took := s.handle(ctx, Request{
		Kind:    KindCapture,
		Handle:  mat.Handle,
		Outputs: cmd.OutputPaths,
	}, nil)
	if took.Err != "" {
		return layer.Result{}, fmt.Errorf("capture what the action produced: %s", took.Err)
	}

	root, err := ir.ParseNodeID(took.Content)
	if err != nil {
		return layer.Result{}, fmt.Errorf("the capture named %q: %w", took.Content, err)
	}

	made, err := ir.ParseNodeID(took.Layer)
	if err != nil {
		return layer.Result{}, fmt.Errorf("the capture named the layer %q: %w", took.Layer, err)
	}

	// **The client is about to fetch this tree, so it has to be in the store.**
	// A capture writes a layer and a manifest; the Directory nodes a peer reads
	// are folded when somebody asks, because doing it on the capture path cost
	// ninety times what capturing does. This is somebody asking.
	size, err := st.TreeNodes(made, root)
	if err != nil {
		return layer.Result{}, fmt.Errorf("the tree this action produced: %w", err)
	}

	return layer.Result{
		Root:     root,
		RootSize: size,
		ExitCode: int32(ran.Exit), //nolint:gosec // an exit status
		Stdout:   []byte(ran.Output),
	}, nil
}

// propContainerImage is REAPI's conventional name for the image an action runs
// in. Every client that names one names it here, and the property is part of
// the Platform, which is part of the Action, which is the key.
const propContainerImage = "container-image"

// confirmable refuses an action whose environment this guest cannot vouch for.
//
// **The one false hit that is a line of code away.** An action's image is part
// of its key, so running it over whatever base the calling step happens to have
// and filing the result under the image it *named* produces a cache entry
// describing an environment the work never ran in - and every later build that
// legitimately uses that image gets it (I3).
//
// This guest holds layers by digest and has no registry, so it can confirm
// nothing and refuses everything that asks. That is the answer rather than a
// placeholder: there is no base it could substitute that would make the key
// true, and the alternative is a wrong answer nothing downstream can detect.
// When it can resolve a reference to a stack, this becomes a lookup.
func confirmable(platform []layer.Property) error {
	for _, p := range platform {
		if p.Name != propContainerImage {
			continue
		}

		return fmt.Errorf(
			"this action asks to run in %s, and this engine cannot confirm that"+
				"\n  it holds layers by digest and has no registry, so it cannot tell"+
				"\n  whether the environment it would run this in is that image"+
				"\n  running it anyway would file the result under a key naming an"+
				"\n  environment the work never ran in, which every later build using"+
				"\n  that image would then be served"+
				"\n  send the action without a %s property to run it in the environment"+
				"\n  of the step that is asking",
			p.Value, propContainerImage)
	}

	return nil
}

// actionIn reads the Action a digest names.
func actionIn(st store.DirStore, id ir.NodeID) (layer.Action, error) {
	b, err := st.Node(id)
	if err != nil {
		return layer.Action{}, fmt.Errorf(
			"the action %s is not in this store"+
				"\n  send it with BatchUpdateBlobs before asking for it to be executed: %w",
			id, err)
	}

	a, err := layer.ActionIn(b)
	if err != nil {
		return layer.Action{}, fmt.Errorf("read the action %s: %w", id, err)
	}

	return a, nil
}
