package cliutil

import (
	"github.com/urfave/cli/v3"
)

func GetValidEnvNames(app *cli.Command) map[string]struct{} {
	envs := map[string]struct{}{}
	for _, envName := range getValidEnvNamesFromCommands(app.Commands) {
		envs[envName] = struct{}{}
	}

	// and root level flags
	for _, flg := range app.Flags {
		for _, envName := range flagEnvVars(flg) {
			envs[envName] = struct{}{}
		}
	}

	return envs
}

func flagEnvVars(flg cli.Flag) []string {
	if df, ok := flg.(cli.DocGenerationFlag); ok {
		return df.GetEnvVars()
	}

	return nil
}

func getValidEnvNamesFromCommands(cmds []*cli.Command) []string {
	envs := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		for _, flg := range cmd.Flags {
			envs = append(envs, flagEnvVars(flg)...)
		}
	}

	return envs
}
