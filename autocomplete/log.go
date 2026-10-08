package autocomplete

import (
	"fmt"
	"os"
)

var logPath string

// SetupLog enables debug-level logging in the autocomplete package when path is set to a logfile.
// this is particularly useful since autocompletion is called via a shell which can mangle stderr output
// and interprets stdout as autocompletion values.
func SetupLog(path string) {
	logPath = path
}

// Logf writes a formatted string to the log file.
func Logf(format string, args ...any) {
	Log(fmt.Sprintf(format, args...))
}

// Log writes a string to the log file.
func Log(s string) {
	if logPath == "" {
		return
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o755) // #nosec G302, G304
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = f.WriteString(s + "\n")
}
