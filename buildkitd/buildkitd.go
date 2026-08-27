// Package buildkitd manages the lifecycle of the embedded or remote Buildkit daemon used by earth.
package buildkitd

import (
	"cmp"
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/EarthBuild/earthbuild/internal/env"
	"github.com/EarthBuild/earthbuild/util/buildkitutil"
	"github.com/EarthBuild/earthbuild/util/fileutil"
	"github.com/EarthBuild/earthbuild/util/hint"
	"github.com/containerd/platforms"
	"github.com/docker/go-units"
	"github.com/dustin/go-humanize"
	"github.com/gofrs/flock"
	"github.com/moby/buildkit/client"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // Load "docker-container://" helper.
	"golang.org/x/mod/semver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const minRecommendedCacheSize = 10 << 30 // 10 GiB

var (
	// ErrBuildkitCrashed is an error returned when buildkit has terminated unexpectedly.
	ErrBuildkitCrashed = errors.New("buildkitd crashed")

	// ErrBuildkitConnectionFailure is an error returned when buildkit has failed to respond.
	ErrBuildkitConnectionFailure = errors.New("buildkitd did not respond (in time)")
)

// ContainerName returns the buildkitd container name for a given installation.
func ContainerName(installationName string) string {
	return installationName + "-buildkitd"
}

// VolumeName returns the cache volume name for a given installation.
func VolumeName(installationName string) string {
	return installationName + "-cache"
}

// NewClient returns a new buildkitd client. If the buildkitd daemon is local, this function
// might start one up, if not already started.
func NewClient(
	ctx context.Context,
	log *conslogging.ConsoleLogger,
	image, containerName string,
	eng *engine.Client,
	earthVersion string,
	settings Settings,
	opts ...client.ClientOpt,
) (_ *client.Client, retErr error) {
	defer func() {
		if retErr == nil {
			return
		}

		if errors.Is(retErr, os.ErrNotExist) {
			scheme := eng.Metadata().Scheme
			if scheme == engine.SchemePodman || scheme == engine.SchemeApple {
				tlsPaths := []string{
					settings.TLSCA,
					settings.ServerTLSKey,
					settings.ServerTLSCert,
					settings.ClientTLSKey,
					settings.ClientTLSCert,
				}
				if containsAny(retErr.Error(), tlsPaths) {
					retErr = hint.Wrapf(
						retErr,
						"podman now requires TLS certs by default - "+
							"try stopping the %s container and re-running 'earth bootstrap'\n"+
							"alternatively, run 'earth config global.tls_enabled false' to disable TLS",
						containerName,
					)
				}
			}

			return
		}

		if strings.Contains(retErr.Error(), rsa.ErrVerification.Error()) {
			// verification errors can happen server-side, which means
			// errors.Is() won't work. We use strings.Contains instead to handle
			// that case.
			retErr = hint.Wrapf(
				retErr,
				"did earth's certificates get regenerated? you may need to manually stop the %s container.",
				containerName,
			)

			return
		}
	}()

	baseOpts := opts

	opts, err := addRequiredOpts(settings, baseOpts...)
	if err != nil {
		return nil, fmt.Errorf("add required client opts: %w", err)
	}

	isLocal := engine.IsLocal(settings.BuildkitAddress)
	if !isLocal {
		var (
			remoteConsole = log.WithPrefix("buildkitd")
			info          *client.Info
			workerInfo    *client.WorkerInfo
		)

		remoteConsole.Printf("Connecting to %s...", settings.BuildkitAddress)

		info, workerInfo, err = waitForConnection(ctx, containerName, settings, eng, opts...)
		if err != nil {
			return nil, fmt.Errorf("connect provided buildkit: %w", err)
		}

		remoteConsole.Printf("...Done")
		printBuildkitInfo(remoteConsole, info, workerInfo, earthVersion, isLocal, settings.HasConfiguredCacheSize())

		var bkClient *client.Client

		bkClient, err = client.New(ctx, settings.BuildkitAddress, opts...)
		if err != nil {
			return nil, fmt.Errorf("start provided buildkit: %w", err)
		}

		return bkClient, nil
	}

	bkCons := log.WithPrefix("buildkitd")
	if !eng.IsAvailable(ctx) {
		bkCons.Printf("Is %[1]s installed and running? Are you part of any needed groups?\n", engineName(eng))
		return nil, fmt.Errorf("%s not available", engineName(eng))
	}

	info, workerInfo, err := maybeStart(ctx, log, image, containerName, eng, settings, opts...)
	if err != nil {
		return nil, fmt.Errorf("maybe start buildkitd: %w", err)
	}

	opts, err = updateClientSettings(ctx, containerName, eng, &settings, baseOpts...)
	if err != nil {
		return nil, err
	}

	printBuildkitInfo(log, info, workerInfo, earthVersion, isLocal, settings.HasConfiguredCacheSize())

	bkClient, err := client.New(ctx, settings.BuildkitAddress, opts...)
	if err != nil {
		return nil, fmt.Errorf("new buildkit client: %w", err)
	}

	return bkClient, nil
}

// ResetCache restarts the buildkitd daemon with the reset command.
func ResetCache(
	ctx context.Context,
	log *conslogging.ConsoleLogger,
	image, containerName string,
	eng *engine.Client,
	settings Settings,
	opts ...client.ClientOpt,
) error {
	// Prune by resetting container.
	if !engine.IsLocal(settings.BuildkitAddress) {
		return errors.New("cannot reset cache of a provided buildkit-host setting")
	}

	opts, err := addRequiredOpts(settings, opts...)
	if err != nil {
		return fmt.Errorf("add required client opts: %w", err)
	}

	log.
		WithPrefix("buildkitd").
		Printf("Restarting buildkit daemon with reset command...\n")

	// Use twice the restart timeout for reset operations
	// (needs extra time to also remove the files).
	settings.Timeout *= 2

	isStarted, err := IsStarted(ctx, containerName, eng)
	if err != nil {
		return fmt.Errorf("check is started buildkitd: %w", err)
	}

	if isStarted {
		err = Stop(ctx, containerName, eng)
		if err != nil {
			return err
		}

		err = WaitUntilStopped(ctx, containerName, settings.Timeout, eng)
		if err != nil {
			return err
		}
	}

	err = Start(ctx, log, image, containerName, eng, settings, true)
	if err != nil {
		return err
	}

	_, _, err = WaitUntilStarted(ctx, log, containerName, settings.VolumeName, settings, eng, opts...)
	if err != nil {
		return err
	}

	log.
		WithPrefix("buildkitd").
		Printf("... Done")

	return nil
}

// maybeStart ensures that the buildkitd daemon is started. It returns the URL
// that can be used to connect to it.
func maybeStart(
	ctx context.Context,
	log *conslogging.ConsoleLogger,
	image, containerName string,
	eng *engine.Client,
	settings Settings,
	opts ...client.ClientOpt,
) (cinfo *client.Info, winfo *client.WorkerInfo, finalErr error) {
	if settings.StartUpLockPath != "" {
		var tryLockDone atomic.Bool

		go func() {
			time.Sleep(3 * time.Second)

			if !tryLockDone.Load() {
				log.Warnf("waiting on other instance of earthbuild to start buildkitd (as indicated by %q existing)",
					settings.StartUpLockPath)
			}
		}()

		startLock := flock.New(settings.StartUpLockPath)

		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		_, err := startLock.TryLockContext(timeoutCtx, 200*time.Millisecond)

		tryLockDone.Store(true)

		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, nil, errors.New("timeout waiting for other instance of earth to start buildkitd")
		case err != nil:
			return nil, nil, fmt.Errorf("try flock context %s: %w", settings.StartUpLockPath, err)
		default:
			defer func() {
				inErr := startLock.Unlock()
				if inErr != nil {
					log.Warnf("Failed to unlock %s: %v", settings.StartUpLockPath, inErr)

					if finalErr == nil {
						finalErr = inErr
					}

					return
				}
			}()
		}
	}

	isStarted, err := IsStarted(ctx, containerName, eng)
	if err != nil {
		return nil, nil, fmt.Errorf("check is started buildkitd: %w", err)
	}

	if isStarted {
		log.
			WithPrefix("buildkitd").
			Printf("Found buildkit daemon as %s (%s)\n", engineContainer(eng), containerName)

		var (
			info       *client.Info
			workerInfo *client.WorkerInfo
		)

		info, workerInfo, err = maybeRestart(ctx, log, image, containerName, eng, settings, opts...)
		if err != nil {
			return nil, nil, fmt.Errorf("maybe restart: %w", err)
		}

		return info, workerInfo, nil
	}

	log.
		WithPrefix("buildkitd").
		Printf("Starting buildkit daemon as %s (%s)...\n", engineContainer(eng), containerName)

	err = Start(ctx, log, image, containerName, eng, settings, false)
	if err != nil {
		return nil, nil, fmt.Errorf("start: %w", err)
	}

	info, workerInfo, err := WaitUntilStarted(ctx, log, containerName, settings.VolumeName, settings, eng, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("wait until started: %w", err)
	}

	// check arch is correct
	runningContainerInfo, err := GetContainerInfo(ctx, containerName, eng)
	if err != nil {
		return nil, nil, fmt.Errorf("GetContainerInfo %s: %w", containerName, err)
	}

	currentImageInfo, err := GetImageInfo(ctx, runningContainerInfo.Image, eng)
	if err != nil {
		return nil, nil, fmt.Errorf("GetImageInfo %s: %w", runningContainerInfo.Image, err)
	}

	if currentImageInfo.Architecture != runtime.GOARCH {
		log.
			WithPrefix("buildkitd").
			Warnf("Warning: %s was started using architecture %s, but host architecture is %s; "+
				"is DOCKER_DEFAULT_PLATFORM accidentally set?\n", containerName, currentImageInfo.Architecture, runtime.GOARCH)
	}

	log.
		WithPrefix("buildkitd").
		Printf("...Done\n")

	return info, workerInfo, nil
}

