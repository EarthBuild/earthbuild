// Package helper runs the program that understands a cache's format.
//
// **The per-language knowledge, delegated.** The engine moves bytes and names
// them by ℋ; which bytes belong together, what a tool calls them and how two of
// them merge are facts about that tool. They live in a helper, named by
// `CACHE --helper`, and nothing here parses a single one of them.
//
// **WASI, because a fleet is deliberately unlike itself.** An arm64 Mac drives
// amd64 steps and a worker may be either, so a helper compiled per architecture
// is a manifest of artefacts where a `wasip1/wasm` module is one file. It also
// runs wherever the cache is - a cache store lives inside the guest on a VM
// backend and beside the host on a native one - without either end needing a
// binary built for it.
//
// **Confined to one directory and nothing else.** The module is given the cache
// root as its only preopened path: no network, no other file, no ambient
// authority. That is a narrower grant than the step beside it already has, and
// it costs nothing to make.
package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// cacheDirIn is where a helper sees the cache, inside its own filesystem.
//
// Fixed rather than the host's path, because a helper must not be able to tell
// one machine from another: a unit's bytes are a function of the cache and never
// of what read it, and a path that differed between machines is one more way for
// two workers to disagree about a digest.
const cacheDirIn = "/cache"

// EnvCacheDir names that directory for the helper.
const EnvCacheDir = "EARTH_CACHE_DIR"

// Runtime compiles helper modules once and runs them many times.
//
// **Compilation is the expensive part and it is per module, not per call.** A
// 4 MiB helper takes on the order of a hundred milliseconds to compile and under
// a millisecond to instantiate, so a `Runtime` held across a build pays the
// first once and the second per verb - which is what makes a verb-per-invocation
// contract affordable here where a container per verb would not be.
type Runtime struct {
	rt    wazero.Runtime
	cache wazero.CompilationCache
}

// Open prepares a runtime, keeping compiled modules under dir.
//
// A compilation cache on disk, so a second build does not recompile what the
// first did. Empty dir keeps it in memory, which is right for a test and for a
// one-shot command.
func Open(ctx context.Context, dir string) (*Runtime, error) {
	var (
		cache wazero.CompilationCache
		err   error
	)

	if dir != "" {
		cache, err = wazero.NewCompilationCacheWithDir(dir)
		if err != nil {
			return nil, fmt.Errorf("helper compilation cache in %s: %w", dir, err)
		}
	}

	cfg := wazero.NewRuntimeConfig()
	if cache != nil {
		cfg = cfg.WithCompilationCache(cache)
	}

	rt := newRuntime(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)

		return nil, fmt.Errorf("wasi in the helper runtime: %w", err)
	}

	return &Runtime{rt: rt, cache: cache}, nil
}

// firstRuntime serialises the construction of the very first wazero runtime.
//
// **Not our race, but ours to avoid.** wazero v1.12.0's
// `internal/version.GetWazeroVersion` memoises the module version into a
// package-level variable with no synchronisation, and `NewRuntimeWithConfig`
// reaches it on every call - so two goroutines opening a helper at the same
// time read and write that variable concurrently. Go's race detector fails the
// whole test binary when it sees it, which is how one upstream global took four
// packages red.
//
// Serialising every construction would be the obvious fix and the wrong one: a
// helper is opened per cache mount, and making that a global bottleneck to work
// around somebody else's unsynchronised variable trades a real property for a
// borrowed bug. One pass through the mutex is enough, because the variable is
// written once and only ever read afterwards.
var firstRuntime sync.Once

// newRuntime builds the runtime, warming wazero's version global exactly once.
func newRuntime(ctx context.Context, cfg wazero.RuntimeConfig) wazero.Runtime {
	var rt wazero.Runtime

	firstRuntime.Do(func() { rt = wazero.NewRuntimeWithConfig(ctx, cfg) })

	if rt != nil {
		return rt
	}

	return wazero.NewRuntimeWithConfig(ctx, cfg)
}

// Close releases the runtime and its compiled modules.
func (r *Runtime) Close(ctx context.Context) error {
	err := r.rt.Close(ctx)

	if r.cache != nil {
		_ = r.cache.Close(ctx)
	}

	if err != nil {
		return fmt.Errorf("close the helper runtime: %w", err)
	}

	return nil
}

