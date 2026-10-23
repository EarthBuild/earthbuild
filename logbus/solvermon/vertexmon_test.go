package solvermon

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logstream"
	"github.com/EarthBuild/earthbuild/util/statsstreamparser"
	"github.com/stretchr/testify/assert"
)

func TestGetExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expectedError error
		name          string
		errString     string
		expectedCode  int
	}{
		{
			name:          "no match",
			errString:     "random error message",
			expectedCode:  0,
			expectedError: errNoExitCode,
		},
		{
			name:          "match with exit code",
			errString:     "process \"foo\" did not complete successfully: exit code: 123",
			expectedCode:  123,
			expectedError: nil,
		},
		{
			name:          "match with max uint32",
			errString:     "process \"foo\" did not complete successfully: exit code: 4294967295",
			expectedCode:  0,
			expectedError: errNoExitCodeOMM,
		},
		{
			name:          "match with max uint32",
			errString:     "some wrap message: process \"foo\" did not complete successfully: exit code: 8",
			expectedCode:  8,
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, err := getExitCode(tt.errString)
			if code != tt.expectedCode {
				t.Errorf("getExitCode(%q) = %d, want %d", tt.errString, code, tt.expectedCode)
			}

			if !errors.Is(err, tt.expectedError) {
				t.Errorf("getExitCode(%q) = %d, want %d", tt.errString, err, tt.expectedError)
			}
		})
	}
}

func TestDetermineFatalErrorType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		parseErr      error
		name          string
		errString     string
		exitCode      int
		expectedType  logstream.FailureType
		expectedFatal bool
	}{
		{
			name:          "context canceled",
			errString:     "context canceled",
			exitCode:      0,
			parseErr:      nil,
			expectedType:  logstream.FailureType_FAILURE_TYPE_UNKNOWN,
			expectedFatal: false,
		},
		{
			name:          "exit code 123",
			errString:     "process \"foo\" did not complete successfully: exit code: 123",
			exitCode:      123,
			parseErr:      nil,
			expectedType:  logstream.FailureType_FAILURE_TYPE_NONZERO_EXIT,
			expectedFatal: true,
		},
		{
			name:          "exit code max uint32",
			errString:     "process \"foo\" did not complete successfully: exit code: 4294967295",
			exitCode:      0,
			parseErr:      errNoExitCodeOMM,
			expectedType:  logstream.FailureType_FAILURE_TYPE_OOM_KILLED,
			expectedFatal: true,
		},
		{
			name:          "file not found",
			errString:     "failed to calculate checksum of ref foo: bar",
			exitCode:      0,
			parseErr:      nil,
			expectedType:  logstream.FailureType_FAILURE_TYPE_FILE_NOT_FOUND,
			expectedFatal: true,
		},
		{
			name:          "file not found (internal)",
			errString:     "internalfailed to calculate checksum of ref foo: bar",
			exitCode:      0,
			parseErr:      nil,
			expectedType:  logstream.FailureType_FAILURE_TYPE_FILE_NOT_FOUND,
			expectedFatal: true,
		},
		{
			name:          "file not found (internal with space)",
			errString:     " internalfailed to calculate checksum of ref foo: bar",
			exitCode:      0,
			parseErr:      nil,
			expectedType:  logstream.FailureType_FAILURE_TYPE_FILE_NOT_FOUND,
			expectedFatal: true,
		},
		{
			name:          "git error",
			errString:     "EARTHLY_GIT_STDERR: Z2l0IC1jI...",
			parseErr:      nil,
			expectedType:  logstream.FailureType_FAILURE_TYPE_GIT,
			expectedFatal: true,
		},
		{
			name:          "unknown error",
			errString:     "unknown error",
			parseErr:      nil,
			expectedType:  logstream.FailureType_FAILURE_TYPE_UNKNOWN,
			expectedFatal: false,
		},
		{
			name:          "invalid exit code",
			errString:     "exit code: 9999",
			parseErr:      errors.New("exit code 9999 out of expected range (0-255)"),
			expectedType:  logstream.FailureType_FAILURE_TYPE_UNKNOWN,
			expectedFatal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fatalType, fatal := determineFatalErrorType(tt.errString, tt.exitCode, tt.parseErr)
			if fatalType != tt.expectedType {
				t.Errorf("determineFatalErrorType(%q, %d) = %v, want %v", tt.errString, tt.exitCode, fatalType, tt.expectedType)
			}

			if fatal != tt.expectedFatal {
				t.Errorf("determineFatalErrorType(%q, %d) = %v, want %v", tt.errString, tt.exitCode, fatal, tt.expectedFatal)
			}
		})
	}
}

