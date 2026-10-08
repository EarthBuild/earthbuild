// Package conslogging provides specialized console logging implementations, including colorized output,
// buffered logging, and progress reporting for earth builds.
package conslogging

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/fatih/color"
)

const (
	// NoPadding means the old behavior of printing the full target only.
	NoPadding int = -1
	// DefaultPadding always prints 20 characters for the target, right
	// justified. If it is longer, it prints the right 20 characters.
	DefaultPadding int = 20
)

// LogLevel defines which types of log messages are displayed (e.g. warning, info, verbose).
type LogLevel int

const (
	// Silent silences logging.
	Silent LogLevel = iota
	// Warn only display warning log messages.
	Warn
	// Info displays info and higher priority log messages.
	Info
	// Verbose displays verbose and higher priority log messages.
	Verbose
	// Debug displays all log messages.
	Debug
)

const barWidth = 80

var currentConsoleMutex sync.Mutex

// ConsoleLogger is a writer for consoles.
type ConsoleLogger struct {
	prefixWriter PrefixWriter
	consoleErrW  io.Writer
	errW         io.Writer
	// The following are shared between instances and are protected by the mutex.
	mu             *sync.Mutex
	nextColorIndex *int
	// salt is a salt used for color consistency
	// (the same salt will get the same color).
	saltColors        map[string]*color.Color
	salt              string
	prefix            string
	logLevel          LogLevel
	prefixPadding     int
	githubAnnotations bool
	isFailed          bool
	isCached          bool
	// isLocal has a special prefix *local* added.
	isLocal bool
	// metadataMode are printed in a different color.
	metadataMode bool
	trailingLine bool
}

// Current returns the current console.
func Current(prefixPadding int, logLevel LogLevel, githubAnnotations bool) *ConsoleLogger {
	return New(getCompatibleStderr(), &currentConsoleMutex, prefixPadding, logLevel, githubAnnotations)
}

// New returns a new ConsoleLogger with a predefined target writer.
func New(
	w io.Writer, mu *sync.Mutex, prefixPadding int, logLevel LogLevel, githubAnnotations bool,
) *ConsoleLogger {
	if mu == nil {
		mu = &sync.Mutex{}
	}

	return &ConsoleLogger{
		consoleErrW:       w,
		errW:              w,
		saltColors:        make(map[string]*color.Color),
		nextColorIndex:    new(int),
		prefixPadding:     prefixPadding,
		mu:                mu,
		logLevel:          logLevel,
		githubAnnotations: githubAnnotations,
	}
}

func (l *ConsoleLogger) clone() *ConsoleLogger {
	return &ConsoleLogger{
		consoleErrW:       l.consoleErrW,
		errW:              l.errW,
		prefixWriter:      l.prefixWriter,
		prefix:            l.prefix,
		metadataMode:      l.metadataMode,
		isLocal:           l.isLocal,
		logLevel:          l.logLevel,
		salt:              l.salt,
		isCached:          l.isCached,
		isFailed:          l.isFailed,
		githubAnnotations: l.githubAnnotations,
		saltColors:        l.saltColors,
		nextColorIndex:    l.nextColorIndex,
		prefixPadding:     l.prefixPadding,
		mu:                l.mu,
	}
}

// WithPrefix returns a ConsoleLogger with a prefix added.
func (l *ConsoleLogger) WithPrefix(prefix string) *ConsoleLogger {
	return l.WithPrefixAndSalt(prefix, prefix)
}

// WithPrefixAndSalt returns a ConsoleLogger with a prefix and a seed added.
func (l *ConsoleLogger) WithPrefixAndSalt(prefix string, salt string) *ConsoleLogger {
	ret := l.clone()
	ret.prefix = prefix

	ret.salt = salt
	if ret.prefixWriter != nil {
		ret.prefixWriter = ret.prefixWriter.WithPrefix(prefix)
		ret.errW = ret.prefixWriter
	}

	return ret
}

// WithMetadataMode returns a ConsoleLogger with metadata printing mode set.
func (l *ConsoleLogger) WithMetadataMode(metadataMode bool) *ConsoleLogger {
	ret := l.clone()
	ret.metadataMode = metadataMode

	return ret
}

// WithLocal returns a ConsoleLogger with local set.
func (l *ConsoleLogger) WithLocal(isLocal bool) *ConsoleLogger {
	ret := l.clone()
	ret.isLocal = isLocal

	return ret
}

// Prefix returns the console's prefix.
func (l *ConsoleLogger) Prefix() string {
	return l.prefix
}

