package guest

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// wireVocabulary is every request a peer may make, and what it is allowed to
// mean.
//
// A register rather than a derivation, because the property being held is that
// the vocabulary **does not grow a way to run something outside the sandbox**.
// A test that derived the list from the code would accept whatever the code
// says, which is the opposite of a guard.
var wireVocabulary = map[Kind]string{
	KindHello:       "version handshake; runs nothing",
	KindMaterialise: "assemble a layer stack; the stack is named by digests the peer already holds",
	KindRelease:     "unmount a handle this connection made",
	KindObserve:     "report what a step looked at; reads, never runs",
	KindExec:        "run a command **inside** a step's filesystem, confined",
	KindCapture:     "digest what a step wrote",
	KindExport:      "copy an artifact out of a materialised stack",
	KindCopy:        "copy between layers this connection can name",
	KindStoreHas:    "report which of these layer ids the store holds; reads, never runs",
	KindSquash:      "merge a range of the stack into one layer in the store; reads and writes layers, never runs",
	KindPackImage:   "write a loadable image archive into the store from layers it already holds; never runs",
	KindUnpackLayer: "unpack a compressed blob this peer named into the store;" +
		" writes a layer, never runs anything from it",
	KindFileConfig: "file an image's configuration beside a layer the store holds;" +
		" writes a sidecar and a declaration, never runs anything",
	KindCancel: "abandon a request this connection made",
}

// The wire vocabulary cannot express running on the host.
//
// Green paper C.3: *"`host` is not in the wire vocabulary. A `host` op cannot be
// expressed in an assignment, so a malicious peer cannot request one. **This is
// a property of the type, not a check that could be forgotten.**"*
//
// A property of the type is exactly the kind that dies quietly: somebody adds a
// request kind for a good local reason, and the sentence in the specification
// stops being true without anything failing. The engine cites C.3 in three
// comments and `OpHost`'s own doc says it is *"absent from the wire vocabulary
// entirely"*.
//
// So the vocabulary is registered here, with what each kind is allowed to mean,
// and a kind that appears in the protocol and not in this list fails. Adding one
// is then a deliberate act with a sentence attached, which is the most a test
// can ask of a design property.
//
// `Unconfined` is the other half and is not reachable from the wire at all: it
// is a field on the server, set by the process that starts the guest. A peer
// cannot ask for it, which is why it is a field and not a request.
func TestTheWireVocabularyCannotReachTheHost(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("proto.go")
	if err != nil {
		t.Fatal(err)
	}

	declared := regexp.MustCompile(`(?m)^\s*(Kind\w+)\s+Kind = "([^"]+)"`).
		FindAllStringSubmatch(string(b), -1)

	if len(declared) == 0 {
		t.Fatal("no request kinds found, so this asserts nothing")
	}

	for _, m := range declared {
		kind := Kind(m[2])

		meaning, listed := wireVocabulary[kind]
		if !listed {
			t.Errorf("%s (%q) is a request a peer may make and is not accounted for:"+
				"\n  green paper C.3 says the wire vocabulary cannot express running on the"+
				"\n  host, and that is a property of this list rather than of any check"+
				"\n  add it to wireVocabulary with what it is allowed to mean", m[1], kind)

			continue
		}

		// A kind whose meaning mentions the host is the thing C.3 forbids.
		if strings.Contains(strings.ToLower(meaning), "host") ||
			strings.Contains(strings.ToLower(meaning), "unconfined") {
			t.Errorf("%s is described as reaching the host: %q", m[1], meaning)
		}
	}

	// And nothing in the register has been left behind by a kind that was
	// removed, which would let a future kind reuse the name and the sentence.
	for kind := range wireVocabulary {
		found := false

		for _, m := range declared {
			if Kind(m[2]) == kind {
				found = true

				break
			}
		}

		if !found {
			t.Errorf("the register describes %q, which the protocol no longer declares", kind)
		}
	}
}
