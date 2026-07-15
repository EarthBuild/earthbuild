package oidcutil

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAWSOIDCInfoString(t *testing.T) {
	t.Parallel()

	//nolint:goconst
	tests := map[string]struct {
		subject  *AWSOIDCInfo
		expected string
	}{
		"happy path - nil": {},
		"happy path - when everything is set": {
			subject: &AWSOIDCInfo{
				RoleARN: &arn.ARN{
					Service:  iam,
					Region:   "us-east-1",
					Resource: "role/123",
				},
				Region:          "us-west-2",
				SessionDuration: aws.Duration(time.Second),
				SessionName:     "my-session",
			},
			expected: "session-name=my-session,role-arn=arn::iam:us-east-1::role/123,region=us-west-2,session-duration=1s",
		},
		"happy path - no role-arn": {
			subject: &AWSOIDCInfo{
				Region:          "us-west-2",
				SessionDuration: aws.Duration(time.Second),
				SessionName:     "my-session",
			},
			expected: "session-name=my-session,region=us-west-2,session-duration=1s",
		},
		"happy path - no region": {
			subject: &AWSOIDCInfo{
				RoleARN: &arn.ARN{
					Service:  "iam",
					Region:   "us-east-1",
					Resource: "role/123",
				},
				SessionDuration: aws.Duration(time.Second),
				SessionName:     "my-session",
			},
			expected: "session-name=my-session,role-arn=arn::iam:us-east-1::role/123,session-duration=1s",
		},
		"happy path - no session duration": {
			subject: &AWSOIDCInfo{
				RoleARN: &arn.ARN{
					Service:  "iam",
					Region:   "us-east-1",
					Resource: "role/123",
				},
				Region:      "us-west-2",
				SessionName: "my-session",
			},
			expected: "session-name=my-session,role-arn=arn::iam:us-east-1::role/123,region=us-west-2",
		},
		"happy path - no session name": {
			subject: &AWSOIDCInfo{
				RoleARN: &arn.ARN{
					Service:  "iam",
					Region:   "us-east-1",
					Resource: "role/123",
				},
				Region:          "us-west-2",
				SessionDuration: aws.Duration(time.Second),
			},
			expected: "role-arn=arn::iam:us-east-1::role/123,region=us-west-2,session-duration=1s",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := tc.subject.String()
			assert.Equal(t, tc.expected, res)
		})
	}
}

func TestAWSOIDCInfoRoleARNString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		subject  *AWSOIDCInfo
		expected string
	}{
		"struct is nil": {},
		"role arn is nil": {
			subject: &AWSOIDCInfo{},
		},
		"role arn is not nil": {
			subject: &AWSOIDCInfo{
				RoleARN: &arn.ARN{
					Service:  "iam",
					Region:   "us-east-1",
					Resource: "role/123",
				},
			},
			expected: "arn::iam:us-east-1::role/123",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := tc.subject.RoleARNString()
			assert.Equal(t, tc.expected, res)
		})
	}
}

func TestParseAWSOIDCInfo(t *testing.T) {
	t.Parallel()

	//nolint:goconst
	tests := map[string]struct {
		expectedErr error
		expected    *AWSOIDCInfo
		input       string
	}{
		"error when string is invalid": {
			input:       "invalid string",
			expectedErr: fmt.Errorf("oidc info is invalid: %w", errors.New("key/value must be set with =")),
		},
		"error when duration is invalid": {
			input:       "session-duration=invalid",
			expectedErr: errors.New(`error decoding 'session-duration': time: invalid duration "invalid"`),
		},
		"error when duration is less than 900s": {
			input:       "role-arn=arn::iam::123:role/456,session-duration=899s",
			expectedErr: errors.New(`error decoding 'session-duration': duration must be between 900s and 43200s`),
		},
		"error when duration is more than 43200s": {
			input:       "role-arn=arn::iam::123:role/456,session-duration=43201s",
			expectedErr: errors.New(`error decoding 'session-duration': duration must be between 900s and 43200s`),
		},
		"error when session name is missing": {
			input:       "role-arn=arn::iam::123:role/456,region=us-west-2,session-duration=900s",
			expectedErr: errors.New("session-name must be specified"),
		},
		"error when role arn is missing": {
			input:       "session-duration=902s",
			expectedErr: errors.New("role-arn must be specified"),
		},
		"error when role arn is empty": {
			input:       "role-arn= ,session-name=my-session",
			expectedErr: errors.New("role-arn must be specified"),
		},
		"error when role arn is invalid": {
			input:       "role-arn=invalid",
			expectedErr: fmt.Errorf("error decoding 'role-arn': %w", errors.New("arn: invalid prefix")),
		},
		"error when role arn is not iam service": {
			input:       "role-arn=arn::kinesis:us-east-1::role/123",
			expectedErr: errors.New(`error decoding 'role-arn': aws service ("kinesis") must be "iam"`),
		},
		"error when role arn resource is not a role": {
			input:       "role-arn=arn::iam:us-east-1::user/123",
			expectedErr: errors.New(`error decoding 'role-arn': resource ("user/123") must be a role`),
		},
		"error when using unrecognized keys": {
			input:       "role-arn=arn::iam:us-east-1::role/123,session-name=my-session,foo=bar",
			expectedErr: errors.New("key(s) [foo] are invalid"),
		},
		"happy path": {
			input: "role-arn=arn::iam::123:role/456,region=us-west-2,session-duration=900s,session-name=my-session",
			expected: &AWSOIDCInfo{
				RoleARN: &arn.ARN{
					Service:   "iam",
					AccountID: "123",
					Resource:  "role/456",
				},
				Region:          "us-west-2",
				SessionDuration: aws.Duration(time.Second * 900),
				SessionName:     "my-session",
			},
		},
		"happy path - no region": {
			input: "role-arn=arn::iam::123:role/456,session-duration=900s,session-name=my-session",
			expected: &AWSOIDCInfo{
				RoleARN: &arn.ARN{
					Service:   "iam",
					AccountID: "123",
					Resource:  "role/456",
				},
				SessionDuration: aws.Duration(time.Second * 900),
				SessionName:     "my-session",
			},
		},
		"happy path - no session duration": {
			input: "role-arn=arn::iam::123:role/456,region=us-west-2,session-name=my-session",
			expected: &AWSOIDCInfo{
				RoleARN: &arn.ARN{
					Service:   "iam",
					AccountID: "123",
					Resource:  "role/456",
				},
				Region:      "us-west-2",
				SessionName: "my-session",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res, err := ParseAWSOIDCInfo(tc.input)
			if tc.expectedErr != nil {
				require.EqualError(t, err, tc.expectedErr.Error())
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.expected, res)
		})
	}
}
