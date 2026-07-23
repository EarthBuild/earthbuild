package subcmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/EarthBuild/earthbuild/buildkitd"
	"github.com/EarthBuild/earthbuild/cmd/earthly/common"
	"github.com/EarthBuild/earthbuild/cmd/earthly/flag"
	"github.com/EarthBuild/earthbuild/util/cliutil"
	"github.com/EarthBuild/earthbuild/util/fileutil"
	"github.com/EarthBuild/earthbuild/util/termutil"
	"github.com/adrg/xdg"
	"github.com/urfave/cli/v3"
)

// Bootstrap encapsulates the bootstrap command logic.
type Bootstrap struct {
	cli              CLI
	homebrewSource   string
	certsHostName    string
	noBuildkit       bool
	genCerts         bool
	withAutocomplete bool
}

// NewBootstrap creates a new Bootstrap command.
func NewBootstrap(cli CLI) *Bootstrap {
	return &Bootstrap{
		cli: cli,
	}
}

// Cmds returns the list of commands for the bootstrap command.
func (b *Bootstrap) Cmds() []*cli.Command {
	return []*cli.Command{
		{
			Name: "bootstrap",
			Usage: "Bootstraps earth installation including buildkit image download and " +
				"optionally shell autocompletion",
			UsageText: "earth [options] bootstrap [--no-buildkit, --with-autocomplete, --certs-hostname]",
			Description: "Bootstraps earthbuild installation including buildkit image download and " +
				"optionally shell autocompletion.",
			Action: b.Action,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:        "source",
					Usage:       "Output source file (for use in homebrew install)",
					Hidden:      true, // only meant for use with homebrew formula
					Destination: &b.homebrewSource,
				},
				&cli.BoolFlag{
					Name:        "no-buildkit",
					Usage:       "Skips setting up the BuildKit container",
					Destination: &b.noBuildkit,
				},
				&cli.BoolFlag{
					Name:        "with-autocomplete",
					Usage:       "Install shell autocompletions during bootstrap",
					Destination: &b.withAutocomplete,
				},
				&cli.BoolFlag{
					Name: "force-certificate-generation",
					Usage: "Force the generation of self-signed TLS certificates, " +
						"even when no BuildKit container is started",
					Destination: &b.genCerts,
				},
				&cli.StringFlag{
					Name:        "certs-hostname",
					Usage:       "Hostname to generate certificates for",
					Sources:     flag.EarthEnvVars("CERTS_HOSTNAME"),
					Value:       "localhost",
					Destination: &b.certsHostName,
				},
			},
		},
	}
}

// Action handles the bootstrap command.
func (b *Bootstrap) Action(ctx context.Context, cmd *cli.Command) error {
	b.cli.SetCommandName("actionbootstrap")

	switch b.homebrewSource {
	case "bash":
		compEntry, err := bashCompleteEntry()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to enable bash-completion: %s\n", err)
			return nil // zsh-completion isn't available, silently fail.
		}

		fmt.Print(compEntry)

		return nil
	case "zsh":
		compEntry, err := zshCompleteEntry()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to bootstrap zsh-completion: %s\n", err)
			return nil // zsh-completion isn't available, silently fail.
		}

		fmt.Print(compEntry)

		return nil
	case "":
		break
	default:
		return fmt.Errorf("unhandled source %q", b.homebrewSource)
	}

	return b.bootstrap(ctx, cmd)
}

