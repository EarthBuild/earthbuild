package common

const (
	// DebuggerSettingsSecretsKey stores the secrets key name
	DebuggerSettingsSecretsKey = "earthbuild_debugger_settings"

	// DebuggerDefaultSocketPath is the default socket to connect to (path is inside the container)
	DebuggerDefaultSocketPath = "/var/run/earthbuild_interactive"

	// DefaultSaveFileSocketPath is the default socket to connect to when sending back files (path is inside the container)
	DefaultSaveFileSocketPath = "/var/run/earthbuild_save"
)

// DebuggerSettings is used to pass settings to the debugger
type DebuggerSettings struct {
	DebugLevelLogging bool                `json:"debugLevel"`
	Enabled           bool                `json:"enabled"`
	SocketPath        string              `json:"socketPath"`
	Term              string              `json:"term"`
	SaveFiles         []SaveFilesSettings `json:"saveFiles"`
}

// SaveFilesSettings is used to pass SAVE ARTIFACT ... AS LOCAL ... commands to the debugger
// which sends them back on failure
type SaveFilesSettings struct {
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	IfExists bool   `json:"ifExists"`
}
