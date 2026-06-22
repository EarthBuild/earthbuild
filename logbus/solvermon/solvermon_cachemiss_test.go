package solvermon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/util/buildkitskipper"
	"github.com/EarthBuild/earthbuild/util/vertexmeta"
	"github.com/moby/buildkit/client"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// vertexMetaPrefix encodes a VertexMeta as a BuildKit vertex name prefix so
// that ParseFromVertexPrefix can parse it back.
func vertexMetaPrefix(vm *vertexmeta.VertexMeta, operation string) string {
	dt, err := json.Marshal(vm)
	if err != nil {
		panic(err)
	}

	return "[" + base64.StdEncoding.EncodeToString(dt) + "] " + operation
}

// sha returns a deterministic digest for a given string label.
func sha(label string) digest.Digest {
	return digest.FromString(label)
}

// makeMonitorWithState builds a SolverMonitor whose prevState is seeded from
// the given records, without touching the filesystem.
func makeMonitorWithState(records []buildkitskipper.VertexRecord) *SolverMonitor {
	sm := &SolverMonitor{
		prevState: make(map[string]buildkitskipper.VertexRecord),
	}

	for _, r := range records {
		sm.prevState[r.Digest] = r
	}

	return sm
}

// ---------------------------------------------------------------------------
// buildCacheMissMessage
// ---------------------------------------------------------------------------

func TestBuildCacheMissMessage_NoHistory(t *testing.T) {
	t.Parallel()

	sm := makeMonitorWithState(nil)
	v := &client.Vertex{Digest: sha("new-op"), Inputs: nil}
	require.Empty(t, sm.buildCacheMissReason(v, &vertexmeta.VertexMeta{}))
}

func TestBuildCacheMissMessage_PreviouslyMiss(t *testing.T) {
	t.Parallel()

	d := sha("op-a")
	sm := makeMonitorWithState([]buildkitskipper.VertexRecord{
		{Digest: d.String(), Operation: "RUN echo hello", WasCached: false},
	})
	v := &client.Vertex{Digest: d, Inputs: nil}
	require.Empty(t, sm.buildCacheMissReason(v, &vertexmeta.VertexMeta{}))
}

func TestBuildCacheMissMessage_PreviouslyCached_NoInputs(t *testing.T) {
	t.Parallel()

	d := sha("op-b")
	sm := makeMonitorWithState([]buildkitskipper.VertexRecord{
		{Digest: d.String(), Operation: "RUN apt-get update", WasCached: true},
	})
	v := &client.Vertex{Digest: d, Inputs: nil}
	msg := sm.buildCacheMissReason(v, &vertexmeta.VertexMeta{})
	require.Contains(t, msg, "previously cached")
}

func TestBuildCacheMissMessage_PreviouslyCached_ArgChanged(t *testing.T) {
	t.Parallel()

	d := sha("op-arg")
	sm := makeMonitorWithState([]buildkitskipper.VertexRecord{
		{
			Digest:     d.String(),
			Operation:  "RUN go build",
			WasCached:  true,
			ActiveArgs: map[string]string{"GO_VERSION": "1.21"},
		},
	})
	v := &client.Vertex{Digest: d, Inputs: nil}
	meta := &vertexmeta.VertexMeta{ActiveArgs: map[string]string{"GO_VERSION": "1.22"}}
	msg := sm.buildCacheMissReason(v, meta)
	require.Contains(t, msg, "previously cached")
	require.Contains(t, msg, "arg changed")
	require.Contains(t, msg, "GO_VERSION")
	require.Contains(t, msg, "1.21")
	require.Contains(t, msg, "1.22")
}

func TestBuildCacheMissMessage_PreviouslyCached_InputChanged(t *testing.T) {
	t.Parallel()

	parentDigest := sha("parent-op")
	childDigest := sha("child-op")

	sm := makeMonitorWithState([]buildkitskipper.VertexRecord{
		{Digest: parentDigest.String(), Operation: "COPY ./src /src", WasCached: false},
		{Digest: childDigest.String(), Operation: "RUN go build", WasCached: true},
	})

	v := &client.Vertex{Digest: childDigest, Inputs: []digest.Digest{parentDigest}}
	msg := sm.buildCacheMissReason(v, &vertexmeta.VertexMeta{})
	require.Contains(t, msg, "previously cached")
	require.Contains(t, msg, "COPY ./src /src")
}

