package base

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/EarthBuild/earthbuild/buildkitd"
	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/EarthBuild/earthbuild/util/cliutil"
	"github.com/EarthBuild/earthbuild/util/fileutil"
	"github.com/urfave/cli/v3"
)

// InitBuildkit initializes the buildkit daemon settings for the given command.
func (cli *CLI) InitBuildkit(cmd *cli.Command) error {
	// command line option overrides the config which overrides the default value
	if !cmd.IsSet("buildkit-image") && cli.Cfg().Global.BuildkitImage != "" {
		cli.Flags().BuildkitdImage = cli.Cfg().Global.BuildkitImage
	}

	if cli.Flags().BuildkitdImage == "" {
		cli.Flags().BuildkitdImage = "ghcr.io/earthbuild/earthbuild:buildkitd-dev-main"
	}

	if cli.Flags().UseTickTockBuildkitImage {
		if cmd.IsSet("buildkit-image") {
			return errors.New("the --buildkit-image and --ticktock flags are mutually exclusive")
		}

		if cli.Cfg().Global.BuildkitImage != "" {
			return errors.New("the --ticktock flag cannot be used in combination with the buildkit_image config option")
		}

		cli.Flags().BuildkitdImage += "-ticktock"
	}

	bkURL, err := url.Parse(cli.Flags().BuildkitHost) // Not validated because we already did that when we calculated it.
	if err != nil {
		return fmt.Errorf("failed to parse generated buildkit URL: %w", err)
	}

	useTCP := engine.UsesTCP(bkURL.Scheme)

	if useTCP && cli.Cfg().Global.TLSEnabled {
		// Auto-generate mTLS certificates on first run when connecting via TCP (e.g. Apple Container
		// or local TCP daemon) so that users do not need to run 'earth bootstrap' beforehand.
		if exists, _ := fileutil.FileExists(cli.Cfg().Global.TLSCACert); !exists {
			err = buildkitd.GenCerts(*cli.Cfg(), "127.0.0.1")
			if err != nil {
				return fmt.Errorf("auto-generate TLS certs: %w", err)
			}
		}

		cli.Flags().BuildkitdSettings.ClientTLSCert = cli.Cfg().Global.ClientTLSCert
		cli.Flags().BuildkitdSettings.ClientTLSKey = cli.Cfg().Global.ClientTLSKey
		cli.Flags().BuildkitdSettings.TLSCA = cli.Cfg().Global.TLSCACert
		cli.Flags().BuildkitdSettings.ServerTLSCert = cli.Cfg().Global.ServerTLSCert
		cli.Flags().BuildkitdSettings.ServerTLSKey = cli.Cfg().Global.ServerTLSKey
	}

	cli.Flags().BuildkitdSettings.AdditionalArgs = cli.Cfg().Global.BuildkitAdditionalArgs
	cli.Flags().BuildkitdSettings.AdditionalConfig = cli.Cfg().Global.BuildkitAdditionalConfig
	cli.Flags().BuildkitdSettings.Timeout = time.Duration(cli.Cfg().Global.BuildkitRestartTimeoutS) * time.Second
	cli.Flags().BuildkitdSettings.Debug = cli.Flags().Debug
	cli.Flags().BuildkitdSettings.BuildkitAddr = cli.Flags().BuildkitHost
	cli.Flags().BuildkitdSettings.LocalRegistryAddr = cli.Flags().LocalRegistryHost
	cli.Flags().BuildkitdSettings.UseTCP = useTCP
	cli.Flags().BuildkitdSettings.UseTLS = cli.Cfg().Global.TLSEnabled
	cli.Flags().BuildkitdSettings.MaxParallelism = cli.Cfg().Global.BuildkitMaxParallelism
	cli.Flags().BuildkitdSettings.CacheSizeMb = cli.Cfg().Global.BuildkitCacheSizeMb
	cli.Flags().BuildkitdSettings.CacheSizePct = cli.Cfg().Global.BuildkitCacheSizePct
	cli.Flags().BuildkitdSettings.CacheKeepDuration = cli.Cfg().Global.BuildkitCacheKeepDurationS
	cli.Flags().BuildkitdSettings.EnableProfiler = cli.Flags().EnableProfiler
	cli.Flags().BuildkitdSettings.NoUpdate = cli.Flags().NoBuildkitUpdate

	// ensure the MTU is something allowable in IPv4, cap enforced by type. Zero is autodetect.
	if cli.Cfg().Global.CniMtu != 0 && cli.Cfg().Global.CniMtu < 68 {
		return errors.New("invalid overridden MTU size")
	}

	cli.Flags().BuildkitdSettings.CniMtu = cli.Cfg().Global.CniMtu

	if cli.Cfg().Global.IPTables != "" &&
		cli.Cfg().Global.IPTables != "iptables-legacy" &&
		cli.Cfg().Global.IPTables != "iptables-nft" {
		return errors.New(`invalid overridden iptables name. Valid values are "iptables-legacy" or "iptables-nft"`)
	}

	cli.Flags().BuildkitdSettings.IPTables = cli.Cfg().Global.IPTables

	earthDir, err := cliutil.GetOrCreateEarthDir(cli.Flags().InstallationName)
	if err != nil {
		return fmt.Errorf("failed to get earth dir: %w", err)
	}

	cli.Flags().BuildkitdSettings.StartUpLockPath = filepath.Join(earthDir, "buildkitd-startup.lock")

	return nil
}
