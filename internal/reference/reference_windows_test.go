//go:build windows

package reference

import (
	"testing"
)

//nolint:goconst
var targetTests = []struct {
	in  string
	out Reference
}{
	{"+target", Reference{Target: "target", LocalPath: "."}},
	{"+another-target", Reference{Target: "another-target", LocalPath: "."}},
	{`.\rel\win\dir+target`, Reference{Target: "target", LocalPath: `.\rel\win\dir`}},
	{`./rel/win/dir+target`, Reference{Target: "target", LocalPath: `./rel/win/dir`}},
	{`C:\abs\win\dir+target`, Reference{Target: "target", LocalPath: `C:\abs\win\dir`}},
	{`.\rel\space here\dir+target`, Reference{Target: "target", LocalPath: `.\rel\space here\dir`}},
	{`.\rel\fwd/slash\dir+target`, Reference{Target: "target", LocalPath: `.\rel\fwd/slash\dir`}},
	{"github.com/foo/bar+target", Reference{Target: "target", GitURL: "github.com/foo/bar"}},
	{"github.com/foo/bar:tag+target", Reference{Target: "target", GitURL: "github.com/foo/bar", Tag: "tag"}},
	{"github.com/foo/bar:tag/with/slash+target", Reference{Target: "target", GitURL: "github.com/foo/bar", Tag: "tag/with/slash"}},
	{"import+target", Reference{Target: "target", ImportRef: "import"}},
	{"github.com/foo/bar/dir-with-\\+-in+target", Reference{Target: "target", GitURL: "github.com/foo/bar/dir-with-+-in"}},
	{"github.com/foo/bar:tag-with-\\+-in+target", Reference{Target: "target", GitURL: "github.com/foo/bar", Tag: "tag-with-+-in"}},
}

var targetNegativeTests = []string{
	"+COMMAND", "./something+COMMAND", "nope", "abc+cde+efg", "+target/artifact",
	"+123target", "+_target", "+-target",
}

func TestTargetParserWin(t *testing.T) {
	for _, tt := range targetTests {
		t.Run(tt.in, func(t *testing.T) {
			out, err := ParseTarget(tt.in)
			NoError(t, err, "parse target failed")

			expected := tt.out
			expected.kind = expected.Kind()
			Equal(t, expected, out)
		})
	}
}

func TestTargetParserNegativeWin(t *testing.T) {
	for _, tt := range targetNegativeTests {
		t.Run(tt, func(t *testing.T) {
			_, err := ParseTarget(tt)
			Error(t, err, "parse target should have failed")
		})
	}
}

func TestTargetToStringWin(t *testing.T) {
	for _, tt := range targetTests {
		t.Run(tt.in, func(t *testing.T) {
			str := tt.out.String()
			Equal(t, tt.in, str)
		})
	}
}

var artifactTests = []struct {
	in  string
	out Artifact
}{
	{"+target/artifact", Artifact{Target: Reference{Target: "target", LocalPath: "."}, Artifact: "/artifact"}},
	{"+another-target/another-artifact", Artifact{Target: Reference{Target: "another-target", LocalPath: "."}, Artifact: "/another-artifact"}},
	{"+another-target/deep/artifact", Artifact{Target: Reference{Target: "another-target", LocalPath: "."}, Artifact: "/deep/artifact"}},
	{"+another-target/deep/artifact/with/*", Artifact{Target: Reference{Target: "another-target", LocalPath: "."}, Artifact: "/deep/artifact/with/*"}},
	{`.\rel\win\dir+target/artifact`, Artifact{Target: Reference{Target: "target", LocalPath: `.\rel\win\dir`}, Artifact: "/artifact"}},
	{`./rel/win/dir+target/artifact`, Artifact{Target: Reference{Target: "target", LocalPath: `./rel/win/dir`}, Artifact: "/artifact"}},
	{`.\rel\space here\dir+target/artifact`, Artifact{Target: Reference{Target: "target", LocalPath: `.\rel\space here\dir`}, Artifact: "/artifact"}},
	{`.\rel\fwd/slash\dir+target/artifact`, Artifact{Target: Reference{Target: "target", LocalPath: `.\rel\fwd/slash\dir`}, Artifact: "/artifact"}},
	{`C:\abs\win\dir+target/artifact`, Artifact{Target: Reference{Target: "target", LocalPath: `C:\abs\win\dir`}, Artifact: "/artifact"}},
	{"github.com/foo/bar+target/artifact", Artifact{Target: Reference{Target: "target", GitURL: "github.com/foo/bar"}, Artifact: "/artifact"}},
	{"github.com/foo/bar:tag+target/artifact", Artifact{Target: Reference{Target: "target", GitURL: "github.com/foo/bar", Tag: "tag"}, Artifact: "/artifact"}},
	{"github.com/foo/bar:tag/with/slash+target/artifact", Artifact{Target: Reference{Target: "target", GitURL: "github.com/foo/bar", Tag: "tag/with/slash"}, Artifact: "/artifact"}},
	{"github.com/foo/bar/dir-with-\\+-in+target/artifact", Artifact{Target: Reference{Target: "target", GitURL: "github.com/foo/bar/dir-with-+-in"}, Artifact: "/artifact"}},
	{"github.com/foo/bar:tag-with-\\+-in+target/artifact", Artifact{Target: Reference{Target: "target", GitURL: "github.com/foo/bar", Tag: "tag-with-+-in"}, Artifact: "/artifact"}},
	{"github.com/foo/bar/dir-with-\\+-in+target/artifact-with-\\+/in/it", Artifact{Target: Reference{Target: "target", GitURL: "github.com/foo/bar/dir-with-+-in"}, Artifact: "/artifact-with-+/in/it"}},
	{"github.com/foo/bar:tag-with-\\+-in+target/artifact-with-\\+/in/it", Artifact{Target: Reference{Target: "target", GitURL: "github.com/foo/bar", Tag: "tag-with-+-in"}, Artifact: "/artifact-with-+/in/it"}},
}