func TestBuildCacheMissMessage_PreviouslyCached_InputNewlyAdded(t *testing.T) {
	t.Parallel()

	childDigest := sha("child-op2")
	sm := makeMonitorWithState([]buildkitskipper.VertexRecord{
		{Digest: childDigest.String(), Operation: "RUN make", WasCached: true},
	})

	v := &client.Vertex{Digest: childDigest, Inputs: []digest.Digest{sha("new-input")}}
	msg := sm.buildCacheMissReason(v, &vertexmeta.VertexMeta{})
	require.Contains(t, msg, "previously cached")
}

func TestBuildCacheMissMessage_AllInputsPreviouslyCached(t *testing.T) {
	t.Parallel()

	inputDigest := sha("stable-input")
	childDigest := sha("child-op3")

	sm := makeMonitorWithState([]buildkitskipper.VertexRecord{
		{Digest: inputDigest.String(), Operation: "FROM alpine", WasCached: true},
		{Digest: childDigest.String(), Operation: "RUN echo old", WasCached: true},
	})

	v := &client.Vertex{Digest: childDigest, Inputs: []digest.Digest{inputDigest}}
	msg := sm.buildCacheMissReason(v, &vertexmeta.VertexMeta{})
	require.Contains(t, msg, "previously cached")
}

// ---------------------------------------------------------------------------
// handleBuildkitStatus — filtering: only earth-managed vertices get annotated
// ---------------------------------------------------------------------------

// exportingOutputsVertex is the BuildKit vertex name used by the exporter,
// which has no VertexMeta prefix and must never receive cache-miss annotations.
const exportingOutputsVertex = "exporting outputs"

// newTestBus creates a minimal logbus.Bus for testing.
func newTestBus(t *testing.T) *logbus.Bus {
	t.Helper()

	return logbus.New()
}

func TestHandleBuildkitStatus_SkipsNonEarthVertices(t *testing.T) {
	t.Parallel()

	// Vertices without a CommandID (exporter, context, cache vertices) must
	// never produce a cache-miss annotation even when they were "seen before".
	b := newTestBus(t)
	sm := &SolverMonitor{
		b:         b,
		digests:   make(map[digest.Digest]string),
		vertices:  make(map[string]*vertexMonitor),
		prevState: make(map[string]buildkitskipper.VertexRecord),
	}

	exporterDigest := sha("exporting-outputs")

	// Seed prevState as if this digest was previously cached
	sm.prevState[exporterDigest.String()] = buildkitskipper.VertexRecord{
		Digest:    exporterDigest.String(),
		Operation: exportingOutputsVertex,
		WasCached: true,
	}

	now := time.Now()

	status := &client.SolveStatus{
		Vertexes: []*client.Vertex{
			{
				Name:    exportingOutputsVertex,
				Digest:  exporterDigest,
				Started: &now,
				Cached:  false,
			},
		},
	}

	err := sm.handleBuildkitStatus(status)
	require.NoError(t, err)

	// The vertex monitor must NOT have cacheMissLogged set
	for _, vm := range sm.vertices {
		require.False(t, vm.cacheMissLogged,
			"exporter vertex should not have cache miss logged")
	}
}

func TestHandleBuildkitStatus_AnnotatesEarthVertex(t *testing.T) {
	t.Parallel()

	b := newTestBus(t)

	// Pre-create the logbus command as the converter would.
	run := b.Run()

	cmdID := "target-1/cmd-0"
	targetID := "target-1"

	_, err := run.NewTarget(targetID, domain.Target{}, nil, "", "")
	require.NoError(t, err)

	_, err = run.NewCommand(cmdID, "RUN echo hello", targetID, "+my-target", "", false, false, false, nil, "", "", "")
	require.NoError(t, err)

	sm := &SolverMonitor{
		b:         b,
		digests:   make(map[digest.Digest]string),
		vertices:  make(map[string]*vertexMonitor),
		prevState: make(map[string]buildkitskipper.VertexRecord),
	}

	opDigest := sha("run-echo-hello")

	// Seed: this vertex was previously cached — verifies the regression suffix fires.
	sm.prevState[opDigest.String()] = buildkitskipper.VertexRecord{
		Digest:    opDigest.String(),
		Operation: "RUN echo hello",
		WasCached: true,
	}

	now := time.Now()

	// Build a vertex name with a proper VertexMeta (as the converter embeds)
	vm := &vertexmeta.VertexMeta{
		CommandID:  cmdID,
		TargetID:   targetID,
		TargetName: "+my-target",
	}
	vertexName := vertexMetaPrefix(vm, "RUN echo hello")

	status := &client.SolveStatus{
		Vertexes: []*client.Vertex{
			{
				Name:    vertexName,
				Digest:  opDigest,
				Started: &now,
				Cached:  false,
			},
		},
	}

	err = sm.handleBuildkitStatus(status)
	require.NoError(t, err)

	// The vertex monitor must have cacheMissLogged set
	for _, mon := range sm.vertices {
		if mon.meta.CommandID == cmdID {
			require.True(t, mon.cacheMissLogged,
				"earth-managed vertex should have cache miss logged")

			return
		}
	}

	t.Fatal("expected to find the earth-managed vertex monitor")
}

