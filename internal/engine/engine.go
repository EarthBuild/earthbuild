// Package engine provides container engine abstractions and implementations.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
)

// ErrNotInitialized is returned when the container engine is not initialized.
var ErrNotInitialized = errors.New("container engine not initialized")

var (
	errInvalidURL    = errors.New("invalid URL")
	errInvalidScheme = errors.New("invalid scheme")
	errMissingPort   = errors.New("missing port")
)

// engineDriver is an unexported interface specifying all the container operations EarthBuild needs to perform.
type engineDriver interface {
	IsAvailable(ctx context.Context) bool
	Metadata() Metadata
	Version(ctx context.Context) (Version, error)

	ListContainers(ctx context.Context) ([]Container, error)
	InspectContainer(ctx context.Context, namesOrIDs ...string) (map[string]Container, error)
	RemoveContainer(ctx context.Context, force bool, namesOrIDs ...string) error
	StopContainer(ctx context.Context, timeout time.Duration, namesOrIDs ...string) error
	Logs(ctx context.Context, namesOrIDs ...string) (map[string]Logs, error)
	RunContainer(ctx context.Context, specs ...ContainerSpec) error

	InspectImage(ctx context.Context, refs ...string) (map[string]Image, error)
	PullImage(ctx context.Context, refs ...string) error
	RemoveImage(ctx context.Context, force bool, refs ...string) error
	TagImage(ctx context.Context, tags ...Tag) error
	LoadImage(ctx context.Context, images ...io.Reader) error
	ImageLoadCommand(filename string) string

	InspectVolume(ctx context.Context, volumeNames ...string) (map[string]Volume, error)
}

// Client is the concrete struct used for interacting with the container engine.
type Client struct {
	driver engineDriver
}

// IsAvailable returns true if the container engine is installed and accessible.
func (c *Client) IsAvailable(ctx context.Context) bool { return c.driver.IsAvailable(ctx) }

// Metadata returns engine metadata and endpoints.
func (c *Client) Metadata() Metadata { return c.driver.Metadata() }

// Version returns version information for the container engine.
func (c *Client) Version(ctx context.Context) (Version, error) { return c.driver.Version(ctx) }

// ListContainers lists running or existing containers.
func (c *Client) ListContainers(ctx context.Context) ([]Container, error) {
	return c.driver.ListContainers(ctx)
}

// InspectContainer returns information about specific containers.
func (c *Client) InspectContainer(ctx context.Context, namesOrIDs ...string) (map[string]Container, error) {
	return c.driver.InspectContainer(ctx, namesOrIDs...)
}

// RemoveContainer removes one or more containers.
func (c *Client) RemoveContainer(ctx context.Context, force bool, namesOrIDs ...string) error {
	return c.driver.RemoveContainer(ctx, force, namesOrIDs...)
}

// StopContainer stops one or more running containers.
func (c *Client) StopContainer(ctx context.Context, timeout time.Duration, namesOrIDs ...string) error {
	return c.driver.StopContainer(ctx, timeout, namesOrIDs...)
}

// Logs returns logs for specified containers.
func (c *Client) Logs(ctx context.Context, namesOrIDs ...string) (map[string]Logs, error) {
	return c.driver.Logs(ctx, namesOrIDs...)
}

// RunContainer starts containers according to the given configurations.
func (c *Client) RunContainer(ctx context.Context, specs ...ContainerSpec) error {
	return c.driver.RunContainer(ctx, specs...)
}

// InspectImage retrieves image metadata for the given references.
func (c *Client) InspectImage(ctx context.Context, refs ...string) (map[string]Image, error) {
	return c.driver.InspectImage(ctx, refs...)
}

// PullImage pulls images from a registry.
func (c *Client) PullImage(ctx context.Context, refs ...string) error {
	return c.driver.PullImage(ctx, refs...)
}

// RemoveImage removes images.
func (c *Client) RemoveImage(ctx context.Context, force bool, refs ...string) error {
	return c.driver.RemoveImage(ctx, force, refs...)
}

// TagImage tags an image with target references.
func (c *Client) TagImage(ctx context.Context, tags ...Tag) error {
	return c.driver.TagImage(ctx, tags...)
}

// LoadImage loads images from tar streams into the container engine.
func (c *Client) LoadImage(ctx context.Context, images ...io.Reader) error {
	return c.driver.LoadImage(ctx, images...)
}

// ImageLoadCommand returns the shell command used to load an image archive.
func (c *Client) ImageLoadCommand(filename string) string {
	return c.driver.ImageLoadCommand(filename)
}