func (b *Bootstrap) bootstrap(ctx context.Context, cmd *cli.Command) error {
	console := b.cli.Console().WithPrefix("bootstrap")

	defer func() {
		// cliutil.IsBootstrapped() determines if bootstrapping was done based
		// on the existence of ~/.earthly; therefore we must ensure it's created.
		_, dirErr := cliutil.GetOrCreateEarthDir(b.cli.Flags().InstallationName)
		if dirErr != nil {
			console.Warnf("Warning: Failed to create earthbuild Dir: %v", dirErr)
			// Keep going.
		}

		dirErr = cliutil.EnsurePermissions(b.cli.Flags().InstallationName)
		if dirErr != nil {
			console.Warnf("Warning: Failed to ensure permissions: %v", dirErr)
			// Keep going.
		}
	}()

	if b.withAutocomplete {
		// Because this requires sudo, it should warn and not fail the rest of it.
		err := b.insertBashCompleteEntry()
		if err != nil {
			console.Warnf("Warning: %s\n", err.Error())
			// Keep going.
		}

		err = b.insertZSHCompleteEntry()
		if err != nil {
			console.Warnf("Warning: %s\n", err.Error())
			// Keep going.
		}

		console.Printf("You may have to restart your shell for autocomplete to get initialized (e.g. run \"exec $SHELL\")\n")
	}

	err := symlinkEarthlyToEarth()
	if err != nil {
		console.Warnf("Warning: %s\n", err.Error())
	}

	if !b.noBuildkit || b.genCerts {
		bkURL, err := url.Parse(b.cli.Flags().BuildkitHost)
		if err != nil {
			return fmt.Errorf("invalid buildkit_host: %s: %w", b.cli.Flags().BuildkitHost, err)
		}

		if bkURL.Scheme == "tcp" && b.cli.Cfg().Global.TLSEnabled {
			err := buildkitd.GenCerts(*b.cli.Cfg(), b.certsHostName)
			if err != nil {
				return fmt.Errorf("failed to generate TLS certs: %w", err)
			}
		}
	}

	if !b.noBuildkit {
		// connect to local buildkit instance (to trigger pulling and running the earthbuild/buildkitd image)
		bkClient, err := b.cli.GetBuildkitClient(ctx, cmd)
		if err != nil {
			console.Warnf("Warning: Bootstrapping buildkit failed: %v", err)
			// Keep going.
		} else {
			defer bkClient.Close()
		}
	}

	console.Printf("Bootstrapping successful.\n")

	return nil
}

func (b *Bootstrap) insertBashCompleteEntry() error {
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("could not get current user: %w", err)
	}

	isRootUser := u.Uid == "0"

	var path string
	// Assume that non-root can't write to the system and that installation
	// to root's home isn't desirable.  One possible exception might be if
	// those directories are on an R/O filesystem, but user can install these
	// manually in that case.
	if isRootUser {
		if runtime.GOOS == "darwin" {
			path = "/usr/local/etc/bash_completion.d/earthly"
		} else {
			path = "/usr/share/bash-completion/completions/earthly"
		}
	} else {
		// https://github.com/scop/bash-completion/blob/master/README.md#faq
		userPath, ok := os.LookupEnv("BASH_COMPLETION_USER_DIR")
		if !ok {
			// This will give a standardized fallback even if XDG isn't active
			userPath = xdg.DataHome
		}

		path = filepath.Join(userPath, "bash-completion/completions/earthly")
	}

	ok, err := b.insertBashCompleteEntryAt(path)
	if err != nil {
		return err
	}

	if ok {
		b.cli.Console().VerbosePrintf("Successfully enabled bash-completion at %s\n", path)
	} else {
		b.cli.Console().VerbosePrintf("Bash-completion already present at %s\n", path)
	}

	return nil
}

func (b *Bootstrap) insertBashCompleteEntryAt(path string) (bool, error) {
	dirPath := filepath.Dir(path)

	dirPathExists, err := fileutil.DirExists(dirPath)
	if err != nil {
		return false, fmt.Errorf("failed checking if %s exists: %w", dirPath, err)
	}

	if !dirPathExists {
		return false, fmt.Errorf("%s does not exist", dirPath)
	}

	pathExists, err := fileutil.FileExists(path)
	if err != nil {
		return false, fmt.Errorf("failed checking if %s exists: %w", path, err)
	}

	if pathExists {
		return false, nil // file already exists, don't update it.
	}

	// create the completion file
	f, err := os.Create(path) // #nosec G304
	if err != nil {
		return false, err
	}
	defer f.Close()

	bashEntry, err := bashCompleteEntry()
	if err != nil {
		return false, fmt.Errorf("failed to add entry: %w", err)
	}

	_, err = f.WriteString(bashEntry)
	if err != nil {
		return false, fmt.Errorf("failed writing to %s: %w", path, err)
	}

	return true, nil
}

