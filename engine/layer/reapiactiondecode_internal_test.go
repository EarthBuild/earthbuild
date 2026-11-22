package layer

import (
	"os"
	"reflect"
	"testing"
)

// We read the Command protoc wrote.
//
// **Against protoc's bytes, not our own.** A decoder checked against the
// encoder beside it agrees with whatever that encoder does, including whatever
// it does wrongly - and these two carry the key. The fixture is the same one
// TestOurCommandEncodingIsProtocs writes against, so the pair proves the round
// trip goes through protoc rather than around it.
func TestWeReadTheCommandProtocWrote(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("testdata/reapi/command.bin")
	if err != nil {
		t.Fatal(err)
	}

	got, err := CommandIn(b)
	if err != nil {
		t.Fatal(err)
	}

	want := Command{
		Arguments: []string{"/bin/sh", "-c", "cargo build --release"},
		Env: []Property{
			{Name: "CARGO_TERM_COLOR", Value: "never"},
			{Name: "PATH", Value: "/usr/bin"},
		},
		WorkingDirectory: "/w",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("read %+v\n  want %+v", got, want)
	}
}

// We read the Action protoc wrote, including the fields that are absent.
func TestWeReadTheActionProtocWrote(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file string
		want Action
	}{{
		file: "testdata/reapi/action.bin",
		want: Action{
			Command:     hexID("0000000000000000000000000000000000000000000000000000000000000011"),
			CommandSize: 7,
			InputRoot:   hexID("0000000000000000000000000000000000000000000000000000000000000022"),
			InputSize:   300,
			DoNotCache:  true,
			Salt:        []byte{4},
			Platform: []Property{
				{Name: "earthbuild.privileged", Value: "1"},
				{Name: "os", Value: "linux"},
			},
		},
	}, {
		// **The one that says absent and empty are told apart.** A decoder that
		// invented a zero salt or an empty platform would read this the same as
		// the one above reads with them stripped, and the difference is a
		// different key.
		file: "testdata/reapi/action_bare.bin",
		want: Action{
			Command:     hexID("0000000000000000000000000000000000000000000000000000000000000011"),
			CommandSize: 7,
			InputRoot:   hexID("0000000000000000000000000000000000000000000000000000000000000022"),
			InputSize:   300,
		},
	}} {
		b, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}

		got, err := ActionIn(b)
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}

		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: read %+v\n  want %+v", tc.file, got, tc.want)
		}
	}
}

// What we encode we read back, for anything the fixtures do not cover.
//
// `output_paths` has no fixture because nothing encoded one until R4, and a
// field nobody reads is a field that quietly decodes to nothing.
func TestAnActionAndItsCommandSurviveTheRoundTrip(t *testing.T) {
	t.Parallel()

	c := Command{
		Arguments:        []string{"buck2", "build", "//:all"},
		Env:              []Property{{Name: "HOME", Value: "/root"}},
		WorkingDirectory: "/w",
		OutputPaths:      []string{"buck-out/gen/app", "buck-out/log"},
	}

	gotC, err := CommandIn(EncodeCommand(c))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(gotC, c) {
		t.Errorf("command read %+v\n  want %+v", gotC, c)
	}

	a := Action{
		Command:     hexID("00000000000000000000000000000000000000000000000000000000000000aa"),
		CommandSize: int64(len(EncodeCommand(c))),
		InputRoot:   hexID("00000000000000000000000000000000000000000000000000000000000000bb"),
		InputSize:   42,
		Salt:        []byte("5"),
		Platform:    []Property{{Name: "container-image", Value: "docker://busybox@sha256:00"}},
	}

	gotA, err := ActionIn(EncodeAction(a))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(gotA, a) {
		t.Errorf("action read %+v\n  want %+v", gotA, a)
	}
}

// A truncated message is refused, not half read.
//
// **An action half understood is the worst outcome available.** A command with
// its last argument lost still runs, produces something, and is filed under the
// key of the action that was sent - so every later build gets the wrong answer
// from the cache and nothing anywhere says why.
func TestATruncatedActionIsRefused(t *testing.T) {
	t.Parallel()

	full, err := os.ReadFile("testdata/reapi/action.bin")
	if err != nil {
		t.Fatal(err)
	}

	var refused int

	for n := 1; n < len(full); n++ {
		if _, err := ActionIn(full[:n]); err != nil {
			refused++
		}
	}

	// Not every prefix can be detected - one ending on a field boundary is a
	// shorter valid message - so this asserts that truncation is noticed at
	// all, and separately that a whole message still reads.
	if refused == 0 {
		t.Error("no truncation of an Action was refused")
	}

	if _, err := ActionIn(full); err != nil {
		t.Errorf("the whole message was refused: %v", err)
	}
}
