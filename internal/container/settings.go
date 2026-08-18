package container

import (
	"fmt"
	"net/url"
	"strings"
)

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
	case DriverDockerShell, DriverDocker:
		return DockerSchemePrefix + localContainerName, nil

	case DriverPodmanShell, DriverPodman:
		// Podman only works over TCP. There are weird errors when trying to use the provided helper from buildkit.
		return fmt.Sprintf(TCPAddressFmt, defaultPort), nil

	case DriverAppleContainerShell, DriverAppleContainer:
		// Apple container only works over TCP.
		return fmt.Sprintf(TCPAddressFmt, defaultPort), nil

	case DriverStub:
		return DockerSchemePrefix + localContainerName, nil // Maintain old behavior

	case DriverAuto:
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