// maybeRestart checks whether the there is a different buildkitd image available locally or if
// settings of the current container are different from the provided settings. In either case,
// the container is restarted.
func maybeRestart(
	ctx context.Context,
	log *conslogging.ConsoleLogger,
	image, containerName string,
	eng *engine.Client,
	settings Settings,
	opts ...client.ClientOpt,
) (*client.Info, *client.WorkerInfo, error) {
	bkLog := log.WithPrefix("buildkitd")

	runningContainerInfo, err := GetContainerInfo(ctx, containerName, eng)
	if err != nil {
		return nil, nil, fmt.Errorf("could not get container info: %w", err)
	}

	currentImageInfo, err := GetImageInfo(ctx, runningContainerInfo.Image, eng)
	if err != nil {
		return nil, nil, fmt.Errorf("could not get image info: %w", err)
	}

	if currentImageInfo.Architecture != runtime.GOARCH {
		log.
			WithPrefix("buildkitd").
			Warnf("Warning: currently running %s under architecture %s, but host architecture is %s; "+
				"is DOCKER_DEFAULT_PLATFORM accidentally set?\n", containerName, currentImageInfo.Architecture, runtime.GOARCH)
	}

	containerImageID := runningContainerInfo.ImageID

	availableImageID, err := GetAvailableImageID(ctx, image, eng)
	if err != nil {
		// Could not get available image ID. This happens when a new image tag is given and that
		// tag has not yet been pulled locally. Restarting will cause that tag to be pulled.
		availableImageID = "" // Will cause equality to fail and force a restart.
		// Keep going anyway.
	}

	bkLog.VerbosePrintf("Comparing running container %q image (%q) with available image %q (%q)\n",
		containerName, containerImageID, image, availableImageID)

	switch {
	case containerImageID == availableImageID:
		// Images are the same. Check settings hash.
		var hash string

		hash, err = GetSettingsHash(ctx, containerName, eng)
		if err != nil {
			return nil, nil, fmt.Errorf("could not get settings hash: %w", err)
		}

		var hashOK bool

		hashOK, err = settings.VerifyHash(hash)
		if err != nil {
			return nil, nil, fmt.Errorf("verify hash: %w", err)
		}

		useExistingContainer := false

		if hashOK {
			bkLog.VerbosePrintf("Settings hashes match (%q), no restart required\n", hash)

			useExistingContainer = true
		} else if settings.NoUpdate {
			bkLog.Warnf("Settings do not match; however restart was inhibited. " +
				"This may cause unexpected issues, proceed with caution.\n")

			useExistingContainer = true
		}

		if useExistingContainer {
			opts, err = updateClientSettings(ctx, containerName, eng, &settings, opts...)
			if err != nil {
				return nil, nil, err
			}

			var (
				info       *client.Info
				workerInfo *client.WorkerInfo
			)

			info, workerInfo, err = checkConnection(ctx, settings.BuildkitAddress, 5*time.Second, opts...)
			if err != nil {
				return nil, nil, fmt.Errorf("could not connect to buildkitd: %w", err)
			}

			return info, workerInfo, nil
		}

		bkLog.Printf("Settings do not match. Restarting buildkit daemon with updated settings...\n")
	case settings.NoUpdate:
		bkLog.Printf("Updated image available; however update was inhibited.\n")

		opts, err = updateClientSettings(ctx, containerName, eng, &settings, opts...)
		if err != nil {
			return nil, nil, err
		}

		var (
			info       *client.Info
			workerInfo *client.WorkerInfo
		)

		info, workerInfo, err = checkConnection(ctx, settings.BuildkitAddress, 5*time.Second, opts...)
		if err != nil {
			return nil, nil, fmt.Errorf("could not verify connection to buildkitd container: %w", err)
		}

		return info, workerInfo, nil
	default:
		bkLog.Printf("Updated image available. Restarting buildkit daemon...\n")
	}

	// Replace.
	err = Stop(ctx, containerName, eng)
	if err != nil {
		return nil, nil, fmt.Errorf("could not shut down container %q: %w", containerName, err)
	}

	err = WaitUntilStopped(ctx, containerName, settings.Timeout, eng)
	if err != nil {
		return nil, nil, fmt.Errorf("could not wait for container %q to stop: %w", containerName, err)
	}

	err = Start(ctx, log, image, containerName, eng, settings, false)
	if err != nil {
		return nil, nil, fmt.Errorf("could not start container %q: %w", containerName, err)
	}

	info, workerInfo, err := WaitUntilStarted(ctx, log, containerName, settings.VolumeName, settings, eng, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("could not wait for container %q to start: %w", containerName, err)
	}

	bkLog.Printf("...Done\n")

	return info, workerInfo, nil
}