// If debugging this, it might be required to run `rm ~/.zcompdump*` to remove the cache.
func (b *Bootstrap) insertZSHCompleteEntry() error {
	potentialPaths := []string{
		"/usr/local/share/zsh/site-functions",
		"/usr/share/zsh/site-functions",
	}
	for _, dirPath := range potentialPaths {
		dirPathExists, err := fileutil.DirExists(dirPath)
		if err != nil {
			return fmt.Errorf("failed to check if %s exists: %w", dirPath, err)
		}

		if dirPathExists {
			return b.insertZSHCompleteEntryUnderPath(dirPath)
		}
	}

	fmt.Fprint(os.Stderr,
		"Warning: unable to enable zsh-completion: none of "+strings.Join(potentialPaths, ", ")+" does not exist\n")

	return nil // zsh-completion isn't available, silently fail.
}

func (b *Bootstrap) insertZSHCompleteEntryUnderPath(dirPath string) error {
	path := filepath.Join(dirPath, "_earthly")

	pathExists, err := fileutil.FileExists(path)
	if err != nil {
		return fmt.Errorf("failed to check if %s exists: %w", path, err)
	}

	if pathExists {
		return nil // file already exists, don't update it.
	}

	// create the completion file
	f, err := os.Create(path) // #nosec G304
	if err != nil {
		return err
	}
	defer f.Close()

	compEntry, err := zshCompleteEntry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: unable to enable zsh-completion: %s\n", err)
		return nil // zsh-completion isn't available, silently fail.
	}

	_, err = f.WriteString(compEntry)
	if err != nil {
		return fmt.Errorf("failed writing to %s: %w", path, err)
	}

	return b.deleteZcompdump()
}

func (b *Bootstrap) deleteZcompdump() error {
	var homeDir string

	sudoUser, found := os.LookupEnv("SUDO_USER")
	if !found {
		var err error

		homeDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to lookup current user home dir: %w", err)
		}
	} else {
		currentUser, err := user.Lookup(sudoUser)
		if err != nil {
			return fmt.Errorf("failed to lookup user %s: %w", sudoUser, err)
		}

		homeDir = currentUser.HomeDir
	}

	files, err := os.ReadDir(homeDir)
	if err != nil {
		return fmt.Errorf("failed to read dir %s: %w", homeDir, err)
	}

	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".zcompdump") {
			path := filepath.Join(homeDir, f.Name())

			err := os.Remove(path)
			if err != nil {
				return fmt.Errorf("failed to remove %s: %w", path, err)
			}
		}
	}

	return nil
}

func symlinkEarthlyToEarth() error {
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}

	baseName := path.Base(binPath)
	if baseName != "earthly" {
		return nil
	}

	earthPath := path.Join(path.Dir(binPath), "earth")

	earthPathExists, err := fileutil.FileExists(earthPath)
	if err != nil {
		return fmt.Errorf("failed to check if %q exists: %w", earthPath, err)
	}

	if !earthPathExists && termutil.IsTTY() {
		return nil // legacy earth binary doesn't exist, don't create it (unless we're under a non-tty system e.g. CI)
	}

	if !common.IsEarthlyBinary(earthPath) {
		return nil // file exists but is not an earth binary, leave it alone.
	}

	// otherwise legacy earth command has been detected, remove it and symlink
	// to the new earth command.
	err = os.Remove(earthPath)
	if err != nil {
		return fmt.Errorf("failed to remove old install at %s: %w", earthPath, err)
	}

	err = os.Symlink(binPath, earthPath)
	if err != nil {
		return fmt.Errorf("failed to symlink %s to %s: %w", binPath, earthPath, err)
	}

	return nil
}

func bashCompleteEntry() (string, error) {
	template := "complete -o nospace -C '__earthly__' earthly\n"
	return renderEntryTemplate(template)
}

func zshCompleteEntry() (string, error) {
	template := `#compdef _earthly earthly

function _earthly {
    autoload -Uz bashcompinit
    bashcompinit
    complete -o nospace -C '__earthly__' earthly
}
`

	return renderEntryTemplate(template)
}

func renderEntryTemplate(template string) (string, error) {
	earthPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to determine earth path: %w", err)
	}

	return strings.ReplaceAll(template, "__earthly__", earthPath), nil
}