func TestReErrNotFound(t *testing.T) {
	t.Parallel()

	//nolint:goconst
	tests := []struct {
		name      string
		errString string
		expected  []string
	}{
		{
			name:      "simple",
			errString: "failed to calculate checksum of ref foo: bar",
			expected:  []string{"", "foo", "bar"},
		},
		{
			name:      "simple (internal)",
			errString: "internalfailed to calculate checksum of ref foo: bar",
			expected:  []string{"internal", "foo", "bar"},
		},
		{
			name:      "simple (internal with space)",
			errString: " internalfailed to calculate checksum of ref foo: bar",
			expected:  []string{"internal", "foo", "bar"},
		},
		{
			name:      "complex",
			errString: ` failed to calculate checksum of ref p4gz72iufvk3t1nsqq07p9sim::m4m7o7gui4zuuoy9vynbrzx8f: "/doesnotexist": not found`, //nolint:lll
			expected:  []string{"", "p4gz72iufvk3t1nsqq07p9sim::m4m7o7gui4zuuoy9vynbrzx8f", `"/doesnotexist": not found`},                      //nolint:lll
		},
		{
			name:      "complex (internal)",
			errString: ` internalfailed to calculate checksum of ref p4gz72iufvk3t1nsqq07p9sim::m4m7o7gui4zuuoy9vynbrzx8f: "/doesnotexist": not found`, //nolint:lll
			expected:  []string{"internal", "p4gz72iufvk3t1nsqq07p9sim::m4m7o7gui4zuuoy9vynbrzx8f", `"/doesnotexist": not found`},                      //nolint:lll
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			match := reErrNotFound.FindStringSubmatch(tt.errString)

			if len(match) == 0 || !assert.ElementsMatch(t, match[1:], tt.expected) {
				t.Errorf("reErrNotFound.FindStringSubmatch(%s) = %v, want %v", tt.errString, match, tt.expected)
			}
		})
	}
}

func TestVertexMonitor_Write_StatsStream_NonFatalOnError(t *testing.T) {
	t.Parallel()

	bus := logbus.New()

	cp, err := bus.Run().NewCommand(
		"cmd-id", "echo hello", "target-id", "cmd", "linux/amd64",
		false, false, false, earthfile.SourceLocation{}, "", "", "",
	)
	if err != nil {
		t.Fatalf("failed to create command: %v", err)
	}

	vm := &vertexMonitor{
		cp:  cp,
		ssp: statsstreamparser.New(),
	}

	// 1. Send corrupted/scrambled stats stream data (e.g. JSON starting with '{' / 123)
	corrupted := []byte(`{"cpu":{"usage":{"total":100}}}`)

	n, err := vm.Write(corrupted, time.Now(), BuildkitStatsStream)
	if err != nil {
		t.Fatalf("Write returned error %v, want nil (non-fatal)", err)
	}

	if n != len(corrupted) {
		t.Errorf("Write returned n=%d, want %d", n, len(corrupted))
	}

	// 2. Following the corrupted data, parser should be reset and able to parse a valid packet
	validPayload := `{"cpu":{"usage":{"total":200}}}`
	buf := make([]byte, 1+4+len(validPayload))
	buf[0] = 1                                                         // version 1
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(validPayload))) // #nosec G115
	copy(buf[5:], validPayload)

	n, err = vm.Write(buf, time.Now(), BuildkitStatsStream)
	if err != nil {
		t.Fatalf("Write returned error %v on subsequent valid packet, want nil", err)
	}

	if n != len(buf) {
		t.Errorf("Write returned n=%d, want %d", n, len(buf))
	}
}