// Salt returns the console's salt.
func (l *ConsoleLogger) Salt() string {
	return l.salt
}

// WithCached returns a ConsoleLogger with isCached flag set accordingly.
func (l *ConsoleLogger) WithCached(isCached bool) *ConsoleLogger {
	ret := l.clone()
	ret.isCached = isCached

	return ret
}

// WithFailed returns a ConsoleLogger with isFailed flag set accordingly.
func (l *ConsoleLogger) WithFailed(isFailed bool) *ConsoleLogger {
	ret := l.clone()
	ret.isFailed = isFailed

	return ret
}

// WithWriter returns a ConsoleLogger with stderr pointed at the provided io.Writer.
func (l *ConsoleLogger) WithWriter(w io.Writer) *ConsoleLogger {
	ret := l.clone()
	ret.errW = w

	return ret
}

// WithPrefixWriter returns a ConsoleLogger with a prefix writer.
func (l *ConsoleLogger) WithPrefixWriter(w PrefixWriter) *ConsoleLogger {
	ret := l.clone()
	ret.prefixWriter = w
	ret.errW = w

	return ret
}

// PrintPhaseHeader prints the phase header.
func (l *ConsoleLogger) PrintPhaseHeader(phase string, disabled bool, special string) {
	w := new(bytes.Buffer)

	l.mu.Lock()

	defer func() {
		_, _ = w.WriteTo(l.errW)
		l.mu.Unlock()
	}()

	msg := phase

	c := l.color(phaseColor)
	if disabled {
		c = l.color(disabledPhaseColor)
		msg += " (disabled)"
	} else if special != "" {
		c = l.color(specialPhaseColor)
		msg += " (" + special + ")"
	}

	underlineLength := max(utf8.RuneCountInString(msg)+2, barWidth)
	l.printGithubActionsControl(groupCommand, msg)
	c.Fprintf(w, " %s", msg) // #nosec G104
	fmt.Fprintf(w, "\n")
	c.Fprintf(w, "%s", strings.Repeat("—", underlineLength)) // #nosec G104
	fmt.Fprintf(w, "\n\n")
}

// PrintPhaseFooter prints the phase footer.
func (l *ConsoleLogger) PrintPhaseFooter(phase string) {
	w := new(bytes.Buffer)

	l.mu.Lock()

	defer func() {
		_, _ = w.WriteTo(l.errW)
		l.mu.Unlock()
	}()

	c := l.color(noColor)
	l.printGithubActionsControl(endGroupCommand, phase)
	c.Fprintf(w, "\n") // #nosec G104
}

// PrintSuccess prints the success message.
func (l *ConsoleLogger) PrintSuccess() {
	l.PrintBar(successColor, "🌍 Earth Build  ✅ SUCCESS", "")
}

// PrintFailure prints the failure message.
func (l *ConsoleLogger) PrintFailure(phase string) {
	l.PrintBar(warnColor, "❌ FAILURE", phase)
}

// PrefixColor returns the color used for the prefix.
func (l *ConsoleLogger) PrefixColor() *color.Color {
	c, found := l.saltColors[l.salt]
	if !found {
		c = availablePrefixColors[*l.nextColorIndex]
		l.saltColors[l.salt] = c
		*l.nextColorIndex = (*l.nextColorIndex + 1) % len(availablePrefixColors)
	}

	return l.color(c)
}

// PrintGHASummary prints a GitHub Actions summary message to GITHUB_STEP_SUMMARY.
func (l *ConsoleLogger) PrintGHASummary(message string) {
	if !l.githubAnnotations {
		return
	}

	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		w := new(bytes.Buffer)

		defer func() {
			_, _ = w.WriteTo(l.errW)
		}()

		fmt.Print(w, message)

		return
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) // #nosec G302, G304, G703
	if err != nil {
		return
	}
	defer file.Close()

	_, _ = file.WriteString(message + "\n")
}

// GHAError represents a GitHub Actions error with formatted output.
type GHAError struct {
	message string
	file    string
	line    int32
	col     int32
}

// FormattedMessage returns the formatted message for the GHAError.
func (e *GHAError) FormattedMessage() string {
	if e.file != "" {
		return fmt.Sprintf("file=%s,line=%d,col=%d,title=Error::%s", e.file, e.line, e.col, e.message)
	}

	return "title=Error::" + e.message
}

// GHAErrorOpt is a functional option for configuring a GHAError.
type GHAErrorOpt func(*GHAError)