func TestHandleBuildkitStatus_AnnotatesEarthVertex_NoDB(t *testing.T) {
	t.Parallel()

	// Verifies that cache miss is annotated even without any prior DB state
	// (first run, no --auto-skip-db-path).
	b := newTestBus(t)
	run := b.Run()

	cmdID := "target-2/cmd-0"
	targetID := "target-2"

	_, err := run.NewTarget(targetID, domain.Target{}, nil, "", "")
	require.NoError(t, err)

	_, err = run.NewCommand(cmdID, "RUN echo world", targetID, "+my-target", "", false, false, false, nil, "", "", "")
	require.NoError(t, err)

	sm := &SolverMonitor{
		b:         b,
		digests:   make(map[digest.Digest]string),
		vertices:  make(map[string]*vertexMonitor),
		prevState: make(map[string]buildkitskipper.VertexRecord), // empty — no DB
	}

	opDigest := sha("run-echo-world")
	now := time.Now()

	vm := &vertexmeta.VertexMeta{
		CommandID:  cmdID,
		TargetID:   targetID,
		TargetName: "+my-target",
	}

	status := &client.SolveStatus{
		Vertexes: []*client.Vertex{
			{
				Name:    vertexMetaPrefix(vm, "RUN echo world"),
				Digest:  opDigest,
				Started: &now,
				Cached:  false,
			},
		},
	}

	err = sm.handleBuildkitStatus(status)
	require.NoError(t, err)

	for _, mon := range sm.vertices {
		if mon.meta.CommandID == cmdID {
			require.True(t, mon.cacheMissLogged,
				"earth-managed vertex must be annotated even without prior DB state")

			return
		}
	}

	t.Fatal("expected to find the earth-managed vertex monitor")
}

// ---------------------------------------------------------------------------
// VertexRecord collection
// ---------------------------------------------------------------------------

func TestHandleBuildkitStatus_CollectsRecordsOnCompletion(t *testing.T) {
	t.Parallel()

	b := newTestBus(t)
	sm := &SolverMonitor{
		b:         b,
		digests:   make(map[digest.Digest]string),
		vertices:  make(map[string]*vertexMonitor),
		prevState: make(map[string]buildkitskipper.VertexRecord),
	}

	d := sha("op-collect")
	now := time.Now()
	completed := now.Add(time.Second)

	status := &client.SolveStatus{
		Vertexes: []*client.Vertex{
			{
				Name:      exportingOutputsVertex,
				Digest:    d,
				Started:   &now,
				Completed: &completed,
				Cached:    true,
			},
		},
	}

	err := sm.handleBuildkitStatus(status)
	require.NoError(t, err)
	require.Len(t, sm.collected, 1)
	require.Equal(t, d.String(), sm.collected[0].Digest)
	require.True(t, sm.collected[0].WasCached)
}

// ---------------------------------------------------------------------------
// VertexStateStore: SaveState / LoadState round-trip
// ---------------------------------------------------------------------------

func TestVertexStateStore_RoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	store, err := buildkitskipper.NewLocal(dir + "/test.db")
	require.NoError(t, err)

	vss := store.VertexStateStore()
	ctx := context.Background()

	records := []buildkitskipper.VertexRecord{
		{Digest: sha("a").String(), Operation: "FROM alpine", Inputs: nil, WasCached: true},
		{Digest: sha("b").String(), Operation: "RUN apt-get update", Inputs: []string{sha("a").String()}, WasCached: false},
	}

	err = vss.SaveState(ctx, "./+my-target", records)
	require.NoError(t, err)

	loaded, err := vss.LoadState(ctx, "./+my-target")
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	require.Equal(t, records[0].Digest, loaded[0].Digest)
	require.Equal(t, records[0].Operation, loaded[0].Operation)
	require.True(t, loaded[0].WasCached)
	require.Equal(t, records[1].Operation, loaded[1].Operation)
	require.False(t, loaded[1].WasCached)
	require.Equal(t, records[1].Inputs, loaded[1].Inputs)
}

func TestVertexStateStore_MissingTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	store, err := buildkitskipper.NewLocal(dir + "/test.db")
	require.NoError(t, err)

	loaded, err := store.VertexStateStore().LoadState(context.Background(), "./+nonexistent")
	require.NoError(t, err)
	require.Nil(t, loaded)
}