// RemoveExited removes any stopped or exited buildkitd containers.
func RemoveExited(ctx context.Context, eng *engine.Client, containerName string) error {
	info, err := eng.InspectContainer(ctx, containerName)
	if err != nil {
		return fmt.Errorf("get info to remove exited %s: %w", containerName, err)
	}

	if info.Status == engine.StatusMissing {
		return nil
	}

	err = eng.RemoveContainer(ctx, false, containerName)
	if err != nil {
		return fmt.Errorf("remove exited %s: %w", containerName, err)
	}

	return nil
}

// isEarthInEarth reports whether this CLI is itself running inside a WITH
// DOCKER, which buildkitd/dockerd-wrapper.sh signals by exporting
// EARTH_WITH_DOCKER. When it is, buildkitd needs /sys/fs/cgroup bind-mounted.
//
// NOTE: the deprecated EARTHLY_WITH_DOCKER spelling is still accepted, so that
// an older buildkitd image keeps working; drop it along with the rest of the
// EARTHLY_ support.
func isEarthInEarth() bool {
	v, ok := env.Lookup("WITH_DOCKER")
	if !ok {
		return false
	}

	withDocker, _ := strconv.ParseBool(v)

	return withDocker
}

// Start starts the buildkitd daemon.
func Start(
	ctx context.Context,
	log *conslogging.ConsoleLogger,
	image, containerName string,
	eng *engine.Client,
	settings Settings,
	reset bool,
) error {
	settingsHash, err := settings.Hash()
	if err != nil {
		return fmt.Errorf("settings hash: %w", err)
	}

	err = RemoveExited(ctx, eng, containerName)
	if err != nil {
		return err
	}
	// Pulling is not strictly needed, but it helps display some progress status to the user in
	// case the image is not available locally.
	err = MaybePull(ctx, log, image, eng)
	if err != nil {
		log.
			WithPrefix("buildkitd-pull").
			Printf("Error: %s. Attempting to start buildkitd anyway...\n", err.Error())
		// Keep going - it might still work.
	}

	envs := map[string]string{
		"BUILDKIT_DEBUG":                 strconv.FormatBool(settings.Debug),
		"BUILDKIT_TCP_TRANSPORT_ENABLED": strconv.FormatBool(settings.UseTCP),
		"BUILDKIT_TLS_ENABLED":           strconv.FormatBool(settings.UseTCP && settings.UseTLS),
		"BUILDKIT_MAX_PARALLELISM":       strconv.Itoa(settings.MaxParallelism),
	}

	labels := map[string]string{
		"dev.earthly.settingshash": settingsHash,
	}

	mounts := []engine.Mount{
		{
			Type:     engine.MountVolume,
			Source:   settings.VolumeName,
			Dest:     "/tmp/earthbuild",
			ReadOnly: false,
		},
	}

	ports := []engine.Port{}

	if settings.AdditionalConfig != "" {
		envs["EARTH_ADDITIONAL_BUILDKIT_CONFIG"] = settings.AdditionalConfig
	}

	if settings.IPTables != "" {
		envs["IP_TABLES"] = settings.IPTables
	}

	const localhost = "127.0.0.1"

	withDocker := isEarthInEarth()

	//nolint:nestif // TODO(jhorsts): simplify
	if withDocker {
		// Add /sys/fs/cgroup if it's earth-in-earth.
		mounts = append(mounts, engine.Mount{
			Type:   engine.MountBind,
			Source: "/sys/fs/cgroup",
			Dest:   "/sys/fs/cgroup",
		})
	} else {
		if settings.LocalRegistryAddress != "" {
			var lrURL *url.URL

			lrURL, err = url.Parse(settings.LocalRegistryAddress)
			if err != nil {
				return fmt.Errorf("parse local registry address %q: %w", settings.LocalRegistryAddress, err)
			}

			var hostPort int

			hostPort, err = strconv.Atoi(lrURL.Port())
			if err != nil {
				return fmt.Errorf("invalid port in local registry address %q: %w", settings.LocalRegistryAddress, err)
			}

			ports = append(ports, engine.Port{
				IP:            localhost,
				HostPort:      hostPort,
				ContainerPort: 8371,
				Protocol:      engine.ProtocolTCP,
			})
		}

		var bkURL *url.URL

		bkURL, err = url.Parse(settings.BuildkitAddress)
		if err != nil {
			return fmt.Errorf("error parsing buildkit address url: %w", err)
		}

		if settings.UseTCP {
			var hostPort int

			hostPort, err = strconv.Atoi(bkURL.Port())
			if err != nil {
				return fmt.Errorf("invalid port in buildkit address %q: %w", settings.BuildkitAddress, err)
			}

			ports = append(ports, engine.Port{
				IP:            localhost,
				HostPort:      hostPort,
				ContainerPort: 8372,
				Protocol:      engine.ProtocolTCP,
			})
			if settings.EnableProfiler {
				ports = append(ports, engine.Port{
					IP:            localhost,
					HostPort:      6061, // 6060 is reserved for earth client
					ContainerPort: 6060,
					Protocol:      engine.ProtocolTCP,
				})
			}

			if settings.UseTLS {
				if settings.TLSCA != "" {
					if exists, _ := fileutil.FileExists(settings.TLSCA); !exists {
						return fmt.Errorf("TLS CA file %q is missing: %w", settings.TLSCA, os.ErrNotExist)
					}
				}

				if settings.ServerTLSCert != "" {
					if exists, _ := fileutil.FileExists(settings.ServerTLSCert); !exists {
						return fmt.Errorf("TLS certificate %q is missing: %w", settings.ServerTLSCert, os.ErrNotExist)
					}
				}

				if settings.ServerTLSKey != "" {
					if exists, _ := fileutil.FileExists(settings.ServerTLSKey); !exists {
						return fmt.Errorf("TLS private key %q is missing: %w", settings.ServerTLSKey, os.ErrNotExist)
					}
				}

				if eng.Metadata().Scheme == engine.SchemeApple {
					// Apple Container requires directory-level bind mounts.
					// Mount the certificates directory to /etc/earth-certs.
					certsDir := filepath.Dir(settings.ServerTLSCert)
					mounts = append(mounts, engine.Mount{
						Type:     engine.MountBind,
						Source:   certsDir,
						Dest:     "/etc/earth-certs",
						ReadOnly: true,
					})
				} else {
					if settings.TLSCA != "" {
						mounts = append(mounts, engine.Mount{
							Type:     engine.MountBind,
							Source:   settings.TLSCA,
							Dest:     "/etc/earth-certs/ca_cert.pem",
							ReadOnly: true,
						})
					}

					if settings.ServerTLSCert != "" {
						mounts = append(mounts, engine.Mount{
							Type:     engine.MountBind,
							Source:   settings.ServerTLSCert,
							Dest:     "/etc/earth-certs/buildkit_cert.pem",
							ReadOnly: true,
						})
					}

					if settings.ServerTLSKey != "" {
						mounts = append(mounts, engine.Mount{
							Type:     engine.MountBind,
							Source:   settings.ServerTLSKey,
							Dest:     "/etc/earth-certs/buildkit_key.pem",
							ReadOnly: true,
						})
					}
				}
			}
		}
	}

	if settings.CniMtu > 0 {
		envs["CNI_MTU"] = strconv.Itoa(int(settings.CniMtu))
	}

	if settings.CacheSizeMb > 0 {
		envs["CACHE_SIZE_MB"] = strconv.Itoa(settings.CacheSizeMb)
	}

	if settings.CacheSizePct > 0 {
		envs["CACHE_SIZE_PCT"] = strconv.Itoa(settings.CacheSizePct)
	}

	if settings.CacheKeepDuration > 0 {
		envs["CACHE_KEEP_DURATION"] = strconv.Itoa(settings.CacheKeepDuration)
	}

	if settings.EnableProfiler {
		envs["BUILDKIT_PPROF_ENABLED"] = "true"
	}

	// Apply reset.
	if reset {
		envs["EARTH_RESET_TMP_DIR"] = "true"
	}

	// Ensure buildkitd gets sufficient file descriptors. Docker 29+ (containerd v2)
	// lowered the default from 1048576 to 1024, which starves buildkitd.
	additionalArgs := append([]string{"--ulimit", "nofile=1048576:1048576"}, settings.AdditionalArgs...)

	// Execute.
	err = eng.RunContainer(ctx, engine.ContainerSpec{
		NameOrID:       containerName,
		ImageRef:       image,
		Privileged:     true,
		Envs:           envs,
		Labels:         labels,
		Mounts:         mounts,
		Ports:          ports,
		AdditionalArgs: additionalArgs,
	})
	if err != nil {
		return fmt.Errorf("could not start buildkit: %w", err)
	}

	return nil
}

