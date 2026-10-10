package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// awsPaths is where this machine keeps its AWS credentials, and which profile
// of them to read.
//
// A struct rather than four arguments, and taken by the reader rather than read
// from the environment inside it, because a test that has to set `HOME` to
// exercise this is a test that cannot run beside another one.
type awsPaths struct {
	home        string
	credentials string // AWS_SHARED_CREDENTIALS_FILE
	config      string // AWS_CONFIG_FILE
	profile     string // AWS_PROFILE, "default" when empty
}

// awsPathsFromEnv reads the three variables that move these files, so a machine
// that has moved them is still read correctly.
func awsPathsFromEnv(environ []string) awsPaths {
	var p awsPaths

	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}

		switch name {
		case "HOME":
			p.home = value
		case "AWS_SHARED_CREDENTIALS_FILE":
			p.credentials = value
		case "AWS_CONFIG_FILE":
			p.config = value
		case "AWS_PROFILE":
			p.profile = value
		}
	}

	return p
}

func (p awsPaths) credentialsFile() string {
	if p.credentials != "" {
		return p.credentials
	}

	return filepath.Join(p.home, ".aws", "credentials")
}

func (p awsPaths) configFile() string {
	if p.config != "" {
		return p.config
	}

	return filepath.Join(p.home, ".aws", "config")
}

func (p awsPaths) profileName() string {
	if p.profile != "" {
		return p.profile
	}

	return "default"
}

// awsCredentials is everything a `RUN --aws` step should be given.
//
// **The environment wins.** That is the order the AWS tools themselves resolve
// in, and a build that exported a key deliberately must not be handed a stale
// one from disk instead.
func awsCredentials(environ []string, paths awsPaths) map[string]string {
	out := awsFromFiles(paths)

	for name, value := range awsFromEnv(environ) {
		if out == nil {
			out = map[string]string{}
		}

		out[name] = value
	}

	return out
}

// awsFromFiles reads the shared credentials and config files.
//
// `RUN --aws` shipped reading the environment only, which is one of the two ways
// credentials reach a machine and not the commoner one: `aws configure` writes
// files, and the corpus has a driver for each. The file case was failing.
//
// **Never an error.** No `~/.aws` is the ordinary case, and an unreadable or
// malformed file leaves the build as it was rather than stopping it - the step
// that needed a credential will say so, which is a better diagnostic than this
// could give.
func awsFromFiles(paths awsPaths) map[string]string {
	var out map[string]string

	put := func(name, value string) {
		if value == "" {
			return
		}

		if out == nil {
			out = map[string]string{}
		}

		out[name] = value
	}

	profile := paths.profileName()

	creds := readINISection(paths.credentialsFile(), profile)
	put("AWS_ACCESS_KEY_ID", creds["aws_access_key_id"])
	put("AWS_SECRET_ACCESS_KEY", creds["aws_secret_access_key"])
	put("AWS_SESSION_TOKEN", creds["aws_session_token"])

	// **The config file spells a profile differently.** `[default]` there, but
	// `[profile work]` for every other one - the credentials file writes plain
	// `[work]`. One rule for both silently loses the region.
	section := profile
	if section != "default" {
		section = "profile " + section
	}

	cfg := readINISection(paths.configFile(), section)
	put("AWS_REGION", cfg["region"])

	return out
}

// readINISection returns one section's keys, lower-cased, or nothing.
//
// Deliberately small: this reads two files written by `aws configure`, not the
// whole of the INI dialect. Nested profiles, `source_profile` chains and SSO
// sessions are not resolved - a build needing those is one this cannot serve,
// and pretending otherwise would hand the step a half-resolved credential.
func readINISection(path, want string) map[string]string {
	// The path is this machine's own AWS configuration, named by the caller's
	// environment - reading it is the whole purpose of the function.
	f, err := os.Open(path) //nolint:gosec // caller's own AWS config path
	if err != nil {
		return nil
	}

	defer func() { _ = f.Close() }()

	out := map[string]string{}
	in := false

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			in = strings.TrimSpace(line[1:len(line)-1]) == want

			continue
		}

		if !in {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		out[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}

	return out
}