// Helper is one compiled helper module.
type Helper struct {
	// Prefix goes before the verb, for a module that serves more than one cache
	// format. The contract is `<helper> <verb>`; a module holding several
	// helpers needs to be told which, and that is its own business rather than
	// the engine's.
	Prefix []string

	rt   wazero.Runtime
	code wazero.CompiledModule
	name string
}

// Compile prepares a helper from its module bytes.
//
// The bytes rather than a path, because what an Earthfile names has to be
// resolved and digested before it is run - a helper decides what lands in a
// cache, so which helper ran is part of the step's identity (Κ₁).
func (r *Runtime) Compile(ctx context.Context, name string, module []byte) (*Helper, error) {
	code, err := r.rt.CompileModule(ctx, module)
	if err != nil {
		return nil, fmt.Errorf("compile helper %s: %w", name, err)
	}

	return &Helper{rt: r.rt, code: code, name: name}, nil
}

// Run invokes one verb against a cache directory.
//
// **A miss is not a failure and an exit code says which.** A helper that exits
// non-zero has refused - it does not understand this cache, or the request was
// malformed - and that is reported. A helper that exits zero having written
// nothing has answered "nothing here", which is an ordinary answer for a cache
// and must not be mistaken for a fault.
func (h *Helper) Run(
	ctx context.Context, cacheDir string, args []string, in io.Reader, out io.Writer,
) error {
	fs := wazero.NewFSConfig().WithDirMount(cacheDir, cacheDirIn)

	// **Kept, because "exit 1" is not a diagnosis.** A helper that refuses says
	// why on stderr, and discarding it left a build reporting an exit code and
	// no reason at all - a helper nobody can debug and a cache nobody can
	// explain. Bounded, since a module in a loop must not fill memory with its
	// own complaint.
	var whined boundedBuffer

	cfg := wazero.NewModuleConfig().
		WithFSConfig(fs).
		WithArgs(append(append([]string{h.name}, h.Prefix...), args...)...).
		WithEnv(EnvCacheDir, cacheDirIn).
		// LC_ALL, so a helper that sorts its index sorts it the same way
		// everywhere. An index ordered by one machine's locale and read by
		// another's is a diff that never converges.
		WithEnv("LC_ALL", "C").
		WithStdin(in).
		WithStdout(out).
		WithStderr(&whined)

	// **No clock and no randomness, by saying nothing.** wazero grants neither
	// unless asked, and a helper has no business with either: a unit whose bytes
	// depended on the time or on chance would be a unit two machines name
	// differently, which is the failure this whole design exists to avoid.
	//
	// The start function is left alone too. A Go `wasip1` build is a *command*
	// whose entry is `_start`; naming `_initialize` instead - the reactor entry
	// - instantiates the module without ever running `main`, and every verb then
	// returns success having written nothing. Which reads exactly like a cache
	// that is empty.

	mod, err := h.rt.InstantiateModule(ctx, h.code, cfg)
	if err != nil {
		var exit *sys.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 0 {
				return nil
			}

			return fmt.Errorf("helper %s %v: exit %d%s", h.name, args, exit.ExitCode(), whined.said())
		}

		return fmt.Errorf("run helper %s %v: %w%s", h.name, args, err, whined.said())
	}

	return mod.Close(ctx) //nolint:wrapcheck // the module's own error
}

// maxWhine bounds what a helper's stderr can cost.
const maxWhine = 8 << 10

// boundedBuffer keeps the first maxWhine bytes written to it and drops the rest.
type boundedBuffer struct{ b []byte }

func (w *boundedBuffer) Write(p []byte) (int, error) {
	if room := maxWhine - len(w.b); room > 0 {
		w.b = append(w.b, p[:min(room, len(p))]...)
	}

	return len(p), nil
}

// said is the helper's complaint, ready to append to an error, or empty.
func (w *boundedBuffer) said() string {
	if s := strings.TrimSpace(string(w.b)); s != "" {
		return ": " + s
	}

	return ""
}