// Stop stops the buildkitd container.
func Stop(ctx context.Context, containerName string, eng *engine.Client) error {
	return eng.StopContainer(ctx, 10*time.Second, containerName)
}

// IsStarted checks if the buildkitd container has been started.
func IsStarted(ctx context.Context, containerName string, eng *engine.Client) (bool, error) {
	info, err := eng.InspectContainer(ctx, containerName)
	if err != nil {
		return false, err
	}

	return info.Status == engine.StatusRunning, nil
}

// WaitUntilStarted waits until the buildkitd daemon has started and is healthy.
func WaitUntilStarted(
	ctx context.Context,
	log *conslogging.ConsoleLogger,
	containerName, volumeName string,
	settings Settings,
	eng *engine.Client,
	opts ...client.ClientOpt,
) (*client.Info, *client.WorkerInfo, error) {
	opTimeout := settings.Timeout
	address := settings.BuildkitAddress
	// Check that containerName and address match when address connects over the docker-container:// scheme
	if strings.HasPrefix(address, engine.DockerSchemePrefix) {
		expectedAddress := engine.DockerSchemePrefix + containerName
		if address != expectedAddress {
			// This shouldn't happen unless there's a programming error
			return nil, nil, fmt.Errorf("expected address to be %s, but got %s", expectedAddress, address)
		}
	}
	// First, wait for the container to be marked as started.
	ctxTimeout, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

ContainerRunningLoop:
	for {
		select {
		case <-time.After(200 * time.Millisecond):
			isRunning, err := isContainerRunning(ctxTimeout, containerName, eng)
			if err != nil {
				// Has not yet started. Keep waiting.
				continue
			}

			if !isRunning {
				return nil, nil, ErrBuildkitCrashed
			}

			if isRunning {
				break ContainerRunningLoop
			}

		case <-ctxTimeout.Done():
			return nil, nil, fmt.Errorf("timeout %s: buildkitd container did not start", opTimeout)
		}
	}

	opts, err := updateClientSettings(ctxTimeout, containerName, eng, &settings, opts...)
	if err != nil {
		return nil, nil, err
	}

	// Wait for the connection to be available.
	info, workerInfo, err := waitForConnection(ctx, containerName, settings, eng, opts...)

	switch {
	case err != nil && !errors.Is(err, ErrBuildkitConnectionFailure):
		return nil, nil, err
	case err != nil:
		// We timed out. Check if the user has a lot of cache and give buildkit another chance.
		cacheSizeBytes, cacheSizeErr := getCacheSize(ctx, volumeName, eng)
		if cacheSizeErr != nil {
			log.
				WithPrefix("buildkitd").
				Printf("Warning: Could not detect buildkit cache size: %v\n", cacheSizeErr)

			return nil, nil, err
		}

		cacheGigs := cacheSizeBytes / 1024 / 1024 / 1024
		if cacheGigs >= 30 || (cacheGigs >= 10 && runtime.GOOS == "darwin") {
			log.
				WithPrefix("buildkitd").
				Printf("Detected cache size %d GiB. "+
					"It could take a while for buildkit to start up. "+
					"Waiting for another %s before giving up...\n", cacheGigs, opTimeout)
			log.
				WithPrefix("buildkitd").
				Printf("To reduce the size of the cache, you can run one of\n" +
					"\t\tearth config 'global.cache_size_mb' <new-size>\n" +
					"\t\tearth config 'global.cache_size_pct' <new-percent>\n" +
					"These set the BuildKit GC target to a specific value. For more information see " +
					"the earth config reference page: https://docs.earthbuild.dev/docs/earthly-config\n")

			info, workerInfo, err = waitForConnection(ctx, containerName, settings, eng, opts...)
			if err != nil {
				return nil, nil, err
			}

			return info, workerInfo, nil
		}

		return nil, nil, err
	default:
		return info, workerInfo, nil
	}
}

