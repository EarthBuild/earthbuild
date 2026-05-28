// Package solvermon monitors the progress of buildkit solvers, tracking operations and identifying fatal errors.
package solvermon

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logstream"
	"github.com/EarthBuild/earthbuild/util/buildkitskipper"
	"github.com/EarthBuild/earthbuild/util/statsstreamparser"
	"github.com/EarthBuild/earthbuild/util/stringutil"
	"github.com/EarthBuild/earthbuild/util/vertexmeta"
	"github.com/EarthBuild/earthbuild/util/xcontext"
	"github.com/moby/buildkit/client"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// SolverMonitor is a buildkit solver monitor.
type SolverMonitor struct {
	b          *logbus.Bus
	digests    map[digest.Digest]string  // digest -> cmdID
	vertices   map[string]*vertexMonitor // cmdID -> vertexMonitor
	store      buildkitskipper.VertexStateStore
	targetName string
	prevState  map[string]buildkitskipper.VertexRecord // digest -> VertexRecord from last run
	collected  []buildkitskipper.VertexRecord
	// hashLogDiff contains human-readable lines describing what changed in the
	// Earthfile inputs since the last run. Set via SetHashLogDiff before the
	// build starts; shown on the first *cache miss* when no vertex-level reason
	// is found. Cleared after first use to avoid repeating on every miss.
	hashLogDiff []string
	mu          sync.Mutex
}

// New creates a new SolverMonitor.
// store and target are optional; pass nil and "" to disable vertex-state tracking.
func New(ctx context.Context, b *logbus.Bus, store buildkitskipper.VertexStateStore, target string) *SolverMonitor {
	sm := &SolverMonitor{
		b:          b,
		digests:    make(map[digest.Digest]string),
		vertices:   make(map[string]*vertexMonitor),
		store:      store,
		targetName: target,
		prevState:  make(map[string]buildkitskipper.VertexRecord),
	}

	if store != nil && target != "" {
		records, err := store.LoadState(ctx, target)
		if err == nil {
			for _, r := range records {
				sm.prevState[r.Digest] = r
			}
		}
	}

	return sm
}

// Configure sets the vertex state store and target for this monitor, and loads
// the previous run's state. It is safe to call only before MonitorProgress.
func (sm *SolverMonitor) Configure(ctx context.Context, store buildkitskipper.VertexStateStore, target string) {
	if store == nil || target == "" {
		return
	}

	sm.store = store
	sm.targetName = target

	records, err := store.LoadState(ctx, target)
	if err == nil {
		for _, r := range records {
			sm.prevState[r.Digest] = r
		}
	}
}

// SetHashLogDiff sets the Earthfile-level diff lines to show when a cache miss
// has no vertex-level reason. Safe to call before MonitorProgress starts.
func (sm *SolverMonitor) SetHashLogDiff(lines []string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.hashLogDiff = lines
}

// SaveState persists collected vertex records to the store for the current target.
// It is a no-op when no store or target was configured.
func (sm *SolverMonitor) SaveState(ctx context.Context) error {
	if sm.store == nil || sm.targetName == "" {
		return nil
	}

	sm.mu.Lock()
	records := make([]buildkitskipper.VertexRecord, len(sm.collected))
	copy(records, sm.collected)
	sm.mu.Unlock()

	return sm.store.SaveState(ctx, sm.targetName, records)
}

// MonitorProgress processes a channel of buildkit solve statuses.
func (sm *SolverMonitor) MonitorProgress(ctx context.Context, ch chan *client.SolveStatus) error {
	delayedCtx, delayedCancel := context.WithCancel(xcontext.Detach(ctx))
	defer delayedCancel()

	go func() {
		<-ctx.Done()
		// Delay closing to allow any pending messages to be processed.
		// The delay is very high because we expect the buildkit connection
		// to be closed (and hence status channel to be closed) on cancellations
		// anyway. We should be waiting for the full 30 seconds only if there's
		// a bug.
		select {
		case <-delayedCtx.Done():
		case <-time.After(30 * time.Second):
		}

		delayedCancel()
	}()

	for {
		select {
		case <-delayedCtx.Done():
			return errors.Wrap(ctx.Err(), "timed out waiting for status channel to close")
		case status, ok := <-ch:
			if !ok {
				return nil
			}

			err := sm.handleBuildkitStatus(status)
			if err != nil {
				return err
			}
		}
	}
}

// digestStrings converts a slice of digest.Digest to a slice of strings.
func digestStrings(ds []digest.Digest) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.String()
	}

	return out
}

