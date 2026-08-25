package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// TestTheSecretsAStepWasGivenAreGatheredByName.
//
// **Two kinds, and the wire tells them apart differently.** A secret mount
// carries its value in the request and is named by where it appears; a secret
// environment variable is one entry of `Env` among many, indistinguishable from
// an ordinary one unless the host says which names are secret - so it does.
//
// A strict mode that checked only mounts would be worse than none: it would
// report a clean build to somebody who had put a token in `$TOKEN` and echoed
// it into a file.
func TestTheSecretsAStepWasGivenAreGatheredByName(t *testing.T) {
	t.Parallel()

	req := Request{
		Env: []string{
			"PATH=/usr/bin",
			"TOKEN=hunter2-swordfish",
			"HOME=/root",
			"DEPLOY_KEY=another-credential",
		},
		SecretEnv: []string{"TOKEN", "DEPLOY_KEY", "NEVER_SET"},
		Mounts: []Mount{
			{Target: "/etc/hosts", Secret: "127.0.0.1 localhost"},
			{Target: "/run/secrets/npmrc", Secret: "//registry:_authToken=abc"}, //nolint:gosec // a fixture, not a credential
			{Target: "/cache", ID: "go-mod"},
		},
	}

	got := secretsFrom(req)

	byName := map[string]string{}
	for _, s := range got {
		byName[s.Name] = s.Value
	}

	for name, want := range map[string]string{ //nolint:gosec // fixtures, not credentials
		"TOKEN":              "hunter2-swordfish",
		"DEPLOY_KEY":         "another-credential",
		"/run/secrets/npmrc": "//registry:_authToken=abc",
		"/etc/hosts":         "127.0.0.1 localhost",
	} {
		if byName[name] != want {
			t.Errorf("secret %s came out as %q", name, byName[name])
		}
	}

	// A name the invocation never supplied has no value and must not become an
	// empty-string secret, which would match every file in the layer.
	if _, ok := byName["NEVER_SET"]; ok {
		t.Error("a secret with no value was gathered; an empty value matches" +
			"\n  everything, so every build would be reported as leaking")
	}

	// An ordinary mount is not a secret and an ordinary variable is not one
	// either, however much it looks like one.
	for _, name := range []string{"/cache", "PATH", "HOME"} {
		if _, ok := byName[name]; ok {
			t.Errorf("%s was treated as a secret", name)
		}
	}
}

// deltaOnly is a handle that is nothing but a delta, which is all the check
// looks at.
type deltaOnly struct{ dir string }

func (d deltaOnly) Root() string                     { return d.dir }
func (d deltaOnly) Delta() string                    { return d.dir }
func (d deltaOnly) Observations() core.Observation   { return core.Observation{} }
func (d deltaOnly) Release() error                   { return nil }
func (d deltaOnly) SharedFile(string) (string, bool) { return "", false }

// TestAStepThatWroteItsSecretIsRefused.
//
// The whole point, end to end at the level the guest decides it: a step given a
// credential, a delta holding that credential, and a build that stops.
//
// **The refusal must not repeat the secret.** It is written to the build's
// output, which is the log the credential was being kept out of.
func TestAStepThatWroteItsSecretIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "app.env"),
		[]byte("api=hunter2-swordfish-battery\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	req := Request{
		Handle:    "h1",
		Env:       []string{"TOKEN=hunter2-swordfish-battery"},
		SecretEnv: []string{"TOKEN"},
	}

	// Off, nothing is read and nothing is refused - which is what every build
	// that did not ask for this gets.
	err = strictSecretCheck(req, deltaOnly{dir})
	if err != nil {
		t.Fatalf("a build that did not ask to be strict was refused: %v", err)
	}

	req.Strict = true

	err = strictSecretCheck(req, deltaOnly{dir})
	if err == nil {
		t.Fatal("a step wrote its secret into the layer and the build continued")
	}

	if !strings.Contains(err.Error(), "TOKEN") ||
		!strings.Contains(err.Error(), "app.env") {
		t.Errorf("the refusal does not say which secret or where:\n  %v", err)
	}

	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the refusal quotes the credential, publishing it to every log"+
			"\n  this build writes to:\n  %v", err)
	}

	// A step given the same secret that did not write it carries on.
	clean := Request{
		Handle: "h2", Strict: true,
		Env:       []string{"TOKEN=something-else-entirely"},
		SecretEnv: []string{"TOKEN"},
	}

	err = strictSecretCheck(clean, deltaOnly{dir})
	if err != nil {
		t.Errorf("a clean step was refused: %v", err)
	}
}
