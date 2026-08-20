package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project's `.arg` and `.secret` files are read as `NAME=value` lines.
//
// Five of the corpus's invocations drive `tests/dotenv.earth`, which exists to
// check them, and this engine had neither file (E465).
func TestValuesReadFromAProjectFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, dir, ".arg", "# a comment\n\nTEST_ARG_1=abracadabra\nQUOTED=\"a b\"\n"+
		"SPACED = spaced \nEMPTY=\n")

	got, err := valuesFrom(dir, ".arg", false)
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"TEST_ARG_1": "abracadabra",
		// The quotes are the shell's, not the value's - which is what every
		// reader of these files does and what an author writing one expects.
		"QUOTED": "a b",
		"SPACED": "spaced",
		"EMPTY":  "",
	} {
		if got[name] != want {
			t.Errorf("%s is %q, want %q", name, got[name], want)
		}
	}

	if len(got) != 4 {
		t.Errorf("read %d values, want 4: a comment and a blank line are not values", len(got))
	}
}

// A file the project does not keep is not an error, and one it cannot read is.
//
// Most projects keep neither file. **A file the author wrote and this engine
// could not use is the other case entirely** - building without values somebody
// put in a file is the shape of failure this engine is arranged against - and
// the two are told apart by whether the caller named the path.
func TestAMissingFileIsOnlyAnErrorWhenItWasAskedFor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	got, err := valuesFrom(dir, ".arg", false)
	if err != nil || got != nil {
		t.Errorf("a project with no .arg gave (%v, %v), want (nil, nil)", got, err)
	}

	if _, err := valuesFrom(dir, ".some-other-arg", true); err == nil {
		t.Error("a file the caller named and this engine could not open was" +
			" passed over in silence")
	}
}

// A line that names no value is refused, saying which line.
func TestALineThatNamesNoValueIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, dir, ".arg", "GOOD=1\nnonsense\n")

	_, err := valuesFrom(dir, ".arg", false)
	if err == nil {
		t.Fatal("a line naming no value was read as one")
	}

	if got := err.Error(); !contains(got, ".arg:2") || !contains(got, "nonsense") {
		t.Errorf("refused with %q, which does not name the line or its text", got)
	}
}

// The command line beats the file.
//
// A file is the project's default and an argument is this invocation's
// instruction. The other way round, a value typed on the command line would be
// silently ignored because a file somewhere said otherwise.
func TestTheCommandLineBeatsTheFile(t *testing.T) {
	t.Parallel()

	got := beneath(
		map[string]string{"A": "from-file", "B": "only-in-file"},
		map[string]string{"A": "from-command-line"})

	if got["A"] != "from-command-line" {
		t.Errorf("A is %q, and the command line said otherwise", got["A"])
	}

	if got["B"] != "only-in-file" {
		t.Errorf("B is %q, and only the file mentions it", got["B"])
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}

	return -1
}

// The file's value reaches a declared argument, and the command line beats it.
//
// The readers above are pure; this is the wiring, which is the half that is
// easy to write and never call. A build whose `.arg` is read into a map nobody
// passes on is a feature that exists in the test suite alone (E465).
func TestTheProjectFileReachesTheBuild(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, dir, ".arg", "FROM_FILE=file-value\nBOTH=file-value\n")
	write(t, dir, ".secret", "A_SECRET=shhh\n")

	o := Options{
		Dir:     dir,
		Args:    map[string]string{"BOTH": "command-line"},
		Secrets: map[string]string{},
	}

	args, secrets, err := o.withProjectFiles()
	if err != nil {
		t.Fatal(err)
	}

	if args["FROM_FILE"] != "file-value" {
		t.Errorf("FROM_FILE is %q, and only the file mentions it", args["FROM_FILE"])
	}

	if args["BOTH"] != "command-line" {
		t.Errorf("BOTH is %q; the command line is this invocation's instruction",
			args["BOTH"])
	}

	if secrets["A_SECRET"] != "shhh" {
		t.Errorf("A_SECRET is %q, and .secret holds it", secrets["A_SECRET"])
	}

	// The caller's own maps are not modified: an Options reused for a second
	// build would otherwise carry the first project's values into it.
	if _, leaked := o.Args["FROM_FILE"]; leaked {
		t.Error("the file's values were written into the caller's map")
	}
}

// And through `Run`, which is the half that is easy to write and never call.
//
// The test above calls `withProjectFiles` directly and passed while nothing in
// `Run` called it - **a feature that exists in the test suite alone**, which is
// what that test's own comment warned about and did not prevent. The mutation
// sweep deleted the call and nothing failed (E465).
//
// A dry run, because what is being checked is that the value reached the *plan*:
// no sandbox, no network, and the expansion of the argument into the command is
// visible in the report.
func TestTheProjectFileReachesTheBuildThroughRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, dir, "Earthfile", "VERSION 0.8\n\nmain:\n    FROM alpine:3.22\n"+
		"    ARG GREETING=default\n    RUN echo $GREETING\n")
	write(t, dir, ".arg", "GREETING=from-the-file\n")

	var out strings.Builder

	err := Run(context.Background(), Options{
		Dir: dir, Target: "main", Out: &out, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !contains(out.String(), "from-the-file") {
		t.Errorf("the plan is:\n%s\n  and .arg says GREETING=from-the-file", out.String())
	}
}

// `--secret-file NAME=PATH` is one secret whose value is a file's contents.
//
// Distinct from the project's `.secret`, which holds many, and the two were
// conflated when the second was written: the gate passed
// `--secret-file SECRET3=~/my-secret-file` into the option meaning *where the
// project keeps its secrets*, and the engine looked for a file literally called
// `SECRET3=~/my-secret-file` (E469).
//
// `~` is expanded, because that is how the corpus writes it and how anybody
// writes a path to something in their home directory.
func TestASecretWhoseValueIsAFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, dir, "the-secret", "shhh")

	got, err := secretsFromFiles([]string{"NAME=" + filepath.Join(dir, "the-secret")}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if got["NAME"] != "shhh" {
		t.Errorf("NAME is %q, and the file holds shhh", got["NAME"])
	}

	// And with a tilde, which is how the corpus writes it - and how anybody
	// names something in their own home. `dir` stands in for that home.
	viaHome, err := secretsFromFiles([]string{"NAME=~/the-secret"}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if viaHome["NAME"] != "shhh" {
		t.Errorf("NAME is %q via ~/the-secret, and the file holds shhh",
			viaHome["NAME"])
	}
}

// A file that is not there is refused, naming the secret and the path.
//
// The alternative is a step receiving an empty credential and failing somewhere
// else entirely, with a message about authentication.
func TestASecretFileThatIsNotThere(t *testing.T) {
	t.Parallel()

	_, err := secretsFromFiles([]string{"NAME=/no/such/file"}, t.TempDir())
	if err == nil {
		t.Fatal("a secret file that does not exist was passed over")
	}

	for _, want := range []string{"NAME", "/no/such/file"} {
		if !contains(err.Error(), want) {
			t.Errorf("refused with %q, which does not mention %q", err, want)
		}
	}
}

// A spelling that names no path is refused rather than guessed at.
func TestASecretFileMustNameAPath(t *testing.T) {
	t.Parallel()

	_, err := secretsFromFiles([]string{"JUST_A_NAME"}, t.TempDir())
	if err == nil {
		t.Fatal("`--secret-file JUST_A_NAME` was accepted, and it names no file")
	}
}
