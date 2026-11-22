package cli_test

import "testing"

// A failure this machine cannot avoid is counted apart from a failure of the
// engine.
//
// The distinction is the whole value of the corpus number: a count that mixes
// "the engine got this wrong" with "this Mac has a case-insensitive disk" stops
// moving for reasons nobody can act on, and a number that cannot move is not
// watched.
//
// The case-sensitivity arm is here because `examples/next-js` proved it. It
// fails on a stock Mac inside a TypeScript compiler that probes the filesystem's
// case behaviour, gets ESTALE where it expected ENOENT, and panics - and the
// *same target on the same commit builds end to end* when the store is on a
// case-sensitive volume (E25). That makes it a property of the disk, and the
// engine says so in its own output, which is what this reads.
func TestACaseInsensitiveStoreIsNotAnEngineFailure(t *testing.T) {
	t.Parallel()

	const note = "note: /x/store is on a case-insensitive filesystem\n"

	const panicked = `panic: vfs: failed to stat "/APP/NODE_MODULES/@TYPESCRIPT/TYPESCRIPT-LINUX-ARM64/LIB/TSC": ` +
		`stat /APP/NODE_MODULES/@TYPESCRIPT/TYPESCRIPT-LINUX-ARM64/LIB/TSC: stale file handle`

	for _, tc := range []struct {
		name   string
		err    string
		output string
		want   bool
	}{
		{
			name:   "an image with no manifest for this architecture",
			err:    "Earthfile:4 is for linux/amd64 and this sandbox runs linux/arm64, so it cannot be executed here",
			output: "",
			want:   true,
		},
		{
			name:   "a stale handle on a case-insensitive store",
			err:    "RUN npm run build failed with exit code 1 (Earthfile:18)",
			output: note + panicked,
			want:   true,
		},
		{
			// The same symptom with a case-sensitive store is ours, and must
			// keep counting against us. Without this arm the check would
			// launder every ESTALE, including one the engine caused.
			name:   "the same stale handle with nothing to blame it on",
			err:    "RUN npm run build failed with exit code 1 (Earthfile:18)",
			output: panicked,
			want:   false,
		},
		{
			// The commonest failure in the whole corpus, and the engine's own
			// words: it names the two paths, the layer, and the filesystem as
			// the reason. 17 of 26 failures in the first full sweep were this,
			// every one of them a property of the disk.
			name: "two paths in an image that differ only in case",
			err: `FROM python:3 (Earthfile:2): layer 0 of python:3: ` +
				`"usr/share/man/man7/PAM.7.gz" and "usr/share/man/man7/pam.7.gz" ` +
				`differ only in case, and this filesystem cannot hold both`,
			output: note,
			want:   true,
		},
		{
			// A registry that answered 502 is not this engine failing, and it
			// is not this machine either - it is a bad minute at Docker Hub.
			// Counting it against the engine makes the corpus number twitch for
			// reasons nobody can act on, which is the same fault as counting a
			// case-insensitive disk.
			name: "a registry having a bad minute",
			err: "FROM namely/protoc-all:1.29_4: layer 11: " +
				"https://registry-1.docker.io/v2/namely/protoc-all/blobs/sha256:8f9c " +
				"returned 502 Bad Gateway",
			output: "",
			want:   true,
		},
		{
			// But a 502 from something that is not a registry request is not
			// this rule's business - the words have to come from a fetch.
			name:   "a step that printed 502 itself",
			err:    "RUN check failed with exit code 1 (Earthfile:9)",
			output: "the server returned 502 Bad Gateway\n",
			want:   false,
		},
		{
			name:   "an ordinary failing step",
			err:    "RUN npm install failed with exit code 1 (Earthfile:9)",
			output: note + "npm ERR! code ERESOLVE\n",
			want:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := cannotHere(tc.err, tc.output); got != tc.want {
				t.Errorf("cannotHere = %v, want %v", got, tc.want)
			}
		})
	}
}