// InspectVolume retrieves metadata for specified volume names.
func (c *Client) InspectVolume(ctx context.Context, volumeNames ...string) (map[string]Volume, error) {
	return c.driver.InspectVolume(ctx, volumeNames...)
}

// Config is the configuration needed to bring up a given container engine. Includes logging and needed information to
// calculate URLs to reach the container.
type Config struct {
	BuildkitHostCLIValue       string
	BuildkitHostFileValue      string
	LocalRegistryHostFileValue string
	LocalContainerName         string
	Console                    conslogging.ConsoleLogger
	DefaultPort                int
}

// Container contains things we may care about from inspect output for a given container.
type Container struct {
	Created  time.Time
	IPs      map[string]string
	Labels   map[string]string
	ID       string
	Name     string
	Platform string
	Status   string
	Image    string
	ImageID  string
	Ports    []string
}

const (
	// StatusMissing signifies that a container is not present.
	StatusMissing = "missing"

	// StatusCreated signifies that a container has been created, but not started.
	StatusCreated = "created"

	// StatusRestarting signifies that a container has started, stopped, and is currently restarting.
	StatusRestarting = "restarting"

	// StatusRunning signifies that a container is currently running.
	StatusRunning = "running"

	// StatusRemoving signifies that a container has exited and is currently being removed.
	StatusRemoving = "removing"

	// StatusPaused means a container has been suspended.
	StatusPaused = "paused"

	// StatusExited means that a container was running and has been stopped, but not removed.
	StatusExited = "exited"

	// StatusDead means that a container was killed for some reason and has not yet been restarted.
	StatusDead = "dead"
)

// Logs contains the stdout and stderr logs of a given container.
type Logs struct {
	Stdout string
	Stderr string
}

// Version contains the client and server information for a container engine.
type Version struct {
	ClientVersion    string
	ClientAPIVersion string
	ClientPlatform   string

	ServerVersion    string
	ServerAPIVersion string
	ServerPlatform   string
	ServerAddress    string
}

// Image contains information about a given image ref, including all relevant tags.
type Image struct {
	ID           string
	OS           string
	Architecture string
	Tags         []string
}

// Volume contains information about a given volume, including its name,
// where its mounted from, and the size of the volume.
type Volume struct {
	Name       string
	Mountpoint string
	SizeBytes  uint64
}

// Tag contains a source and target ref, used for tagging an image.
// It means that the SourceRef is tagged as the value in TargetRef.
type Tag struct {
	SourceRef string
	TargetRef string
}

// MountType constrains the kinds of mounts the Engine API needs to support.
// Current valid values are bind and volume.
type MountType string

const (
	// MountBind is the bind MountType.
	MountBind = MountType("bind")

	// MountVolume is the volume MountType.
	MountVolume = MountType("volume")
)

// Mount contains the needed data to construct a mount for a container in a given engine.
type Mount struct {
	Type     MountType
	Source   string
	Dest     string
	ReadOnly bool
}

// ProtocolType constrains the kinds of protocols the engine API needs to support.
// Current valid values are tcp and udp.
type ProtocolType string

const (
	// ProtocolTCP is the TCP protocol type.
	ProtocolTCP = ProtocolType("tcp")

	// ProtocolUDP is the UDP protocol type.
	ProtocolUDP = ProtocolType("udp")
)

// Port contains the needed data to publish a port for a given container in a given engine.
type Port struct {
	IP            string
	Protocol      ProtocolType
	HostPort      int
	ContainerPort int
}

// ContainerSpec contains the information needed to create and run a container.
type ContainerSpec struct {
	Envs          map[string]string
	Labels        map[string]string
	NameOrID      string
	ImageRef      string
	Mounts        []Mount
	Ports         []Port
	ContainerArgs []string
	// We would like to shift to the non-shell providers. However, we do provide an option for supplying
	// additional arguments to the CLI when starting buildkit. While this allowed great flexibility, we
	// also do not know what or how it is being used. This gives us the option to support those users until
	// we decide to pull the plug. This argument is ignored by non-shell providers.
	AdditionalArgs []string
	Privileged     bool
}

// Driver identifies a supported container backend driver.
type Driver string

