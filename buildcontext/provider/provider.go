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
	"path"
	"strings"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/util/fsutilprogress"
	"github.com/dustin/go-humanize"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/sirupsen/logrus"
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
	p      progressCb
	doneCh chan error
	dirs   map[string]SyncedDir
	log    *conslogging.ConsoleLogger
	mu     sync.Mutex
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

func (bcp *BuildContextProvider) handle(method string, stream grpc.ServerStream) (retErr error) {
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

	progressCB := fsutilprogress.New(dir.Dir, bcp.log.WithPrefixAndSalt("context", dir.Dir))

	var doneCh chan error
	if bcp.doneCh != nil {
		doneCh = bcp.doneCh
		bcp.doneCh = nil
	}

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
		log: bcp.log.WithPrefixAndSalt("context", dir.Dir),
		dir: dir.Dir,
	}

	err = pr.sendFn(stream, lfs, progressCB.Info)
	if doneCh != nil {
		if err != nil {
			doneCh <- err
		}

		close(doneCh)
	}

	return err
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

// SetNextProgressCallback sets the progress callback function.
func (bcp *BuildContextProvider) SetNextProgressCallback(f func(int, bool), doneCh chan error) {
	bcp.p = f
	bcp.doneCh = doneCh
}

type loggingFS struct {
	fsutil.FS

	filesize map[string]int64
	log      *conslogging.ConsoleLogger
	dir      string
	mu       sync.Mutex
}

func (l *loggingFS) recordFileSize(p string, d fs.DirEntry) {
	if d.IsDir() {
		return
	}

	info, err := d.Info()
	if err != nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.filesize == nil {
		l.filesize = make(map[string]int64)
	}

	l.filesize[p] = info.Size()
}

func (l *loggingFS) Walk(ctx context.Context, target string, fn fs.WalkDirFunc) error {
	return l.FS.Walk(ctx, target, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fn(p, d, err)
		}

		fullPath := path.Join(l.dir, p)
		l.log.DebugPrintf("sent file stat for %s\n", fullPath)
		l.recordFileSize(p, d)

		return fn(p, d, err)
	})
}

func (l *loggingFS) Open(p string) (io.ReadCloser, error) {
	rc, err := l.FS.Open(p)
	if err != nil {
		return rc, err
	}

	fullPath := path.Join(l.dir, p)

	l.mu.Lock()
	sz, ok := l.filesize[p]
	l.mu.Unlock()

	if ok && sz >= 0 {
		l.log.VerbosePrintf("sent data for %s (%s)\n", fullPath, humanize.Bytes(uint64(sz)))
	} else {
		l.log.VerbosePrintf("sent data for %s\n", fullPath)
	}

	return rc, nil
}

type progressCb func(int, bool)

type protocol struct {
	sendFn func(stream filesync.Stream, fs fsutil.FS, progress progressCb) error
	recvFn func(
		stream grpc.ClientStream,
		destDir string,
		cu filesync.CacheUpdater,
		progress progressCb,
		mapFunc func(string, *fstypes.Stat) bool,
	) error
	name string
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
		recvFn: recvDiffCopy,
	},
}

func sendDiffCopy(stream filesync.Stream, fs fsutil.FS, progress progressCb) error {
	return fsutil.Send(stream.Context(), stream, fs, progress)
}

func recvDiffCopy(
	ds grpc.ClientStream,
	dest string,
	cu filesync.CacheUpdater,
	progress progressCb,
	filter func(string, *fstypes.Stat) bool,
) error {
	st := time.Now()

	defer func() {
		logrus.Debugf("diffcopy took: %v", time.Since(st))
	}()

	var (
		cf fsutil.ChangeFunc
		ch fsutil.ContentHasher
	)

	if cu != nil {
		cu.MarkSupported(true)
		cf = cu.HandleChange
		ch = cu.ContentHasher()
	}

	return fsutil.Receive(ds.Context(), ds, dest, fsutil.ReceiveOpt{
		NotifyHashed:  cf,
		ContentHasher: ch,
		ProgressCb:    progress,
		Filter:        fsutil.FilterFunc(filter),
	})
}
