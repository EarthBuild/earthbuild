package earthfile2llb

import (
	"strings"
	"testing"
)

func TestRunNeedsDebugger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		locally         bool
		debuggerEnabled bool
		isInteractive   bool
		hasSaveFiles    bool
		want            bool
	}{
		{name: "plain RUN", want: false},
		{name: "-i", debuggerEnabled: true, want: true},
		{name: "RUN --interactive", isInteractive: true, want: true},
		{name: "TRY/FINALLY save files", hasSaveFiles: true, want: true},
		{name: "LOCALLY", locally: true, want: false},
		{name: "LOCALLY with -i", locally: true, debuggerEnabled: true, isInteractive: true, hasSaveFiles: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := runNeedsDebugger(tt.locally, tt.debuggerEnabled, tt.isInteractive, tt.hasSaveFiles)
			if got != tt.want {
				t.Errorf("runNeedsDebugger() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShellWrapDebuggerPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		withDebugger bool
		want         bool
	}{
		{name: "without debugger", withDebugger: false, want: false},
		{name: "with debugger", withDebugger: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := withShellAndEnvVars([]string{"echo", "hi"}, nil, true, tt.withDebugger, false)

			got := strings.Contains(strings.Join(args, " "), debuggerPath)
			if got != tt.want {
				t.Errorf("command %q contains %s = %v, want %v", args, debuggerPath, got, tt.want)
			}
		})
	}
}