const (
	// Auto specifies automatic engine detection.
	Auto Driver = "auto"

	// Docker specifies the docker driver.
	Docker Driver = "docker"

	// DockerShell is an alias for Docker for backwards compatibility.
	DockerShell Driver = "docker-shell"

	// Podman specifies the podman driver.
	Podman Driver = "podman"

	// PodmanShell is an alias for Podman for backwards compatibility.
	PodmanShell Driver = "podman-shell"

	// AppleContainer specifies the apple container driver.
	AppleContainer Driver = "apple-container"

	// Stub is for when there is no valid container provider (e.g. tests or satellite builds).
	Stub Driver = "stub"
)

// Metadata contains information describing an engine implementation.
type Metadata struct {
	// Endpoints holds network addresses for communicating with the engine.
	Endpoints Endpoints

	// Name is the display name of the engine (e.g. "Docker", "Podman", "Apple Container").
	Name string

	// Binary is the executable name used for CLI operations (e.g. "docker", "podman", "container").
	Binary string

	// Scheme is the connection protocol scheme used by the engine (e.g. SchemeDocker).
	Scheme Scheme

	// Transport is the communication mechanism used by the engine.
	Transport Transport

	// IsPodman indicates if the underlying engine is Podman, even if accessed via a generic alias.
	IsPodman bool
}

// Transport represents the communication mechanism used by the container engine.
type Transport int

const (
	// TransportShell signifies that a given engine executes operations via an external CLI binary.
	TransportShell Transport = iota

	// TransportAPI signifies that a given engine executes operations via a direct daemon API or socket.
	TransportAPI
)

// String returns the string representation of the transport mechanism.
func (t Transport) String() string {
	switch t {
	case TransportShell:
		return "shell"
	case TransportAPI:
		return "api"
	default:
		return "unknown"
	}
}

// Scheme represents a supported container connection protocol.
type Scheme int

const (
	// SchemeInvalid indicates an uninitialized or unsupported scheme.
	SchemeInvalid Scheme = iota

	// SchemeTCP is the TCP protocol scheme.
	SchemeTCP

	// SchemeDocker is the scheme used for docker-container addresses.
	SchemeDocker

	// SchemePodman is the scheme used for podman-container addresses.
	SchemePodman

	// SchemeApple is the scheme used for apple-container addresses.
	SchemeApple
)

// String implements fmt.Stringer for URI construction and formatting.
func (s Scheme) String() string {
	switch s {
	case SchemeInvalid:
		return "invalid"
	case SchemeTCP:
		return "tcp"
	case SchemeDocker:
		return "docker-container"
	case SchemePodman:
		return "podman-container"
	case SchemeApple:
		return "apple-container"
	default:
		return "invalid"
	}
}

// ParseScheme parses and validates a raw scheme string.
func ParseScheme(s string) (Scheme, error) {
	switch s {
	case "tcp":
		return SchemeTCP, nil
	case "docker-container":
		return SchemeDocker, nil
	case "podman-container":
		return SchemePodman, nil
	case "apple-container":
		return SchemeApple, nil
	default:
		return SchemeInvalid, fmt.Errorf(
			"%s is not a valid scheme. "+
				"Only tcp, docker-container, podman-container, or apple-container is allowed at this time: %w",
			s,
			errInvalidScheme,
		)
	}
}

const (
	// TCPAddressFmt is the address at which the daemon is available when using TCP.
	TCPAddressFmt = "tcp://127.0.0.1:%d"

	// DockerSchemePrefix is used to construct the buildkit address for local docker-based connections.
	DockerSchemePrefix = "docker-container://"
)

// Endpoints contains the relevant host URLs to contact a container engine or buildkit daemon.
type Endpoints struct {
	BuildkitHost      *url.URL
	LocalRegistryHost *url.URL
}

// ResolveEndpoints calculates and validates buildkit and registry URLs based on the given configuration.
func ResolveEndpoints(driver Driver, cfg *Config) (Endpoints, error) {
	calculatedBuildkitHost := cfg.BuildkitHostCLIValue
	if cfg.BuildkitHostCLIValue == "" {
		if cfg.BuildkitHostFileValue != "" {
			calculatedBuildkitHost = cfg.BuildkitHostFileValue
		} else {
			var err error

			calculatedBuildkitHost, err = DefaultAddress(driver, cfg.LocalContainerName, cfg.DefaultPort)
			if err != nil {
				return Endpoints{}, fmt.Errorf("could not validate default address: %w", err)
			}
		}
	}

	bkURL, err := ParseURL(calculatedBuildkitHost)
	if err != nil {
		return Endpoints{}, err
	}

	lrURL := &url.URL{}
	if IsLocal(calculatedBuildkitHost) && cfg.LocalRegistryHostFileValue != "" {
		// Local registry only matters when local, and specified.
		lrURL, err = ParseURL(cfg.LocalRegistryHostFileValue)
		if err != nil {
			return Endpoints{}, err
		}

		if !IsLocal(cfg.LocalRegistryHostFileValue) && bkURL.Hostname() != lrURL.Hostname() {
			format := "Buildkit and local registry URLs are pointed at different hosts (%s vs. %s)"
			cfg.Console.Warnf(format, bkURL.Hostname(), lrURL.Hostname())
		}
	} else if cfg.LocalRegistryHostFileValue != "" {
		cfg.Console.
			VerbosePrintf("Local registry host is specified while using remote buildkit. Local registry will not be used.")
	}

	return Endpoints{
		BuildkitHost:      bkURL,
		LocalRegistryHost: lrURL,
	}, nil
}