func waitForConnection(
	ctx context.Context,
	containerName string,
	settings Settings,
	eng *engine.Client,
	opts ...client.ClientOpt,
) (*client.Info, *client.WorkerInfo, error) {
	opTimeout := settings.Timeout
	address := settings.BuildkitAddress
	isLocal := engine.IsLocal(settings.BuildkitAddress)

	retryInterval := 200 * time.Millisecond
	if !isLocal {
		retryInterval = 1 * time.Second
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	attemptTimeout := 500 * time.Millisecond
	if !isLocal {
		attemptTimeout = 1 * time.Second
	}

	for {
		select {
		case <-time.After(retryInterval):
			if isLocal {
				// Make sure that our managed buildkit has not crashed on startup.
				info, inspectErr := eng.InspectContainer(ctxTimeout, containerName)
				if inspectErr == nil && (info.Status == engine.StatusExited || info.Status == engine.StatusDead) {
					return nil, nil, ErrBuildkitCrashed
				}
			}

			info, workerInfo, err := checkConnection(ctxTimeout, address, attemptTimeout, opts...)
			if err != nil {
				// Try again.
				attemptTimeout *= 2
				// keep timeout reasonable
				if attemptTimeout > opTimeout {
					attemptTimeout = opTimeout
				}

				continue
			}

			return info, workerInfo, nil
		case <-ctxTimeout.Done():
			info, workerInfo, err := checkConnection(ctx, address, attemptTimeout, opts...)
			if err != nil {
				// We give up.
				return nil, nil, fmt.Errorf("timeout %s: could not connect to buildkit: %w: %w",
					opTimeout, err, ErrBuildkitConnectionFailure)
			}

			return info, workerInfo, nil
		}
	}
}

const unknown = "unknown"

func checkConnection(
	ctx context.Context, address string, timeout time.Duration, opts ...client.ClientOpt,
) (*client.Info, *client.WorkerInfo, error) {
	// Each attempt has limited time to succeed, to prevent hanging for too long
	// here.
	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)

	var (
		mu         sync.Mutex // protects the vars below
		connErr    = errors.New("timeout")
		info       *client.Info
		workerInfo *client.WorkerInfo
	)

	go func() {
		defer cancel()

		bkClient, err := client.New(ctxTimeout, address, opts...)
		if err != nil {
			mu.Lock()

			connErr = err

			mu.Unlock()

			return
		}
		defer bkClient.Close()
		// Use ListWorkers for backwards compatibility. (Info is relatively new)
		ws, err := bkClient.ListWorkers(ctxTimeout)
		if err != nil {
			mu.Lock()

			connErr = err

			mu.Unlock()

			return
		}

		if len(ws) == 0 {
			mu.Lock()

			connErr = errors.New("no workers")

			mu.Unlock()

			return
		}

		// Success.
		mu.Lock()
		defer mu.Unlock()

		connErr = nil
		workerInfo = ws[0]

		info, err = bkClient.Info(ctxTimeout)
		if err != nil {
			s, ok := status.FromError(err)
			if ok && s.Code() == codes.Unimplemented {
				// Degrade gracefully.
				info = &client.Info{
					BuildkitVersion: client.BuildkitVersion{
						Version:  unknown,
						Package:  unknown,
						Revision: unknown,
					},
				}
			} else {
				connErr = err
				return
			}
		}
	}()

	<-ctxTimeout.Done() // timeout or goroutine finished

	mu.Lock()
	defer mu.Unlock()

	if connErr != nil {
		return nil, nil, connErr
	}

	return info, workerInfo, nil
}