var artifactNegativeTests = []string{
	"+COMMAND/art", "./something+COMMAND/art", "nope/art", "abc+cde+efg/art", "+just-target",
}

func TestArtifactParserWin(t *testing.T) {
	for _, tt := range artifactTests {
		t.Run(tt.in, func(t *testing.T) {
			out, err := ParseArtifact(tt.in)
			NoError(t, err, "parse artifact failed")

			expected := tt.out
			expected.Target.kind = expected.Target.Kind()
			Equal(t, expected, out)
		})
	}
}

func TestArtifactParserNegativeWin(t *testing.T) {
	for _, tt := range artifactNegativeTests {
		t.Run(tt, func(t *testing.T) {
			_, err := ParseArtifact(tt)
			Error(t, err, "parse artifact should have failed")
		})
	}
}

func TestArtifactToStringWin(t *testing.T) {
	for _, tt := range artifactTests {
		t.Run(tt.in, func(t *testing.T) {
			str := tt.out.String()
			Equal(t, tt.in, str)
		})
	}
}

var commandTests = []struct {
	in  string
	out Reference
}{
	{"+COMMAND", Reference{Command: "COMMAND", LocalPath: "."}},
	{"+ANOTHER_COMMAND", Reference{Command: "ANOTHER_COMMAND", LocalPath: "."}},
	{`.\rel\win\dir+COMMAND`, Reference{Command: "COMMAND", LocalPath: `.\rel\win\dir`}},
	{`./rel/win/dir+COMMAND`, Reference{Command: "COMMAND", LocalPath: `./rel/win/dir`}},
	{`.\rel\space here\dir+COMMAND`, Reference{Command: "COMMAND", LocalPath: `.\rel\space here\dir`}},
	{`.\rel\fwd/slash\dir+COMMAND`, Reference{Command: "COMMAND", LocalPath: `.\rel\fwd/slash\dir`}},
	{`C:\abs\win\dir+COMMAND`, Reference{Command: "COMMAND", LocalPath: `C:\abs\win\dir`}},
	{"github.com/foo/bar+COMMAND", Reference{Command: "COMMAND", GitURL: "github.com/foo/bar"}},
	{"github.com/foo/bar:tag+COMMAND", Reference{Command: "COMMAND", GitURL: "github.com/foo/bar", Tag: "tag"}},
	{"github.com/foo/bar:tag/with/slash+COMMAND", Reference{Command: "COMMAND", GitURL: "github.com/foo/bar", Tag: "tag/with/slash"}},
	{"import+COMMAND", Reference{Command: "COMMAND", ImportRef: "import"}},
	{"github.com/foo/bar/dir-with-\\+-in+COMMAND", Reference{Command: "COMMAND", GitURL: "github.com/foo/bar/dir-with-+-in"}},
	{"github.com/foo/bar:tag-with-\\+-in+COMMAND", Reference{Command: "COMMAND", GitURL: "github.com/foo/bar", Tag: "tag-with-+-in"}},
}

var commandNegativeTests = []string{
	"+target", "./something+target", "nope", "NOPE", "ABC+DEF+EFG", "+COMMAND/artifact",
	"+1COMMAND", "+_COMMAND", "+MY-COMMAND",
}

func TestCommandParserWin(t *testing.T) {
	for _, tt := range commandTests {
		t.Run(tt.in, func(t *testing.T) {
			out, err := ParseCommand(tt.in)
			NoError(t, err, "parse target failed")

			expected := tt.out
			expected.kind = expected.Kind()
			Equal(t, expected, out)
		})
	}
}

func TestCommandParserNegativeWin(t *testing.T) {
	for _, tt := range commandNegativeTests {
		t.Run(tt, func(t *testing.T) {
			_, err := ParseCommand(tt)
			Error(t, err, "parse command should have failed")
		})
	}
}

func TestCommandToStringWin(t *testing.T) {
	for _, tt := range commandTests {
		t.Run(tt.in, func(t *testing.T) {
			str := tt.out.String()
			Equal(t, tt.in, str)
		})
	}
}