func TestVertexMonitor_Write_StatsStream_Success(t *testing.T) {
	t.Parallel()

	bus := logbus.New()

	cp, err := bus.Run().NewCommand(
		"cmd-stats-success", "echo hello", "target-stats-success", "cmd", "linux/amd64",
		false, false, false, earthfile.SourceLocation{}, "", "", "",
	)
	if err != nil {
		t.Fatalf("failed to create command: %v", err)
	}

	vm := &vertexMonitor{
		cp:  cp,
		ssp: statsstreamparser.New(),
	}

	// 1. Single valid stats packet
	payload1 := `{"cpu":{"usage":{"total":12345}}}`
	buf1 := make([]byte, 1+4+len(payload1))
	buf1[0] = 1
	binary.LittleEndian.PutUint32(buf1[1:5], uint32(len(payload1))) // #nosec G115
	copy(buf1[5:], payload1)

	n, err := vm.Write(buf1, time.Now(), BuildkitStatsStream)
	if err != nil {
		t.Fatalf("Write single packet returned error %v, want nil", err)
	}

	if n != len(buf1) {
		t.Errorf("Write single packet returned n=%d, want %d", n, len(buf1))
	}

	// 2. Concatenated multiple valid stats packets in a single Write
	payload2 := `{"cpu":{"usage":{"total":67890}}}`
	buf2 := make([]byte, 1+4+len(payload2))
	buf2[0] = 1
	binary.LittleEndian.PutUint32(buf2[1:5], uint32(len(payload2))) // #nosec G115
	copy(buf2[5:], payload2)

	combined := make([]byte, 0, len(buf1)+len(buf2))
	combined = append(combined, buf1...)
	combined = append(combined, buf2...)

	n, err = vm.Write(combined, time.Now(), BuildkitStatsStream)
	if err != nil {
		t.Fatalf("Write combined packets returned error %v, want nil", err)
	}

	if n != len(combined) {
		t.Errorf("Write combined packets returned n=%d, want %d", n, len(combined))
	}
}

func TestVertexMonitor_Write_StandardStreams(t *testing.T) {
	t.Parallel()

	bus := logbus.New()

	cp, err := bus.Run().NewCommand(
		"cmd-std-streams", "echo hello", "target-std-streams", "cmd", "linux/amd64",
		false, false, false, earthfile.SourceLocation{}, "", "", "",
	)
	if err != nil {
		t.Fatalf("failed to create command: %v", err)
	}

	vm := &vertexMonitor{
		cp:  cp,
		ssp: statsstreamparser.New(),
	}

	tests := map[string]struct {
		data   []byte
		stream int
	}{
		"stdout stream": {
			data:   []byte("standard output log line\n"),
			stream: 1,
		},
		"stderr stream": {
			data:   []byte("standard error log line\n"),
			stream: 2,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n, err := vm.Write(tt.data, time.Now(), tt.stream)
			if err != nil {
				t.Fatalf("Write stream %d returned error %v, want nil", tt.stream, err)
			}

			if n != len(tt.data) {
				t.Errorf("Write stream %d returned n=%d, want %d", tt.stream, n, len(tt.data))
			}
		})
	}
}

func TestVertexMonitor_Write_StatsStream_ReportsDecodeErrorOnce(t *testing.T) {
	t.Parallel()

	bus := logbus.New()

	cp, err := bus.Run().NewCommand(
		"cmd-id", "echo hello", "target-id", "cmd", "linux/amd64",
		false, false, false, earthfile.SourceLocation{}, "", "", "",
	)
	if err != nil {
		t.Fatalf("failed to create command: %v", err)
	}

	var reported []error

	vm := &vertexMonitor{
		cp:                 cp,
		ssp:                statsstreamparser.New(),
		onStatsDecodeError: func(err error) { reported = append(reported, err) },
	}

	corrupted := []byte(`{"cpu":{"usage":{"total":100}}}`)

	for range 3 {
		_, err = vm.Write(corrupted, time.Now(), BuildkitStatsStream)
		if err != nil {
			t.Fatalf("Write returned error %v, want nil (non-fatal)", err)
		}
	}

	if want := 1; len(reported) != want {
		t.Fatalf("decode error reported %d times, want %d", len(reported), want)
	}

	if reported[0] == nil {
		t.Errorf("reported decode error is nil, want the parser error")
	}
}