// MaybePull checks whether an image is available locally and pulls it if it is not.
func MaybePull(
	ctx context.Context, log *conslogging.ConsoleLogger, image string, eng *engine.Client,
) error {
	info, err := eng.InspectImage(ctx, image)
	if err != nil {
		return fmt.Errorf("could not get container info: %w", err)
	}

	if info.ID != "" { // the presence of an item implies its local
		return nil
	}

	log.
		WithPrefix("buildkitd-pull").
		Printf("Pulling buildkitd image...\n")

	err = eng.PullImage(ctx, image)
	if err != nil {
		return fmt.Errorf("could not pull %s: %w", image, err)
	}

	log.
		WithPrefix("buildkitd-pull").
		Printf("...Done\n")

	return nil
}

// GetDockerVersion returns the docker version command output.
func GetDockerVersion(ctx context.Context, eng *engine.Client) (string, error) {
	info, err := eng.Version(ctx)
	if err != nil {
		return "", fmt.Errorf("get version from engine: %w", err)
	}

	return fmt.Sprintf("%#v", info), nil
}

// GetLogs returns buildkitd daemon container logs.
func GetLogs(
	ctx context.Context, containerName string, eng *engine.Client, settings Settings,
) (string, error) {
	if !engine.IsLocal(settings.BuildkitAddress) {
		return "", nil
	}

	logs, err := eng.ContainerLogs(ctx, containerName)
	if err != nil {
		return "", fmt.Errorf(": %w", err)
	}

	return logs.Stdout, nil
}

