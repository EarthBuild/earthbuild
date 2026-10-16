package guest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
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
	ctx context.Context, id ir.NodeID, stack []ir.NodeID, image string,
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

	if bad := confirmable(a.Platform, image); bad != nil {
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

	// **Somewhere to put what was asked for.** REAPI: "Directories leading up
	// to the output directories (but not the output directories themselves) are
	// created by the worker prior to execution, even if they are not explicitly
	// part of the input root." Bazel relies on it - a genrule writes into
	// `bazel-out/...`, which is in no input root and which bazel never creates -
	// and an action that only materialises what it was sent fails with the
	// shell's `No such file or directory`, a message about the output that says
	// nothing about whose job the directory was.
	if dirErr := makeOutputDirs(mat.Root, cmd.WorkingDirectory, cmd.OutputPaths); dirErr != nil {
		return layer.Result{}, dirErr
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

	// **And the same tree again, inline.** A client that reads `tree_digest`
	// and nothing else refuses a result without one, and cannot be argued with.
	treeID, treeSize, err := st.TreeMessage(made, root)
	if err != nil {
		return layer.Result{}, fmt.Errorf("the tree message for this action: %w", err)
	}

	// **Named one path at a time, because that is what was asked for.** The
	// layer already holds only what was declared (the capture was narrowed);
	// this says which part of it is which, without which a client has the
	// artefacts and no way to find them.
	declared, err := namedOutputs(st, made, cmd.OutputPaths)
	if err != nil {
		return layer.Result{}, err
	}

	// **Recorded under the action's own digest**, which is Κₜ - so a step's
	// results and an action's are one key space and one store, whether the work
	// came from an Earthfile or from a client inside one (green paper 4.5a).
	// Without this the service answers correctly and remembers nothing, and a
	// client that has just built something is told to build it again.
	s.recordAction(id, a, made, root, ran)

	return layer.Result{
		Root:     root,
		RootSize: size,
		Tree:     treeID,
		TreeSize: treeSize,
		Declared: declared,
		ExitCode: int32(ran.Exit), //nolint:gosec // an exit status
		Stdout:   []byte(ran.Output),
	}, nil
}

// propContainerImage is REAPI's conventional name for the image an action runs
// in. Every client that names one names it here, and the property is part of
// the Platform, which is part of the Action, which is the key.
const propContainerImage = "container-image"

// confirmable refuses an action that asks for an environment this is not.
//
// **Which is just the FROM line, and that is the whole mechanism.** The engine
// already resolved a reference to the stack this step runs on - memoised on
// (reference, platform), pinned before it reached the key (I17) - so the image
// an action may name is the one the step it is asking from stands on, and the
// host says which that was. There is nothing to look up and no table to keep.
//
// An action naming a different image is refused rather than run here, because
// its image is part of its Platform, which is part of its Action, which is its
// key: running it over the base to hand and filing the result under the image
// it named produces a cache entry describing an environment the work never ran
// in, and every later build legitimately using that image would be served it
// (I3). There is no base this guest could substitute that would make the key
// true - it holds layers by digest and has no registry - so a refusal is the
// only honest answer, not a placeholder for one.
//
// An empty `image` is a step whose base the engine could not name, and then
// nothing can be confirmed. Refusing is the same rule with less to say.
func confirmable(platform []layer.Property, image string) error {
	for _, p := range platform {
		if p.Name != propContainerImage {
			continue
		}

		// `docker://` is REAPI's conventional scheme on a reference that is
		// otherwise spelled as any registry client spells it.
		if asks := strings.TrimPrefix(p.Value, "docker://"); asks == image && image != "" {
			continue
		}

		if image == "" {
			return fmt.Errorf(
				"this action asks to run in %s, and this engine cannot say what the"+
					" step asking on its behalf stands on"+
					"\n  so it cannot tell whether that is the same image, and running it"+
					"\n  would file the result under a key naming an environment the work"+
					"\n  may never have run in",
				p.Value)
		}

		return fmt.Errorf(
			"this action asks to run in %s and this step stands on %s"+
				"\n  they are not the same image, and this engine holds layers by digest"+
				"\n  with no registry, so it cannot fetch the one asked for"+
				"\n  running it in this one would file the result under a key naming an"+
				"\n  environment the work never ran in, which every later build using"+
				"\n  that image would then be served"+
				"\n  give the target a FROM naming the image the actions want, or send"+
				"\n  the action with no %s property to accept this one",
			p.Value, image, propContainerImage)
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

// namedOutputs is each declared path, named as REAPI names it, with the Tree
// blob of any declared directory kept where a client can fetch it.
func namedOutputs(st store.DirStore, id ir.NodeID, paths []string) (layer.Declared, error) {
	if len(paths) == 0 {
		return layer.Declared{}, nil
	}

	m, ok, err := store.ReadManifest(string(st), id)
	if err != nil || !ok {
		return layer.Declared{}, fmt.Errorf(
			"no manifest for the layer under %s, so what it produced cannot be named", id)
	}

	declared, err := layer.Outputs(m, paths)
	if err != nil {
		return layer.Declared{}, err
	}

	// **Fetchable, at the price of a link.** A client materialises the outputs
	// it was told about by asking for them by digest, and a layer's files are
	// not addressable that way. Done here rather than when the client asks
	// because this is where the layer and the path are both known, and because
	// a link costs nothing: with deferred materialisation most outputs are
	// never fetched, and it is not worth knowing which.
	for _, f := range declared.Files {
		if linkErr := st.LinkBlob(id, f.Path, f.Digest); linkErr != nil {
			return layer.Declared{}, fmt.Errorf("make %s fetchable: %w", f.Path, linkErr)
		}
	}

	// A client fetches a directory's Tree by digest a moment after reading it,
	// so it is filed now rather than rebuilt then.
	for _, d := range declared.Dirs {
		for blobID, b := range d.Nodes {
			if acceptErr := st.Accept(blobID, b); acceptErr != nil {
				return layer.Declared{}, fmt.Errorf("keep the tree for %s: %w", d.Path, acceptErr)
			}
		}
	}

	return declared, nil
}

// recordAction files what an action produced, under the action's own digest.
//
// **Only a success, and only where the action allows it.** REAPI's action cache
// holds results a client may be given instead of running the work; a failure is
// a fact about one run and not about the action, and `do_not_cache` is a client
// saying so itself. Both are reasons to remember nothing rather than to
// remember something with an asterisk.
//
// Best effort: an action that ran and could not be recorded has still run, and
// the client is owed its result either way.
func (s *Server) recordAction(
	id ir.NodeID, a layer.Action, made, content ir.NodeID, ran Response,
) {
	c := s.actionCache()
	if c == nil || a.DoNotCache || ran.Exit != 0 {
		return
	}

	c.Put(core.Key(id), core.Entry{
		Layer:   made,
		Content: content,
		Exit:    ran.Exit,
		Stdout:  ran.Output,
		// Whole, because the guest bounds a step's output and hands back what
		// it kept: a caller needing to tell "printed nothing" from "printed too
		// much" needs the entry, and this is the entry.
		StdoutWhole: true,
	})
}

// makeOutputDirs creates the directories an action's declared outputs sit in.
//
// The output itself is not created: its kind is the action's to decide, and a
// directory made here would have a command that meant to write a file finding
// one already there.
func makeOutputDirs(root, workdir string, paths []string) error {
	for _, p := range paths {
		clean := path.Clean(strings.TrimPrefix(p, "/"))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf(
				"%q is a declared output and reaches outside the action's tree", p)
		}

		at := filepath.Join(root, path.Clean("/"+workdir), clean)

		//nolint:gosec // a directory the action writes into
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			return fmt.Errorf("make somewhere for the declared output %s: %w", p, err)
		}
	}

	return nil
}
