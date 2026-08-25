package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/layer"
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
			// Carries its contents and is not a credential: the hosts file and
			// the resolver are built this way, and a scan that took every
			// contents-carrying mount for a secret would fail builds over
			// `127.0.0.1 localhost`.
			{Target: "/etc/hosts", Secret: "127.0.0.1 localhost"},
			{ //nolint:gosec // fixtures, not credentials
				Target: "/run/secrets/npmrc", Credential: true,
				Secret: "//registry:_authToken=abc",
			},
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
	for _, name := range []string{"/cache", "PATH", "HOME", "/etc/hosts"} {
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

// TestAStepThatWroteItsSecretIsRecorded.
//
// **A finding, not a refusal, and that is the design.** A layer holding a
// credential has gone nowhere while it sits in this build's store; it becomes a
// leak when the image is saved or pushed. So the guest records against the
// handle and the host refuses at the exit point - which is also the only place
// that knows whether there is one.
//
// The record must name the secret and where, and never the value: it travels to
// the build's output, which is the log the credential was being kept out of.
func TestAStepThatWroteItsSecretIsRecorded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "app.env"),
		[]byte("api=hunter2-swordfish-battery\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{}
	req := Request{
		Handle:    "h1",
		Env:       []string{"TOKEN=hunter2-swordfish-battery"},
		SecretEnv: []string{"TOKEN"},
	}

	err = s.noteSecretLeak(req, deltaOnly{dir})
	if err != nil {
		t.Fatalf("recording a finding failed: %v", err)
	}

	got := s.leakedBy("h1")
	if len(got) != 1 {
		t.Fatalf("recorded %v, want one finding", got)
	}

	if !strings.Contains(got[0], "TOKEN") || !strings.Contains(got[0], "app.env") {
		t.Errorf("the finding does not say which secret or where: %q", got[0])
	}

	if strings.Contains(got[0], "hunter2") {
		t.Errorf("the finding quotes the credential: %q", got[0])
	}

	// A step given the same secret that did not write it leaves no record, so a
	// clean build never reaches the refusal at all.
	clean := &Server{}
	err = clean.noteSecretLeak(Request{
		Handle: "h2", Env: []string{"TOKEN=something-else-entirely"},
		SecretEnv: []string{"TOKEN"},
	}, deltaOnly{dir})
	if err != nil {
		t.Fatal(err)
	}

	if got := clean.leakedBy("h2"); len(got) != 0 {
		t.Errorf("a clean step was recorded as leaking: %v", got)
	}
}

// TestASecretIsRedactedFromWhatAStepPrinted.
//
// **A build log is the most public thing a build produces.** A step that echoes
// a credential - a `set -x` trace, a curl command line, a config dump on
// failure - puts it in the output, which goes to a terminal, a CI job page and
// from there into an issue somebody pastes it into.
//
// Scrubbed rather than refused, and the difference matters: a secret in a layer
// is an artifact that outlives the build and must stop it, while a secret in the
// output is already loose and the useful thing is not to repeat it. Failing here
// would also destroy the diagnostic the author needs.
func TestASecretIsRedactedFromWhatAStepPrinted(t *testing.T) {
	t.Parallel()

	req := Request{
		Strict:    true,
		Env:       []string{"TOKEN=hunter2-swordfish"},
		SecretEnv: []string{"TOKEN"},
		Mounts: []Mount{{
			Target: "/run/secrets/np", Credential: true,
			Secret: "authToken=abc123xyz",
		}},
	}

	out := []byte("+ curl -H 'Authorization: hunter2-swordfish' https://api\n" +
		"wrote authToken=abc123xyz to /root/.npmrc\nall done\n")

	got, names := redactSecrets(out, secretsFrom(req))

	for _, leaked := range []string{"hunter2-swordfish", "abc123xyz"} {
		if strings.Contains(string(got), leaked) {
			t.Errorf("the output still contains a credential")
		}
	}

	// What survives is the diagnostic, which is the whole reason not to refuse.
	for _, want := range []string{"curl", "https://api", "/root/.npmrc", "all done"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("redaction removed %q, which the author needs", want)
		}
	}

	// And the reader is told, by name, that something was taken out - silently
	// altered output is a debugging session that goes nowhere.
	if len(names) != 2 {
		t.Errorf("reported %v redacted, want both secrets named", names)
	}

	for _, want := range []string{"TOKEN", "/run/secrets/np"} {
		if !strings.Contains(strings.Join(names, " "), want) {
			t.Errorf("%s was redacted without being named: %v", want, names)
		}
	}

	// Nothing to redact leaves the bytes exactly as they were, so an ordinary
	// build is not paying for a copy.
	clean := []byte("ordinary output\n")

	same, none := redactSecrets(clean, secretsFrom(req))
	if len(none) != 0 || string(same) != string(clean) {
		t.Errorf("clean output was altered: %q %v", same, none)
	}
}

// TestAStreamedSecretIsRedactedAcrossChunks.
//
// Output arrives in whatever pieces the step wrote it in, so a credential can
// straddle two. A scrubber that looks at each chunk alone lets it through -
// which is the file scanner's boundary problem at a different granularity, and
// worse here because the result is printed rather than stored.
func TestAStreamedSecretIsRedactedAcrossChunks(t *testing.T) {
	t.Parallel()

	secrets := []layer.Secret{{Name: "TOKEN", Value: "hunter2-swordfish"}}

	var got []byte

	sink, flush := redactingSink(func(b []byte) { got = append(got, b...) }, secrets)

	// Split mid-credential, which is the case that matters.
	for _, chunk := range []string{"start hunter2", "-swordfish end\n"} {
		sink([]byte(chunk))
	}

	flush()

	if strings.Contains(string(got), "hunter2-swordfish") {
		t.Errorf("a credential split across two chunks was printed: %q", got)
	}

	for _, want := range []string{"start", "end"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("redaction lost %q from the output: %q", want, got)
		}
	}

	// Nothing is left behind: what the flush holds must always come out.
	if !strings.Contains(string(got), "[redacted:TOKEN]") {
		t.Errorf("the redaction marker never reached the reader: %q", got)
	}
}