// WaitUntilStopped waits until the buildkitd daemon has stopped.
func WaitUntilStopped(
	ctx context.Context, containerName string, opTimeout time.Duration, eng *engine.Client,
) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	for {
		select {
		case <-time.After(200 * time.Millisecond):
			isRunning, err := isContainerRunning(ctxTimeout, containerName, eng)
			if err != nil {
				// The container can no longer be found at all.
				return nil
			}

			if !isRunning {
				return nil
			}
		case <-ctxTimeout.Done():
			return fmt.Errorf("timeout %s: buildkitd did not stop", opTimeout)
		}
	}
}

// GetSettingsHash fetches the hash of the currently running buildkitd container.
func GetSettingsHash(ctx context.Context, containerName string, eng *engine.Client) (string, error) {
	info, err := eng.InspectContainer(ctx, containerName)
	if err != nil {
		return "", fmt.Errorf("get container info for settings: %w", err)
	}

	if info.Status == engine.StatusMissing {
		return "", fmt.Errorf("settings hash for container %s was not found", containerName)
	}

	return strings.TrimSpace(info.Labels["dev.earthly.settingshash"]), nil
}

// GetContainerInfo inspects the running container (running under containerName).
func GetContainerInfo(
	ctx context.Context, containerName string, eng *engine.Client,
) (engine.Container, error) {
	info, err := eng.InspectContainer(ctx, containerName)
	if err != nil {
		return engine.Container{}, fmt.Errorf("get container info for current container image ID: %w", err)
	}

	if info.Status == engine.StatusMissing {
		return engine.Container{}, fmt.Errorf("info for container %s was not found", containerName)
	}

	return info, nil
}

// GetImageInfo inspects an image.
func GetImageInfo(
	ctx context.Context, image string, eng *engine.Client,
) (engine.Image, error) {
	info, err := eng.InspectImage(ctx, image)
	if err != nil {
		return engine.Image{}, fmt.Errorf("get image info %s: %w", image, err)
	}

	if info.ID == "" {
		return engine.Image{}, fmt.Errorf("info for image %s was not found", image)
	}

	return info, nil
}

// GetAvailableImageID fetches the ID of the image buildkitd image available.
func GetAvailableImageID(ctx context.Context, image string, eng *engine.Client) (string, error) {
	info, err := eng.InspectImage(ctx, image)
	if err != nil {
		return "", fmt.Errorf("get output for available image ID: %w", err)
	}

	if info.ID == "" {
		return "", fmt.Errorf("image ID for %s was not found", image)
	}

	return info.ID, nil
}

func isContainerRunning(ctx context.Context, containerName string, eng *engine.Client) (bool, error) {
	info, err := eng.InspectContainer(ctx, containerName)
	if err != nil {
		return false, fmt.Errorf("failed to get container info while checking if running: %w", err)
	}

	if info.Status == engine.StatusExited || info.Status == engine.StatusDead {
		return false, nil
	}

	if info.Status == engine.StatusRunning {
		return true, nil
	}

	return false, fmt.Errorf("container %s is in state %q", containerName, info.Status)
}

func printBuildkitInfo(
	log *conslogging.ConsoleLogger,
	info *client.Info,
	workerInfo *client.WorkerInfo,
	earthVersion string,
	isLocal, hasConfiguredCacheSize bool,
) {
	// Print most of this stuff only for remote buildkits
	printFun := log.Printf
	if isLocal {
		printFun = log.VerbosePrintf
	}

	//nolint:nestif // TODO(jhorsts): simplify
	if info.BuildkitVersion.Version == unknown {
		log.Warnf(
			"Warning: Buildkit version is unknown. This usually means that " +
				"it's from a version lower than earth Buildkit v0.6.20",
		)
	} else {
		printFun(
			"Version %s %s %s",
			info.BuildkitVersion.Package, info.BuildkitVersion.Version, info.BuildkitVersion.Revision,
		)

		const buildkitPackage = "github.com/EarthBuild/buildkit"

		if !strings.EqualFold(info.BuildkitVersion.Package, buildkitPackage) {
			log.Warnf("Using a non-EarthBuild version of Buildkit is not supported.\n"+
				"  Supported: %s\n"+
				"  Detected:  %s", buildkitPackage, info.BuildkitVersion.Package)
		} else if strings.TrimSuffix(info.BuildkitVersion.Version, "-ticktock") != earthVersion {
			if isLocal {
				// For local buildkits we expect perfect version match.
				log.Warnf(
					"Warning: Buildkit version (%s) is different from earth version (%s)",
					info.BuildkitVersion.Version, earthVersion,
				)
			} else {
				compatible := true

				if !semver.IsValid(info.BuildkitVersion.Version) {
					log.VerbosePrintf("Warning: could not parse buildkit version: %s", info.BuildkitVersion.Version)

					compatible = false
				}

				if !semver.IsValid(earthVersion) {
					log.VerbosePrintf("Warning: could not parse earth version: %s", earthVersion)

					compatible = false
				}

				compatible = compatible && semver.MajorMinor(info.BuildkitVersion.Version) == semver.MajorMinor(earthVersion)
				if compatible {
					log.VerbosePrintf("Buildkit version (%s) is compatible with earth version (%s)",
						info.BuildkitVersion.Version, earthVersion)
				} else {
					log.Warnf("Warning: Buildkit version (%s) is not compatible with earth version (%s)",
						info.BuildkitVersion.Version, earthVersion)
				}
			}
		}
	}

	ps := make([]string, len(workerInfo.Platforms))
	for i, p := range workerInfo.Platforms {
		ps[i] = platforms.Format(p)
	}

	if len(ps) > 0 {
		printFun("Platforms: %s (native) %s", ps[0], strings.Join(ps[1:], " "))
	}

	load := workerInfo.ParallelismCurrent + workerInfo.ParallelismWaiting
	printFun(buildkitutil.FormatUtilization(info.NumSessions, load, workerInfo.ParallelismMax))

	switch {
	case workerInfo.ParallelismWaiting > 5:
		log.Warnf("Warning: Currently under heavy load. Performance will be affected")
	case workerInfo.ParallelismWaiting > 0:
		log.Printf("Note: Currently under significant load. Performance will be affected")
	default:
	}

	ld := time.Duration(0)
	if workerInfo.GCAnalytics.LastEndTime != nil &&
		workerInfo.GCAnalytics.LastStartTime != nil {
		ld = workerInfo.GCAnalytics.LastEndTime.Sub(*workerInfo.GCAnalytics.LastStartTime)
	}

	printFun(
		"GC stats: %s cache, avg GC duration %v, all-time GC duration %v, last GC duration %v, last cleared %v",
		humanizeBytes(workerInfo.GCAnalytics.LastSizeBefore),
		workerInfo.GCAnalytics.AvgDuration,
		workerInfo.GCAnalytics.AllTimeDuration,
		ld,
		humanizeBytes(workerInfo.GCAnalytics.LastSizeCleared),
	)

	if workerInfo.GCAnalytics.CurrentStartTime != nil {
		d := time.Since(*workerInfo.GCAnalytics.CurrentStartTime).Round(time.Second)
		switch {
		case d > 5*time.Minute:
			log.Warnf("Warning: GC has been running for a long time, started %v ago", d)
		case d > 1*time.Minute:
			log.Printf("GC currently ongoing, started %v ago", d)
		default:
		}
	}

	if isLocal && !hasConfiguredCacheSize {
		if size, ok := getGCPolicySize(workerInfo); ok && size < minRecommendedCacheSize {
			log.Warnf("Configured cache size of %s is smaller than the minimum recommended size of %s",
				units.HumanSize(float64(size)), units.HumanSize(minRecommendedCacheSize))
			log.Warnf("Please consider increasing the cache size: https://docs.earthbuild.dev/docs/caching/managing-cache")
		}
	}
}

