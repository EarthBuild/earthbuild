package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// `RUN --aws` shipped reading environment variables only, and the corpus has a
// driver for each of the two ways credentials arrive. The file case failed:
// `test-aws-flag-configs` writes ~/.aws/credentials and nothing reached the step.
func TestCredentialsAreReadFromTheSharedFile(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeAWS(t, filepath.Join(home, ".aws", "credentials"), `[default]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = fake-secret
aws_session_token = tok123
`)
	writeAWS(t, filepath.Join(home, ".aws", "config"), `[default]
region = us-west-1
`)

	got := awsFromFiles(awsPaths{home: home})

	for name, want := range map[string]string{
		"AWS_ACCESS_KEY_ID":     "AKIAEXAMPLE",
		"AWS_SECRET_ACCESS_KEY": "fake-secret",
		"AWS_SESSION_TOKEN":     "tok123",
		"AWS_REGION":            "us-west-1",
	} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}

// The AWS chain puts the environment ahead of the file, and a build that
// exported a key must not be handed a stale one from disk.
func TestTheEnvironmentWinsOverTheFile(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeAWS(t, filepath.Join(home, ".aws", "credentials"), `[default]
aws_access_key_id = FROM_FILE
`)

	got := awsCredentials([]string{"AWS_ACCESS_KEY_ID=FROM_ENV"}, awsPaths{home: home})
	if got["AWS_ACCESS_KEY_ID"] != "FROM_ENV" {
		t.Errorf("AWS_ACCESS_KEY_ID = %q, want the environment's", got["AWS_ACCESS_KEY_ID"])
	}
}

// A named profile is a different set of credentials, and reading `[default]`
// for it would hand the build somebody else's account.
func TestANamedProfileIsRead(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeAWS(t, filepath.Join(home, ".aws", "credentials"), `[default]
aws_access_key_id = DEFAULT_KEY

[work]
aws_access_key_id = WORK_KEY
`)

	got := awsFromFiles(awsPaths{home: home, profile: "work"})
	if got["AWS_ACCESS_KEY_ID"] != "WORK_KEY" {
		t.Errorf("AWS_ACCESS_KEY_ID = %q, want WORK_KEY", got["AWS_ACCESS_KEY_ID"])
	}
}

// A machine with no ~/.aws is the ordinary case and must stay silent: no
// credentials, no error, and a build that never wanted them unaffected.
func TestNoAWSDirectoryIsNotAnError(t *testing.T) {
	t.Parallel()

	if got := awsFromFiles(awsPaths{home: t.TempDir()}); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

// A config file names its non-default profiles `[profile work]`, which is not
// how the credentials file spells the same thing. Reading one rule for both
// silently loses the region.
func TestTheConfigFileProfilePrefixIsUnderstood(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeAWS(t, filepath.Join(home, ".aws", "config"), `[default]
region = eu-west-2

[profile work]
region = ap-south-1
`)

	if got := awsFromFiles(awsPaths{home: home, profile: "work"}); got["AWS_REGION"] != "ap-south-1" {
		t.Errorf("AWS_REGION = %q, want ap-south-1", got["AWS_REGION"])
	}
}

func writeAWS(t *testing.T, path, body string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}
