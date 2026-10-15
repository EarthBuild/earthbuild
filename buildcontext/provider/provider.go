// Package provider is heavily based on fsSyncProvider in github.com/moby/buildkit/session/filesync.
// The key difference between BuildContextProvider and fsSyncProvider is that in
// BuildContextProvider, the dirs can be added incrementally after the construction.
package provider

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/util/fsutilprogress"
	"github.com/dustin/go-humanize"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	keyOverrideExcludes = "override-excludes"
	keyIncludePatterns  = "include-patterns"
	keyExcludePatterns  = "exclude-patterns"
	keyFollowPaths      = "followpaths"
	keyDirName          = "dir-name"
)

var (
	_ session.Attachable      = (*BuildContextProvider)(nil)
	_ filesync.FileSyncServer = (*BuildContextProvider)(nil)
)

// BuildContextProvider is a BuildKit attachable which provides local files as part
// of the build context.
type BuildContextProvider struct {
	dirs map[string]SyncedDir
	log  *conslogging.ConsoleLogger
	mu   sync.Mutex
}

// SyncedDir is a directory to be synced across.
type SyncedDir struct {
	Map      func(string, *fstypes.Stat) fsutil.MapResult
	Name     string
	Dir      string
	Excludes []string
}

// NewBuildContextProvider creates a new provider for sending build context files from client.
func NewBuildContextProvider(log *conslogging.ConsoleLogger) *BuildContextProvider {
	return &BuildContextProvider{
		dirs: map[string]SyncedDir{},
		log:  log,
	}
}

// AddDirs adds local directories to the context.
func (bcp *BuildContextProvider) AddDirs(dirs map[string]string) {
	bcp.mu.Lock()
	defer bcp.mu.Unlock()

	for dirName, dir := range dirs {
		bcp.addDir(dirName, dir)
	}
}

// AddDir adds a single local directory to the context.
func (bcp *BuildContextProvider) AddDir(dirName, dir string) {
	bcp.mu.Lock()
	defer bcp.mu.Unlock()

	bcp.addDir(dirName, dir)
}

func (bcp *BuildContextProvider) addDir(dirName, dir string) {
	resetUIDAndGID := func(_ string, st *fstypes.Stat) fsutil.MapResult {
		st.Uid = 0
		st.Gid = 0

		return fsutil.MapResultKeep
	}
	sd := SyncedDir{
		Name: dirName,
		Dir:  dir,
		Map:  resetUIDAndGID,
	}

	bcp.dirs[sd.Name] = sd
}

// Register registers the attachable.
func (bcp *BuildContextProvider) Register(server *grpc.Server) {
	filesync.RegisterFileSyncServer(server, bcp)
}

// DiffCopy implements the DiffCopy attachable.
func (bcp *BuildContextProvider) DiffCopy(stream filesync.FileSync_DiffCopyServer) error {
	return bcp.handle("diffcopy", stream)
}

// TarStream implements the DiffCopy attachable.
func (bcp *BuildContextProvider) TarStream(stream filesync.FileSync_TarStreamServer) error {
	return bcp.handle("tarstream", stream)
}

func (bcp *BuildContextProvider) handle(method string, stream grpc.ServerStream) error {
	var pr *protocol

	for _, p := range supportedProtocols {
		if method == p.name && isProtoSupported(p.name) {
			pr = &p
			break
		}
	}

	if pr == nil {
		return errors.New("failed to negotiate protocol")
	}

	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object

	dirName := ""

	name, ok := opts[keyDirName]
	if ok && len(name) > 0 {
		dirName = name[0]
	}

	dir, err := bcp.getDir(dirName)
	if err != nil {
		return err
	}

	excludes := opts[keyExcludePatterns]
	if len(dir.Excludes) != 0 && (len(opts[keyOverrideExcludes]) == 0 || opts[keyOverrideExcludes][0] != "true") {
		excludes = dir.Excludes
	}

	includes := opts[keyIncludePatterns]
	followPaths := opts[keyFollowPaths]
	log := bcp.log.WithPrefixAndSalt("context", dir.Dir)
	progressCB := fsutilprogress.NewContext(dir.Dir, log)

	fs, err := fsutil.NewFS(dir.Dir)
	if err != nil {
		return err
	}

	fs, err = fsutil.NewFilterFS(fs, &fsutil.FilterOpt{
		ExcludePatterns: excludes,
		IncludePatterns: includes,
		FollowPaths:     followPaths,
		Map:             dir.Map,
	})
	if err != nil {
		return err
	}

	lfs := &loggingFS{
		FS:  fs,
		log: log,
		dir: dir.Dir,
	}

	return pr.sendFn(stream, lfs, progressCB.Info)
}

func (bcp *BuildContextProvider) getDir(dirName string) (SyncedDir, error) {
	bcp.mu.Lock()
	defer bcp.mu.Unlock()

	dir, ok := bcp.dirs[dirName]
	if !ok {
		return SyncedDir{}, status.Errorf(codes.NotFound, "no access allowed to dir %q", dirName)
	}

	return dir, nil
}

type loggingFS struct {
	fsutil.FS

	log *conslogging.ConsoleLogger
	dir string
}

func (l *loggingFS) Walk(ctx context.Context, target string, fn fs.WalkDirFunc) error {
	return l.FS.Walk(ctx, target, func(p string, d fs.DirEntry, walkErr error) error {
		err := fn(p, d, walkErr)
		if err != nil {
			return err
		}

		if walkErr == nil {
			fullPath := filepath.Join(l.dir, p)
			l.log.DebugPrintf("sent file stat for %s\n", fullPath)
		}

		return nil
	})
}

func (l *loggingFS) Open(p string) (io.ReadCloser, error) {
	rc, err := l.FS.Open(p)
	if err != nil {
		return nil, err
	}

	fullPath := filepath.Join(l.dir, p)

	var sz int64 = -1

	if s, ok := rc.(interface{ Stat() (fs.FileInfo, error) }); ok {
		fi, err := s.Stat()
		if err == nil {
			sz = fi.Size()
		}
	}

	if sz >= 0 {
		l.log.VerbosePrintf("sent data for %s (%s)\n", fullPath, humanize.Bytes(uint64(sz)))
	} else {
		l.log.VerbosePrintf("sent data for %s\n", fullPath)
	}

	return rc, nil
}

type progressCb func(int, bool)

type protocol struct {
	sendFn func(stream filesync.Stream, fs fsutil.FS, progress progressCb) error
	name   string
}

func isProtoSupported(p string) bool {
	// TODO: this should be removed after testing if stability is confirmed
	if override := os.Getenv("BUILD_STREAM_PROTOCOL"); override != "" {
		return strings.EqualFold(p, override)
	}

	return true
}

var supportedProtocols = []protocol{
	{
		name:   "diffcopy",
		sendFn: sendDiffCopy,
	},
}

func sendDiffCopy(stream filesync.Stream, fs fsutil.FS, progress progressCb) error {
	return fsutil.Send(stream.Context(), stream, fs, progress)
}
