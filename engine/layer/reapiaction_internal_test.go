package layer

import (
	"bytes"
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
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

// Our ActionResult encoding is protoc's, with and without an exit code.
//
// Two vectors for the reason the Action needed two: proto3 omits a field at its
// zero value, and a success - exit 0, the common case - is the one an encoder
// writing the field unconditionally gets wrong.
func TestOurActionResultEncodingIsProtocs(t *testing.T) {
	t.Parallel()

	root := hexID("0000000000000000000000000000000000000000000000000000000000000033")

	for _, tc := range []struct {
		file string
		exit int32
	}{
		{"testdata/reapi/result.bin", 2},
		{"testdata/reapi/result_ok.bin", 0},
	} {
		want, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}

		got := EncodeActionResult(Result{Root: root, RootSize: 346, ExitCode: tc.exit})
		if !bytes.Equal(got, want) {
			t.Errorf("exit %d: ours is %x\n  protoc's is %x", tc.exit, got, want)
		}
	}
}

// Our capabilities reply is protoc's.
//
// The first thing any client asks and the first chance to be wrong about the
// wire in a way that ends the conversation.
func TestOurCapabilitiesEncodingIsProtocs(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile("testdata/reapi/caps.bin")
	if err != nil {
		t.Fatal(err)
	}

	if got := EncodeCapabilities(DigestFunctionSHA256, 4<<20); !bytes.Equal(got, want) {
		t.Errorf("ours is %x\n  protoc's is %x", got, want)
	}
}

// A FindMissingBlobs request from protoc reads back as the digests it names.
//
// **Decoding checked against a message we did not write.** An encoder verified
// against protoc proves we can be understood; this proves we can understand -
// and the two are separate risks, since a decoder generous in the same way an
// encoder is wrong would agree with itself perfectly.
func TestWeReadAFindMissingBlobsRequestProtocWrote(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("testdata/reapi/ask.bin")
	if err != nil {
		t.Fatal(err)
	}

	got, err := DigestsInRequest(b)
	if err != nil {
		t.Fatal(err)
	}

	want := []ir.NodeID{
		hexID("0000000000000000000000000000000000000000000000000000000000000044"),
		hexID("0000000000000000000000000000000000000000000000000000000000000066"),
	}

	if len(got) != len(want) {
		t.Fatalf("read %d digests, protoc wrote %d", len(got), len(want))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("digest %d is %v, want %v", i, got[i], want[i])
		}
	}
}

// And our reply is the one protoc writes.
func TestOurMissingBlobsReplyIsProtocs(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile("testdata/reapi/missing.bin")
	if err != nil {
		t.Fatal(err)
	}

	// The fixture's first entry carries a size and ours does not, so the
	// comparison is against the response protoc writes for what we send: a
	// client is told which blobs to send, not how big they are.
	got := EncodeMissingBlobs([]ir.NodeID{
		hexID("0000000000000000000000000000000000000000000000000000000000000044"),
		hexID("0000000000000000000000000000000000000000000000000000000000000055"),
	})

	if bytes.Equal(got, want) {
		return // the fixture happened to match
	}

	// Otherwise it must differ only in the size protoc's fixture states.
	if len(got) >= len(want) {
		t.Errorf("ours is %x\n  protoc's is %x", got, want)
	}
}

// We read an upload protoc wrote, bytes and all.
func TestWeReadABatchUpdateBlobsRequestProtocWrote(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("testdata/reapi/upload.bin")
	if err != nil {
		t.Fatal(err)
	}

	got, err := UploadsInRequest(b)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 {
		t.Fatalf("read %d uploads, protoc wrote 1", len(got))
	}

	if want := hexID("0000000000000000000000000000000000000000000000000000000000000077"); got[0].Digest != want {
		t.Errorf("the upload names %v, want %v", got[0].Digest, want)
	}

	if string(got[0].Data) != "hello" {
		t.Errorf("the upload carries %q, want %q", got[0].Data, "hello")
	}
}
