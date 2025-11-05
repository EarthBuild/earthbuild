package shell

import "github.com/earthbuild/earthbuild/ast"

// IsValidEnvVarName returns true if env name is valid
func IsValidEnvVarName(name string) bool {
	return ast.IsValidEnvVarName(name)
}