func getGCPolicySize(workerInfo *client.WorkerInfo) (int64, bool) {
	for _, p := range workerInfo.GCPolicy {
		if p.All {
			return p.KeepBytes, true
		}
	}

	return 0, false
}

// getCacheSize returns the size of the earthbuild cache in bytes.
func getCacheSize(ctx context.Context, volumeName string, eng *engine.Client) (int, error) {
	info, err := eng.InspectVolume(ctx, volumeName)
	if err != nil {
		return 0, fmt.Errorf("failed to get volume info for cache size %s: %w", volumeName, err)
	}

	return int(info.SizeBytes), nil // #nosec G115
}

func addRequiredOpts(settings Settings, opts ...client.ClientOpt) ([]client.ClientOpt, error) {
	server, err := url.Parse(settings.BuildkitAddress)
	if err != nil {
		return []client.ClientOpt{}, fmt.Errorf("failed to parse buildkit url %s: %w", settings.BuildkitAddress, err)
	}

	if !settings.UseTCP || !settings.UseTLS {
		return opts, nil
	}

	if settings.TLSCA == "" && settings.ClientTLSCert == "" && settings.ClientTLSKey == "" {
		return append(opts, client.WithServerConfigSystem("")), nil
	}

	serverName := server.Hostname()
	if engine.IsLocal(settings.BuildkitAddress) {
		serverName = "localhost"
	}

	opts = append(
		opts,
		client.WithCredentials(settings.ClientTLSCert, settings.ClientTLSKey),
		client.WithServerConfig(serverName, settings.TLSCA),
	)

	return opts, nil
}

func updateContainerEndpoints(ctx context.Context, containerName string, eng *engine.Client, settings *Settings) {
	if eng == nil || eng.Metadata().Scheme != engine.SchemeApple {
		return
	}

	info, err := eng.InspectContainer(ctx, containerName)
	if err == nil && info.IPs["bridge"] != "" {
		settings.BuildkitAddress = "tcp://" + net.JoinHostPort(info.IPs["bridge"], "8372")
		if settings.LocalRegistryAddress != "" {
			settings.LocalRegistryAddress = "http://" + net.JoinHostPort(info.IPs["bridge"], "8371")
		}
	}
}

func updateClientSettings(
	ctx context.Context,
	containerName string,
	eng *engine.Client,
	settings *Settings,
	opts ...client.ClientOpt,
) ([]client.ClientOpt, error) {
	updateContainerEndpoints(ctx, containerName, eng, settings)

	requiredOpts, err := addRequiredOpts(*settings, opts...)
	if err != nil {
		return nil, fmt.Errorf("add required client opts: %w", err)
	}

	return requiredOpts, nil
}

func containsAny(hs string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(hs, n) {
			return true
		}
	}

	return false
}

func humanizeBytes(v int64) string {
	var bytes uint64

	if v > 0 {
		bytes = uint64(v)
	}

	return humanize.Bytes(bytes)
}

func engineName(eng *engine.Client) string {
	meta := eng.Metadata()

	return cmp.Or(meta.Name, meta.Binary)
}

func engineContainer(eng *engine.Client) string {
	name := engineName(eng)
	if strings.HasSuffix(strings.ToLower(name), "container") {
		return name
	}

	return name + " container"
}
