// Package setup provides initialization functions for creating and configuring the central logbus instance.
package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logbus/formatter"
	"github.com/EarthBuild/earthbuild/logbus/solvermon"
	"github.com/EarthBuild/earthbuild/logbus/writersub"
	"github.com/EarthBuild/earthbuild/logstream"
	"github.com/EarthBuild/earthbuild/util/deltautil"
	"github.com/EarthBuild/earthbuild/util/execstatssummary"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// BusSetup is a helper for setting up a logbus.Bus.
type BusSetup struct {
	Bus              *logbus.Bus
	ConsoleWriter    *writersub.WriterSub
	Formatter        *formatter.Formatter
	SolverMonitor    *solvermon.SolverMonitor
	BusDebugWriter   *writersub.RawWriterSub
	InitialManifest  *logstream.RunManifest
	execStatsTracker *execstatssummary.Tracker
}

// New creates a new BusSetup.
func New(
	ctx context.Context,
	bus *logbus.Bus,
	debug, verbose, displayStats bool, disableOngoingUpdates bool,
	busDebugFile, buildID string,
	execStatsTracker *execstatssummary.Tracker,
	isGitHubActions bool,
) (*BusSetup, error) {
	bs := &BusSetup{
		Bus:           bus,
		ConsoleWriter: writersub.New(os.Stderr, "_full"),
		Formatter:     nil, // set below
		SolverMonitor: nil, // set below
		InitialManifest: &logstream.RunManifest{
			BuildId:            buildID,
			Version:            deltautil.Version,
			CreatedAtUnixNanos: uint64(bus.CreatedAt().UnixNano()), // #nosec G115
		},
		execStatsTracker: execStatsTracker,
	}
	bs.Formatter = formatter.New(
		ctx, bs.Bus, debug, verbose, displayStats,
		disableOngoingUpdates, execStatsTracker, isGitHubActions,
	)
	bs.Bus.AddRawSubscriber(bs.Formatter)
	bs.Bus.AddFormattedSubscriber(bs.ConsoleWriter)
	bs.SolverMonitor = solvermon.New(bs.Bus)

	if busDebugFile != "" {
		f, err := os.OpenFile(busDebugFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) // #nosec G302, G304
		if err != nil {
			return nil, fmt.Errorf("failed to open bus debug file %s: %w", busDebugFile, err)
		}

		useJSON := strings.HasSuffix(busDebugFile, ".json")
		bs.BusDebugWriter = writersub.NewRaw(f, useJSON)
		bs.Bus.AddSubscriber(bs.BusDebugWriter)
	}

	return bs, nil
}

// SetDefaultPlatform sets the default platform of the build.
func (bs *BusSetup) SetDefaultPlatform(platform string) {
	bs.Formatter.SetDefaultPlatform(platform)
}

// SetGitAuthor records the Git author information on the initial manifest.
func (bs *BusSetup) SetGitAuthor(gitAuthor, gitCommitEmail string) {
	bs.InitialManifest.GitAuthor = gitAuthor
	bs.InitialManifest.GitConfigEmail = gitCommitEmail
}

// SetCI tracks whether this build is being run in a CI environment.
func (bs *BusSetup) SetCI(isCI bool) {
	bs.InitialManifest.IsCi = isCI
}

// DumpManifestToFile dumps the manifest to the given file.
func (bs *BusSetup) DumpManifestToFile(path string) error {
	m := bs.Formatter.Manifest()
	proto.Merge(m, bs.InitialManifest)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644) // #nosec G302, G304
	if err != nil {
		return fmt.Errorf("failed to open bus manifest debug file %s: %w", path, err)
	}

	useJSON := strings.HasSuffix(path, ".json")

	var dt []byte

	if useJSON {
		jsonOpts := protojson.MarshalOptions{
			Multiline:       true,
			Indent:          "  ",
			UseProtoNames:   false,
			EmitUnpopulated: true,
		}
		dt, err = jsonOpts.Marshal(m)
	} else {
		dt, err = proto.Marshal(m)
	}

	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	_, err = f.Write(dt)
	if err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// Close the bus setup & gather all errors.
func (bs *BusSetup) Close() error {
	var err error

	if bs.execStatsTracker != nil {
		trackerErr := bs.execStatsTracker.Close()
		if trackerErr != nil {
			err = errors.Join(err, fmt.Errorf("exec stats summary: %w", trackerErr))
		}
	}

	consoleErr := bs.ConsoleWriter.Err()
	if consoleErr != nil {
		err = errors.Join(err, fmt.Errorf("console writer: %w", consoleErr))
	}

	formatterErr := bs.Formatter.Close()
	if formatterErr != nil {
		err = errors.Join(err, fmt.Errorf("formatter: %w", formatterErr))
	}

	if bs.BusDebugWriter != nil {
		debugErr := bs.BusDebugWriter.Err()
		if debugErr != nil {
			err = errors.Join(err, fmt.Errorf("bus debug writer: %w", debugErr))
		}
	}

	return err
}
