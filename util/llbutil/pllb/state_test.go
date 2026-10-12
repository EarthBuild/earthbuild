package pllb

import (
	"bytes"
	"slices"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
)

// copyRunFromLocal builds the shape of an Earthfile target that COPYs from
// the local build context and then RUNs a command on the result.
func copyRunFromLocal() State {
	local := Local("context", llb.SharedKeyHint("context"))

	return Image("alpine:3.20").
		File(Copy(local, "/file", "/file")).
		Run(llb.Shlex("cat /file")).
		Root()
}

// lastOpDigest returns the digest of the terminal op of a definition, which
// identifies the vertex that the definition solves.
func lastOpDigest(t *testing.T, def *llb.Definition) digest.Digest {
	t.Helper()

	if len(def.Def) == 0 {
		t.Fatal("definition has no ops")
	}

	return digest.FromBytes(def.Def[len(def.Def)-1])
}

// A single earth process can solve the same state several times within one
// build (async forced execution, a wait block, the builder's main ref). If each
// solve gets different vertex digests for what is the same work, BuildKit
// merges the edges by cache key and replays the vertex logs once per extra
// solve, so the output of a RUN after a COPY from the build context is printed
// several times.
func TestMarshalIsStableForLocalSources(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := copyRunFromLocal()

	first, err := st.Marshal(ctx)
	if err != nil {
		t.Fatalf("first marshal: %v", err)
	}

	second, err := st.Marshal(ctx)
	if err != nil {
		t.Fatalf("second marshal: %v", err)
	}

	got, want := lastOpDigest(t, second), lastOpDigest(t, first)
	if got != want {
		t.Errorf("terminal op digest changed between marshals: got %s, want %s", got, want)
	}

	if !slices.EqualFunc(second.Def, first.Def, bytes.Equal) {
		t.Error("definitions differ between marshals of the same state")
	}
}

func TestMarshalLetsCallersOverrideLocalUniqueID(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := copyRunFromLocal()

	def, err := st.Marshal(ctx)
	if err != nil {
		t.Fatalf("default marshal: %v", err)
	}

	overridden, err := st.Marshal(ctx, llb.LocalUniqueID("caller-chosen"))
	if err != nil {
		t.Fatalf("overridden marshal: %v", err)
	}

	if lastOpDigest(t, overridden) == lastOpDigest(t, def) {
		t.Error("a caller-supplied llb.LocalUniqueID did not change the digest")
	}

	again, err := st.Marshal(ctx, llb.LocalUniqueID("caller-chosen"))
	if err != nil {
		t.Fatalf("second overridden marshal: %v", err)
	}

	if got, want := lastOpDigest(t, again), lastOpDigest(t, overridden); got != want {
		t.Errorf("same caller-supplied llb.LocalUniqueID gave different digests: got %s, want %s", got, want)
	}
}