// DefaultAddress returns an address (signifying the desired/default transport)
// for a given container driver.
func DefaultAddress(driver Driver, localContainerName string, defaultPort int) (string, error) {
	switch driver {
	case DockerShell, Docker:
		return DockerSchemePrefix + localContainerName, nil

	case PodmanShell, Podman:
		// Podman only works over TCP. There are weird errors when trying to use the provided helper from buildkit.
		return fmt.Sprintf(TCPAddressFmt, defaultPort), nil

	case AppleContainer:
		// Apple container only works over TCP.
		return fmt.Sprintf(TCPAddressFmt, defaultPort), nil

	case Stub:
		return DockerSchemePrefix + localContainerName, nil // Maintain old behavior

	case Auto:
		return "", fmt.Errorf("cannot determine default buildkit address for %s", driver)
	}

	return "", fmt.Errorf("no default buildkit address for %s", driver)
}

// ParseURL parses and checks if a URL has an allowed scheme and required port.
func ParseURL(addr string) (*url.URL, error) {
	parsed, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", addr, errInvalidURL)
	}

	scheme, err := ParseScheme(parsed.Scheme)
	if err != nil {
		return nil, err
	}

	if parsed.Port() == "" && scheme == SchemeTCP {
		return nil, fmt.Errorf("%s does not contain a port number: %w", addr, errMissingPort)
	}

	return parsed, nil
}

// IsLocal parses a URL and returns whether it is considered a local buildkit host + port that we
// need to manage ourselves.
func IsLocal(addr string) bool {
	if strings.HasPrefix(addr, DockerSchemePrefix) ||
		strings.HasPrefix(addr, "podman-container://") ||
		strings.HasPrefix(addr, "apple-container://") {
		return true
	}

	parsed, err := url.Parse(addr)
	if err != nil {
		return false
	}

	hostname := parsed.Hostname()
	// These need to match what we put in our certificates.
	return hostname == "127.0.0.1" || // The only IPv4 Loopback we honor. Because we need to include it in the TLS cert.
		hostname == "localhost" || // Convention. Users hostname omitted; this is only really here for convenience.
		hostname == "::1" // IPv6 loopback without calling net.IPv6loopback.String()
}

// New returns a container client given a driver. This includes automatic detection.
func New(ctx context.Context, driver Driver, cfg *Config) (*Client, error) {
	var (
		drv engineDriver
		err error
	)

	switch driver {
	case Auto, "":
		return autodetectEngine(ctx, cfg)
	case DockerShell, Docker:
		drv, err = newDockerEngine(ctx, cfg)
	case PodmanShell, Podman:
		drv, err = newPodmanEngine(ctx, cfg)
	case AppleContainer:
		drv, err = newAppleEngine(ctx, cfg)
	case Stub:
		drv, err = newStubEngine(cfg)
	default:
		return nil, fmt.Errorf("%s is not a supported container driver", driver)
	}

	if err != nil {
		return nil, err
	}

	return &Client{driver: drv}, nil
}

func autodetectEngine(ctx context.Context, cfg *Config) (*Client, error) {
	var errs error

	for _, driver := range [...]Driver{
		DockerShell,
		PodmanShell,
		AppleContainer,
	} {
		client, err := New(ctx, driver, cfg)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}

		if !client.IsAvailable(ctx) {
			continue
		}

		if client.Metadata().IsPodman && driver == DockerShell {
			// Docker CLI works, but it's likely podman making itself available via docker CLI.
			continue
		}

		return client, nil
	}

	return nil, fmt.Errorf("failed to autodetect a supported container engine: %w", errs)
}
