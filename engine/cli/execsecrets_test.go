package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTheStepGetsTheSameSecretsThePlanWasChecked Against.
//
// **Two maps for one question.** The interpreter is given the *merged* secrets
// - the `--secret` flags, the `--secret-file` entries, and the project's
// `.secret` file - and the executor was given `Options.Secrets`, which is only
// the flags. So a build supplying `--secret-file MY=sec.txt` passed planning
// and then failed inside the step with "needs the secret MY", naming a secret
// the caller had plainly supplied.
//
// The plan check exists to fail *early* and name what to pass. Failing late,
// on a secret that was passed, is the worst of both: the diagnostic is right
// about the name and wrong about the fact.
func TestTheStepGetsTheSameSecretsThePlanWasCheckedAgainst(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "sec.txt"), []byte("hello"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	o := Options{
		Dir:         dir,
		Secrets:     map[string]string{"FLAG": "one"},
		SecretFiles: []string{"FROMFILE=" + filepath.Join(dir, "sec.txt")},
	}

	_, secrets, err := o.withProjectFiles()
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{"FLAG": "one", "FROMFILE": "hello"} {
		if secrets[name] != want {
			t.Errorf("the merged secrets have %s=%q, want %q", name, secrets[name], want)
		}
	}

	// The executor must be handed *these*, not the flags alone - which is what
	// `runSecrets` exists to say in one place.
	got := o.runSecrets(secrets)
	if got["FROMFILE"] != "hello" {
		t.Error("the executor was given the flags alone, so a step asks for a" +
			" secret the caller supplied by file and is told it was not")
	}
}
