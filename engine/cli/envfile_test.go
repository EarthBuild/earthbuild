package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestTheEnvFileSuppliesSettings.
//
// `.env` stopped supplying build arguments in v0.7.0 and never stopped
// supplying *settings*: the corpus writes `EARTHLY_PUSH=1` into one and expects
// the build to push.
func TestTheEnvFileSuppliesSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, ".env", "EARTHLY_PUSH=1\n")

	got, err := EnvFileValues(dir, "", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}

	if got["EARTHLY_PUSH"] != "1" {
		t.Errorf("read %v, want EARTHLY_PUSH=1", got)
	}

	// A project with no `.env` is most projects, and is not an error.
	got, err = EnvFileValues(t.TempDir(), "", func(string) string { return "" })
	if err != nil || len(got) != 0 {
		t.Errorf("an absent .env gave %v, %v", got, err)
	}
}

// A file named outright must be there: the caller said where it is.
func TestAnEnvFileNamedOutrightMustExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, ".other", "EARTHLY_PUSH=1\n")

	got, err := EnvFileValues(dir, ".other", func(string) string { return "" })
	if err != nil || got["EARTHLY_PUSH"] != "1" {
		t.Fatalf("--env-file-path .other gave %v, %v", got, err)
	}

	_, err = EnvFileValues(dir, ".missing", func(string) string { return "" })
	if err == nil {
		t.Error("a named file that is not there was passed over in silence")
	}

	// And the environment names one too, the flag still winning.
	got, err = EnvFileValues(dir, "", func(n string) string {
		if n == "EARTHLY_ENV_FILE_PATH" {
			return ".other"
		}

		return ""
	})
	if err != nil || got["EARTHLY_PUSH"] != "1" {
		t.Errorf("EARTHLY_ENV_FILE_PATH gave %v, %v", got, err)
	}
}

// TestTheDotEnvWarningSkipsWhatTheCliUses.
//
// The warning says a name must move to `.arg` to reach a `--build-arg`. That is
// true of `TEST_IN_DOTENV` and false of `EARTHLY_PUSH`, which this engine reads
// from exactly where it is - so saying it of both makes the true half harder to
// believe.
func TestTheDotEnvWarningSkipsWhatTheCliUses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, ".env", "TEST_IN_DOTENV=x\nEARTHLY_PUSH=1\nEARTH_NO_OUTPUT=1\n")

	var out bytes.Buffer

	Options{Dir: dir, Out: &out}.reportDotEnv(".arg")

	if !strings.Contains(out.String(), "TEST_IN_DOTENV") {
		t.Errorf("the warning is %q, and does not mention the name that really is ignored", &out)
	}

	for _, used := range []string{"EARTHLY_PUSH", "EARTH_NO_OUTPUT"} {
		if strings.Contains(out.String(), used) {
			t.Errorf("the warning claims %s is ignored, and it is not:\n%s", used, &out)
		}
	}
}
