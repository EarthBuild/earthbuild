package docker2earth_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/docker2earth"
)

func TestGenerateNativeEarthfileAST(t *testing.T) {
	t.Parallel()

	// Create a temporary Dockerfile
	tmpDir, err := os.MkdirTemp("", "test-ast-gen")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dockerfileContent := `FROM alpine:3.18
RUN echo "hello"
COPY file.txt /dest
`
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	err = os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644)
	if err != nil {
		t.Fatalf("failed to write temp Dockerfile: %v", err)
	}

	tree, err := docker2earth.GenerateNativeEarthfileAST(dockerfilePath, "myimage:latest")
	if err != nil {
		t.Fatalf("failed to generate native Earthfile AST: %v", err)
	}

	// Verify target count (should be "subbuild1" and "build")
	if len(tree.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(tree.Targets))
	}

	// Verify source line mapping
	foundSubbuild1 := false
	for _, target := range tree.Targets {
		if target.Name == "subbuild1" {
			foundSubbuild1 = true
			if target.SourceLocation == nil {
				t.Fatalf("target subbuild1 has nil SourceLocation")
			}
			if target.SourceLocation.File != dockerfilePath {
				t.Errorf("target subbuild1 File = %q; want %q", target.SourceLocation.File, dockerfilePath)
			}
			if target.SourceLocation.StartLine != 1 {
				t.Errorf("target subbuild1 StartLine = %d; want 1", target.SourceLocation.StartLine)
			}

			// Check recipe statements
			if len(target.Recipe) < 2 {
				t.Fatalf("target subbuild1 has too few recipe statements: %d", len(target.Recipe))
			}

			// FROM alpine:3.18 (first command in recipe)
			fromStmt := target.Recipe[0]
			if fromStmt.Command == nil || fromStmt.Command.Name != "FROM" {
				t.Fatalf("first statement is not FROM")
			}
			if fromStmt.Command.SourceLocation.StartLine != 1 {
				t.Errorf("FROM command line = %d; want 1", fromStmt.Command.SourceLocation.StartLine)
			}

			// RUN echo "hello"
			runStmt := target.Recipe[1]
			if runStmt.Command == nil || runStmt.Command.Name != "RUN" {
				t.Fatalf("second statement is not RUN")
			}
			if runStmt.Command.SourceLocation.StartLine != 2 {
				t.Errorf("RUN command line = %d; want 2", runStmt.Command.SourceLocation.StartLine)
			}

			// COPY file.txt /dest
			copyStmt := target.Recipe[2]
			if copyStmt.Command == nil || copyStmt.Command.Name != "COPY" {
				t.Fatalf("third statement is not COPY")
			}
			if copyStmt.Command.SourceLocation.StartLine != 3 {
				t.Errorf("COPY command line = %d; want 3", copyStmt.Command.SourceLocation.StartLine)
			}
		}
	}

	if !foundSubbuild1 {
		t.Errorf("did not find target subbuild1")
	}
}