// WithGHASourceLocation specifies the source location file, line, and column for a GHAError.
func WithGHASourceLocation(file string, line, col int32) GHAErrorOpt {
	return func(cfg *GHAError) {
		cfg.file = file
		cfg.line = line
		cfg.col = col
	}
}

// PrintGHAError constructs a GitHub Actions error message.
func (l *ConsoleLogger) PrintGHAError(message string, fns ...GHAErrorOpt) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ge := GHAError{
		message: message,
	}
	for _, fn := range fns {
		fn(&ge)
	}

	l.printGithubActionsControl(errorCommand, ge.FormattedMessage())
}

type ghHeader string

const (
	errorCommand    ghHeader = "::error"
	groupCommand    ghHeader = "::group::"
	endGroupCommand ghHeader = "::endgroup::"
)

// Print GHA control messages like ::group and ::error.
func (l *ConsoleLogger) printGithubActionsControl(header ghHeader, msg string) {
	if !l.githubAnnotations {
		return
	}

	// Assumes mu locked.
	var w bytes.Buffer

	w.WriteString(string(header))
	w.WriteByte(' ')

	if !strings.HasSuffix(msg, "\n") {
		w.WriteByte('\n')
	}

	w.WriteString(msg)

	_, _ = w.WriteTo(l.errW)
}

// PrintBar prints an earth message bar.
func (l *ConsoleLogger) PrintBar(c *color.Color, msg, phase string) {
	w := new(bytes.Buffer)

	l.mu.Lock()

	defer func() {
		_, _ = w.WriteTo(l.errW)
		l.mu.Unlock()
	}()

	c = l.color(c)

	center := msg
	if phase != "" {
		center = fmt.Sprintf("%s [%s]", msg, phase)
	}

	center = fmt.Sprintf(" %s ", center)

	sideWidth := max((barWidth-utf8.RuneCountInString(center))/2, 0)
	eqBar := strings.Repeat("=", sideWidth)
	leftBar := eqBar

	rightBar := eqBar
	if utf8.RuneCountInString(center)%2 == 1 && sideWidth > 0 {
		// Ensure the width is always barWidth
		rightBar += "="
	}

	fmt.Fprintf(w, "\n")
	c.Fprintf(w, "%s%s%s", leftBar, center, rightBar) // #nosec G104
	fmt.Fprintf(w, "\n\n")
}

// Warn prints a warning message in red to errWriter.
func (l *ConsoleLogger) Warn(message string) {
	c := l.color(warnColor)
	l.colorPrint(Warn, c, message)
}

// Warnf prints a formatted warning message in red to errWriter.
func (l *ConsoleLogger) Warnf(format string, args ...any) {
	l.Warn(fmt.Sprintf(format, args...))
}

// VerboseWarn prints a message in red to errWriter when verbose flag is set.
func (l *ConsoleLogger) VerboseWarn(msg string) {
	if l.logLevel < Verbose {
		return
	}

	l.Warn(msg)
}

// VerboseWarnf prints a formatted message in red to errWriter when verbose flag is set.
func (l *ConsoleLogger) VerboseWarnf(format string, args ...any) {
	l.VerboseWarn(fmt.Sprintf(format, args...))
}

// HelpPrint prints message to the console with `Help:` prefix in a specific color.
func (l *ConsoleLogger) HelpPrint(msg string) {
	l.ColorPrint(l.color(helpColor), "\nHelp: "+msg+"\n")
}

// HelpPrintf prints formatted message to the console with `Help:` prefix in a specific color.
func (l *ConsoleLogger) HelpPrintf(format string, args ...any) {
	l.ColorPrintf(l.color(helpColor), "\nHelp: "+format+"\n", args...)
}

// Print prints message to the console.
func (l *ConsoleLogger) Print(msg string) {
	if l == nil {
		return
	}

	c := l.color(noColor)
	if l.metadataMode {
		c = l.color(metadataModeColor)
	}

	l.ColorPrint(c, msg)
}

// Printf prints formatted message to the console.
func (l *ConsoleLogger) Printf(format string, args ...any) {
	l.Print(fmt.Sprintf(format, args...))
}

func (l *ConsoleLogger) colorPrint(level LogLevel, c *color.Color, msg string) {
	if l == nil || l.logLevel < level {
		return
	}

	w := new(bytes.Buffer)

	l.mu.Lock()

	defer func() {
		_, _ = w.WriteTo(l.errW)
		l.mu.Unlock()
	}()

	msg = strings.TrimSuffix(msg, "\n")
	for line := range strings.SplitSeq(msg, "\n") {
		l.printPrefix(w)
		c.Fprintf(w, "%s", line) // #nosec G104

		// Don't use a background color for \n.
		noColor.Fprintf(w, "\n") // #nosec G104
	}
}

