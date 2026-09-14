package layer

import (
	"bytes"
	"os"
	"testing"
)

// Our Command and Action encodings are protoc's, byte for byte.
//
// Same reason as the Directory vector: proto3 defines no canonical form, so an
// encoder checked against itself proves nothing. These two carry the key, so a
// byte wrong here is every cache entry wrong.
//
// Regenerate with:
//
//	protoc --proto_path=engine/layer/testdata/reapi \
//	  --encode=build.bazel.remote.execution.v2.Command \
//	  engine/layer/testdata/reapi/reapi_min.proto \
//	  < engine/layer/testdata/reapi/command.textproto \
//	  > engine/layer/testdata/reapi/command.bin
func TestOurCommandEncodingIsProtocs(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile("testdata/reapi/command.bin")
	if err != nil {
		t.Fatal(err)
	}

	got := EncodeCommand(Command{
		Arguments: []string{"/bin/sh", "-c", "cargo build --release"},
		Env: []Property{
			{Name: "CARGO_TERM_COLOR", Value: "never"},
			{Name: "PATH", Value: "/usr/bin"},
		},
		WorkingDirectory: "/w",
	})

	if !bytes.Equal(got, want) {
		t.Errorf("our Command is %x\n  protoc's is    %x", got, want)
	}
}

func TestOurActionEncodingIsProtocs(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile("testdata/reapi/action.bin")
	if err != nil {
		t.Fatal(err)
	}

	got := EncodeAction(Action{
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
	})

	if !bytes.Equal(got, want) {
		t.Errorf("our Action is %x\n  protoc's is   %x", got, want)
	}
}

// An Action with nothing optional set emits none of it.
//
// **The vector the other one cannot be.** The Action above carries a salt, a
// do_not_cache and a platform, so an encoder that emitted those unconditionally
// would match it exactly. Only a message without them says whether "absent"
// and "present and empty" are being told apart - and they are different bytes,
// so they are different keys.
func TestAnActionWithNothingOptionalIsProtocs(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile("testdata/reapi/action_bare.bin")
	if err != nil {
		t.Fatal(err)
	}

	got := EncodeAction(Action{
		Command:     hexID("0000000000000000000000000000000000000000000000000000000000000011"),
		CommandSize: 7,
		InputRoot:   hexID("0000000000000000000000000000000000000000000000000000000000000022"),
		InputSize:   300,
	})

	if !bytes.Equal(got, want) {
		t.Errorf("our bare Action is %x\n  protoc's is        %x", got, want)
	}
}

// Nothing to say emits nothing, at every level.
//
// An empty Command is empty bytes, and an Action with no platform omits the
// field rather than writing an empty message - the same rule NodeProperties
// follows, for the same reason: a peer with nothing to say must produce exactly
// what we produce.
func TestAnEmptyCommandAndPlatformEmitNothing(t *testing.T) {
	t.Parallel()

	if b := EncodeCommand(Command{}); len(b) != 0 {
		t.Errorf("an empty Command is %x, want nothing", b)
	}

	withNone := EncodeAction(Action{Salt: []byte{1}})
	withEmpty := EncodeAction(Action{Salt: []byte{1}, Platform: []Property{}})

	if !bytes.Equal(withNone, withEmpty) {
		t.Errorf("an Action with no platform is %x and one with an empty platform"+
			" is %x", withNone, withEmpty)
	}
}
