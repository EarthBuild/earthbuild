package earthfile

import (
	"slices"
	"strings"
)

// commands lists every canonical Earthfile command keyword. Editor features
// such as completion consume it so the keyword set has exactly one source of
// truth. TestCommandsMatchesCanonicalConstants fails when a Cmd constant is
// added without being listed here.
var commands = []Cmd{
	CmdAdd,
	CmdArg,
	CmdBuild,
	CmdCache,
	CmdCatch,
	CmdCmd,
	CmdCommand,
	CmdCopy,
	CmdDo,
	CmdDocker,
	CmdElse,
	CmdElseIf,
	CmdEnd,
	CmdEntrypoint,
	CmdEnv,
	CmdExpose,
	CmdFinally,
	CmdFor,
	CmdFrom,
	CmdFromDockerfile,
	CmdFunction,
	CmdGitClone,
	CmdHealthCheck,
	CmdHost,
	CmdIf,
	CmdImport,
	CmdLabel,
	CmdLet,
	CmdLoad,
	CmdLocally,
	CmdOnBuild,
	CmdProject,
	CmdRun,
	CmdSaveArtifact,
	CmdSaveImage,
	CmdSet,
	CmdShell,
	CmdStopSignal,
	CmdTry,
	CmdUser,
	CmdVersion,
	CmdVolume,
	CmdWait,
	CmdWith,
	CmdWorkdir,
}

// Commands returns the canonical Earthfile command keywords in sorted order.
func Commands() []Cmd {
	return slices.Clone(commands)
}

// MultiWordCommands returns the canonical commands spelled with more than one
// word, such as SAVE ARTIFACT. Callers that tokenize a command line need them
// to recognize the full keyword before its arguments begin.
func MultiWordCommands() []Cmd {
	var multi []Cmd

	for _, cmd := range commands {
		if strings.ContainsRune(string(cmd), ' ') {
			multi = append(multi, cmd)
		}
	}

	return multi
}
