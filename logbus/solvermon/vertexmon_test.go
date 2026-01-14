package solvermon

import (
	"testing"

	"github.com/earthly/cloud-api/logstream"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestGetExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		errString     string
		expectedCode  int
		expectedError error
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

			if err != tt.expectedError {
				t.Errorf("getExitCode(%q) = %d, want %d", tt.errString, err, tt.expectedError)
			}
		})
	}
}

func TestDetermineFatalErrorType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		errString     string
		exitCode      int
		parseErr      error
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
