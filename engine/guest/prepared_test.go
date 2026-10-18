package guest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// refusing materialiser: it must not be reached when a base is already prepared.
var errPreparedInstead = errors.New("this base was already assembled")

type refusingMat struct{ called int }

func (m *refusingMat) Materialise(context.Context, []ir.NodeID) (core.Handle, error) {
	m.called++

	return nil, errPreparedInstead
}

// A base that is already assembled is used as it is.
//
// **The seam a lazy base needs.** Every base until now was a stack of layers the
// guest assembled; a lazily materialised one is a directory somebody else
// primed with the paths a step was predicted to read (E292), and it is not a
// layer - a fragment never is (E281).
//
// So the request says so explicitly, rather than the guest guessing from a stack
// of one: a prepared root is a *materialisation strategy* arriving as a fact,
// and passing it as a layer id would be passing a lie the cache would key on.
func TestABaseThatIsAlreadyAssembledIsUsedAsItIs(t *testing.T) {
	t.Parallel()

	prepared := t.TempDir()

	err := os.WriteFile(filepath.Join(prepared, "hello"), []byte("primed\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	mat := &refusingMat{}
	s := &Server{Mat: mat, LayerDir: t.TempDir()}

	resp := s.handle(context.Background(), Request{
		ID:       1,
		Kind:     KindMaterialise,
		Prepared: prepared,
	}, nil)

	if resp.Err != "" {
		t.Fatalf("a prepared base was refused: %s", resp.Err)
	}

	if mat.called != 0 {
		t.Error("the layer materialiser was asked to assemble a base that was" +
			" already assembled")
	}

	body, err := os.ReadFile(filepath.Join(resp.Root, "hello"))
	if err != nil {
		t.Fatalf("the prepared base is not where the step will look: %v", err)
	}

	if string(body) != "primed\n" {
		t.Errorf("it reads as %q", body)
	}
}

// A prepared base still keeps the step's writes apart.
//
// The delta is what a step produces, and a prepared base is what it reads. They
// must not be the same directory or the layer would contain its own base -
// which is the thing `TakeExcluding` exists to prevent afterwards, and preventing
// it here as well costs nothing (E293, E300).
func TestAPreparedBaseStillKeepsTheStepsWritesApart(t *testing.T) {
	t.Parallel()

	prepared := t.TempDir()

	s := &Server{Mat: &refusingMat{}, LayerDir: t.TempDir()}

	resp := s.handle(context.Background(), Request{
		ID:       1,
		Kind:     KindMaterialise,
		Prepared: prepared,
	}, nil)

	if resp.Err != "" {
		t.Fatal(resp.Err)
	}

	h, ok := s.get(resp.Handle)
	if !ok {
		t.Fatal("no handle")
	}

	if h.Delta() == h.Root() {
		t.Error("a step's writes land in its base" +
			"\n  the layer it produces would contain the base it read")
	}
}

// Both a stack and a prepared root is a request nobody can honour.
//
// Refused rather than resolved by precedence: the two say different things about
// where a step's filesystem comes from, and a caller that sent both does not know
// which it wants (I10).
func TestAStackAndAPreparedRootTogetherIsRefused(t *testing.T) {
	t.Parallel()

	s := &Server{Mat: &refusingMat{}, LayerDir: t.TempDir()}

	resp := s.handle(context.Background(), Request{
		ID:       1,
		Kind:     KindMaterialise,
		Stack:    []string{"00"},
		Prepared: t.TempDir(),
	}, nil)

	if resp.Err == "" {
		t.Fatal("a request naming both a stack and a prepared root was honoured")
	}
}