func (sm *SolverMonitor) handleBuildkitStatus(status *client.SolveStatus) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	bp := sm.b.Run()

	for _, vertex := range status.Vertexes {
		meta, operation := vertexmeta.ParseFromVertexPrefix(vertex.Name)

		var cmdID string

		createCmd := true

		switch {
		case meta.TargetName == "context":
			cmdID = operation
		case meta.CommandID != "":
			// If the command ID is set, the Logbus command is guaranteed to
			// have been created by earth in the converter ahead of time.
			cmdID = meta.CommandID
			createCmd = false
		default:
			cmdID = vertex.Digest.String()
		}

		vm, exists := sm.vertices[cmdID]
		//nolint:nestif // TODO(jhorsts): simplify
		if !exists {
			category := meta.TargetName
			if meta.Internal {
				category = "internal " + category
			}

			var cp *logbus.Command
			// Operations initiated from earth have created Logbus commands
			// ahead-of-time. Others may originate from BuildKit, so we'll have
			// to create a command at this point.
			if createCmd {
				var err error

				cp, err = bp.NewCommand(
					cmdID, operation, meta.TargetID, category, meta.Platform,
					vertex.Cached, meta.Local, meta.Interactive, meta.SourceLocation,
					meta.RepoGitURL, meta.RepoGitHash, meta.RepoFileRelToRepo)
				if err != nil {
					return err
				}
			} else {
				var ok bool

				cp, ok = bp.Command(cmdID)
				if !ok {
					// Note: if we receive a vertex with a full command ID that
					// does not exist in this process, it may have originated
					// from another earth process. It should be safe to
					// ignore, in this case.
					continue
				}

				cp.SetName(operation) // Command created prior may not have a full name.
			}

			vm = &vertexMonitor{
				vertex:    vertex,
				meta:      meta,
				operation: operation,
				cp:        cp,
				ssp:       statsstreamparser.New(),
			}
			sm.vertices[cmdID] = vm
		}

		sm.digests[vertex.Digest] = cmdID

		vm.vertex = vertex
		if vertex.Cached {
			vm.cp.SetCached(true)
		}

		if vertex.Started != nil {
			vm.cp.SetStart(*vertex.Started)

			// Only annotate RUN and COPY operations created by earth's converter
			// (identified by a non-empty CommandID). FROM / image-pull vertices
			// are always re-executed on a cold daemon and carry no actionable
			// miss information.
			if !vertex.Cached && !vm.cacheMissLogged &&
				meta.CommandID != "" && isAnnotatableOp(vm.operation) {
				vm.cacheMissLogged = true

				cacheMissMsg := "*cache miss*" + sm.buildCacheMissReason(vertex, meta)
				_, _ = vm.cp.Write([]byte(cacheMissMsg+"\n"), *vertex.Started, logbus.Stderr)
			}
		}

		if vertex.Error != "" {
			vm.parseError()
		}

		if vertex.Completed == nil {
			continue
		}

		// Collect the vertex record once the vertex is complete.
		sm.collected = append(sm.collected, buildkitskipper.VertexRecord{
			Digest:       vertex.Digest.String(),
			Inputs:       digestStrings(vertex.Inputs),
			Operation:    vm.operation,
			WasCached:    vertex.Cached,
			ActiveArgs:   vm.meta.ActiveArgs,
			CopiedPaths:  vm.meta.CopiedPaths,
			BaseImageRef: vm.meta.BaseImageRef,
		})

		var status logstream.RunStatus

		switch {
		case vm.isCanceled:
			status = logstream.RunStatus_RUN_STATUS_CANCELED
		case vertex.Error == "" && !vm.isFatalError:
			status = logstream.RunStatus_RUN_STATUS_SUCCESS
		default:
			status = logstream.RunStatus_RUN_STATUS_FAILURE
		}

		vm.cp.SetEnd(*vertex.Completed, status, vm.errorStr)

		if vm.isFatalError {
			// Run this at the end so that we capture any additional log lines.
			defer bp.SetFatalError(
				*vertex.Completed,
				vm.meta.TargetID,
				cmdID,
				vm.fatalErrorType,
				"",
				stringutil.ScrubCredentialsAll(vm.errorStr),
			)
		}
	}

	for _, vs := range status.Statuses {
		cmdID, exists := sm.digests[vs.Vertex]
		if !exists {
			continue
		}

		vm := sm.vertices[cmdID]

		progress := int32(0)
		if vs.Total != 0 {
			progress = int32(100.0 * float32(vs.Current) / float32(vs.Total))
		}

		if vs.Completed != nil {
			progress = 100
		}

		vm.cp.SetProgress(progress)
	}

	for _, logLine := range status.Logs {
		cmdID, exists := sm.digests[logLine.Vertex]
		if !exists {
			continue
		}

		vm := sm.vertices[cmdID]
		logLine.Data = []byte(stringutil.ScrubCredentialsAll((string(logLine.Data))))

		_, err := vm.Write(logLine.Data, logLine.Timestamp, logLine.Stream)
		if err != nil {
			return err
		}
	}

	return nil
}

// cacheMissReasonUnknown is returned when a cache miss regression is detected
// but no specific input change can be identified.
const cacheMissReasonUnknown = "unknown"

