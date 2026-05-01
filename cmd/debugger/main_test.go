package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExitCodeDiagnosticExplains126(t *testing.T) {
	t.Parallel()

	diagnostic := exitCodeDiagnostic(126)

	require.Contains(t, diagnostic, "command was found but could not be executed")
	require.Contains(t, diagnostic, "executable permissions")
	require.Contains(t, diagnostic, "shebang/interpreter")
}

func TestExitCodeDiagnosticSkipsOrdinaryExitCode(t *testing.T) {
	t.Parallel()

	require.Empty(t, exitCodeDiagnostic(1))
}