// ColorPrint prints message to the console in a specific color.
func (l *ConsoleLogger) ColorPrint(c *color.Color, msg string) {
	l.colorPrint(Info, c, msg)
}

// ColorPrintf prints formatted message to the console in a specific color.
func (l *ConsoleLogger) ColorPrintf(c *color.Color, format string, args ...any) {
	l.colorPrint(Info, c, fmt.Sprintf(format, args...))
}

// PrintBytes prints bytes directly to the console.
func (l *ConsoleLogger) PrintBytes(data []byte) {
	if l == nil {
		return
	}

	w := new(bytes.Buffer)
	w.Grow(len(data) + len(data)/4)
	l.mu.Lock()

	defer func() {
		_, _ = w.WriteTo(l.errW)
		l.mu.Unlock()
	}()

	c := l.color(noColor)
	if l.metadataMode {
		c = l.color(metadataModeColor)
	}

	output := make([]byte, 0, len(data))
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		ch := data[:size]
		data = data[size:]

		switch r {
		case '\r':
			output = append(output, ch...)
			l.trailingLine = false
		case '\n':
			output = append(output, ch...)
			l.trailingLine = false
		default:
			if !l.trailingLine {
				if len(output) > 0 {
					c.Fprintf(w, "%s", string(output)) // #nosec G104
					output = output[:0]
				}

				l.printPrefix(w)
				l.trailingLine = true
			}

			output = append(output, ch...)
		}
	}

	if len(output) > 0 {
		c.Fprintf(w, "%s", string(output)) // #nosec G104
		// output = output[:0] // needed if output is used further in the future
	}
}

// VerbosePrint prints a message to the console when verbose flag is set.
func (l *ConsoleLogger) VerbosePrint(msg string) {
	if l.logLevel < Verbose {
		return
	}

	l.WithMetadataMode(true).Print(msg)
}

// VerbosePrintf prints formatted message to the console when verbose flag is set.
func (l *ConsoleLogger) VerbosePrintf(format string, args ...any) {
	l.VerbosePrint(fmt.Sprintf(format, args...))
}

// VerboseBytes prints bytes directly to the console when verbose flag is set.
func (l *ConsoleLogger) VerboseBytes(data []byte) {
	if l.logLevel < Verbose {
		return
	}

	l.WithMetadataMode(true).PrintBytes(data)
}

// DebugPrintf prints formatted message to the console when debug flag is set.
func (l *ConsoleLogger) DebugPrintf(format string, args ...any) {
	if l.logLevel < Debug {
		return
	}

	l.WithMetadataMode(true).Printf(format, args...)
}

// DebugBytes prints bytes directly to the console when debug flag is set.
func (l *ConsoleLogger) DebugBytes(data []byte) {
	if l.logLevel < Debug {
		return
	}

	l.WithMetadataMode(true).PrintBytes(data)
}

func (l *ConsoleLogger) printPrefix(w io.Writer) {
	// Assumes mu locked.
	if l.prefixWriter != nil {
		// When the prefix writer is in use, we don't need to print the prefix.
		return
	}

	if l.prefix == "" {
		return
	}

	c := l.PrefixColor()
	c.Fprintf(w, "%s", prettyPrefix(l.prefixPadding, l.prefix)) // #nosec G104

	if l.isLocal {
		fmt.Fprintf(w, " *")
		l.color(localColor).Fprintf(w, "local") // #nosec G104
		fmt.Fprintf(w, "*")
	}

	if l.isFailed {
		fmt.Fprintf(w, " *")
		l.color(warnColor).Fprintf(w, "failed") // #nosec G104
		fmt.Fprintf(w, "*")
	}

	fmt.Fprintf(w, " | ")

	if l.isCached {
		fmt.Fprintf(w, "*")
		l.color(cachedColor).Fprintf(w, "cached") // #nosec G104
		fmt.Fprintf(w, "* ")
	}
}

func (l *ConsoleLogger) color(c *color.Color) *color.Color {
	if color.NoColor {
		return noColor
	}

	return c
}

func prettyPrefix(prefixPadding int, prefix string) string {
	return formatter.Format(prefix, prefixPadding)
}

// WithLogLevel changes the log level.
func (l *ConsoleLogger) WithLogLevel(logLevel LogLevel) *ConsoleLogger {
	ret := l.clone()
	ret.logLevel = logLevel

	return ret
}