// buildCacheMissReason returns an optional suffix to append to the base
// "*cache miss*" annotation. It returns "" when no prior state is available
// (first run or no DB configured). When the vertex was previously cached it
// returns a human-readable explanation of what changed.
func (sm *SolverMonitor) buildCacheMissReason(vertex *client.Vertex, meta *vertexmeta.VertexMeta) string {
	digestStr := vertex.Digest.String()
	prev, found := sm.prevState[digestStr]

	if !found {
		// Vertex digest changed (command/inputs changed) or first run.
		// Show the Earthfile diff once on the first miss, then clear it.
		if len(sm.hashLogDiff) > 0 {
			reason := " (Earthfile changed:\n" + strings.Join(sm.hashLogDiff, "\n") + ")"
			sm.hashLogDiff = nil

			return reason
		}

		return ""
	}

	if !prev.WasCached {
		// Was already a miss last time — no regression to report.
		return ""
	}

	// Previously cached but now a miss. Try to explain why, in priority order.

	// 1. ARG value changed.
	if reason := diffArgs(prev.ActiveArgs, meta.ActiveArgs); reason != "" {
		return " (previously cached; " + reason + ")"
	}

	// 2. COPY source paths changed.
	if reason := diffCopiedPaths(prev.CopiedPaths, meta.CopiedPaths); reason != "" {
		return " (previously cached; " + reason + ")"
	}

	// 3. Base image reference changed.
	if prev.BaseImageRef != "" && meta.BaseImageRef != "" && prev.BaseImageRef != meta.BaseImageRef {
		return " (previously cached; base image changed: " + prev.BaseImageRef + " → " + meta.BaseImageRef + ")"
	}

	// 4. Command text changed (same digest, different operation string — shouldn't
	// happen for structural ops, but handle it defensively).
	if prev.Operation != "" && prev.Operation != sm.prevState[digestStr].Operation {
		return " (previously cached; command changed)"
	}

	// 5. Fall back to input-chain analysis.
	if changedInput := sm.findChangedInput(vertex.Inputs); changedInput != cacheMissReasonUnknown {
		return " (previously cached; upstream changed: " + changedInput + ")"
	}

	// 6. Fall back to Earthfile-level diff if available.
	if len(sm.hashLogDiff) > 0 {
		return " (previously cached; Earthfile changed:\n" + strings.Join(sm.hashLogDiff, "\n") + ")"
	}

	return " (previously cached; reason " + cacheMissReasonUnknown + ")"
}

// diffArgs returns a human-readable description of the first arg that changed
// between prev and current. Returns "" if no relevant change is found.
func diffArgs(prev, current map[string]string) string {
	for k, curVal := range current {
		prevVal, existed := prev[k]
		if !existed {
			return "new arg: " + k + "=" + curVal
		}

		if prevVal != curVal {
			return "arg changed: " + k + "=" + prevVal + " → " + curVal
		}
	}

	for k, prevVal := range prev {
		if _, exists := current[k]; !exists {
			return "arg removed: " + k + "=" + prevVal
		}
	}

	return ""
}

// diffCopiedPaths returns a human-readable description if the set of copied
// paths changed between runs. Returns "" if unchanged.
func diffCopiedPaths(prev, current []string) string {
	if len(prev) == 0 && len(current) == 0 {
		return ""
	}

	prevSet := make(map[string]struct{}, len(prev))
	for _, p := range prev {
		prevSet[p] = struct{}{}
	}

	for _, p := range current {
		if _, ok := prevSet[p]; !ok {
			return "file added to COPY: " + p
		}
	}

	currSet := make(map[string]struct{}, len(current))
	for _, p := range current {
		currSet[p] = struct{}{}
	}

	for _, p := range prev {
		if _, ok := currSet[p]; !ok {
			return "file removed from COPY: " + p
		}
	}

	return ""
}

// isAnnotatableOp returns true for operations where a cache miss is meaningful
// and actionable — specifically RUN and COPY commands authored by the user.
// FROM and image-pull operations are excluded because they depend on registry
// state and are always non-cached on a cold daemon.
func isAnnotatableOp(operation string) bool {
	return strings.HasPrefix(operation, "RUN ") ||
		strings.HasPrefix(operation, "COPY ") ||
		strings.HasPrefix(operation, "GIT CLONE ")
}

// findChangedInput scans the given input digests and returns the Operation of
// the first input that either (a) does not appear in prevState or (b) was not
// cached last time. Returns "unknown" when no specific input can be identified.
func (sm *SolverMonitor) findChangedInput(inputs []digest.Digest) string {
	for _, inp := range inputs {
		inpStr := inp.String()
		prev, ok := sm.prevState[inpStr]

		if !ok || !prev.WasCached {
			if ok && prev.Operation != "" {
				return prev.Operation
			}

			return cacheMissReasonUnknown
		}
	}

	return cacheMissReasonUnknown
}
