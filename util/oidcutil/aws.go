package oidcutil

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/util/parseutil"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
)

const iam = "iam"

// AWSOIDCInfo contains AWS OIDC authentication information.
type AWSOIDCInfo struct {
	RoleARN         *arn.ARN
	SessionDuration *time.Duration
	SessionName     string
	Region          string
}

var requiredFields = []string{"role-arn", "session-name"}

func (oi *AWSOIDCInfo) String() string {
	if oi == nil {
		return ""
	}

	var sb strings.Builder
	sb.Grow(128)

	if oi.SessionName != "" {
		sb.WriteString("session-name=")
		sb.WriteString(oi.SessionName)
	}

	if arnStr := oi.RoleARNString(); arnStr != "" {
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}

		sb.WriteString("role-arn=")
		sb.WriteString(arnStr)
	}

	if oi.Region != "" {
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}

		sb.WriteString("region=")
		sb.WriteString(oi.Region)
	}

	if oi.SessionDuration != nil {
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}

		sb.WriteString("session-duration=")
		sb.WriteString(oi.SessionDuration.String())
	}

	return sb.String()
}

// RoleARNString returns the role ARN as a string.
func (oi *AWSOIDCInfo) RoleARNString() string {
	if oi != nil && oi.RoleARN != nil {
		return oi.RoleARN.String()
	}

	return ""
}

// ParseAWSOIDCInfo takes a string that represents a list of oidc key/value pairs and returns it
// in the form of a *AWSOIDCInfo. The function errors if the string is invalid, including unexpected keys and/or values.
func ParseAWSOIDCInfo(oidcInfo string) (*AWSOIDCInfo, error) {
	m, err := parseutil.StringToMap(oidcInfo)
	if err != nil {
		return nil, fmt.Errorf("oidc info is invalid: %w", err)
	}

	info := &AWSOIDCInfo{}

	// 1. Parse/validate role-arn
	roleARNVal, ok := m["role-arn"]
	if ok && strings.TrimSpace(roleARNVal) != "" {
		parsedARN, err := arn.Parse(roleARNVal)
		if err != nil {
			return nil, fmt.Errorf("error decoding 'role-arn': %w", err)
		}

		if parsedARN.Service != iam {
			err := fmt.Errorf(
				`error decoding 'role-arn': aws service ("%s") must be "%s"`,
				parsedARN.Service,
				iam,
			)

			return nil, err
		}

		if !strings.HasPrefix(parsedARN.Resource, "role/") {
			err := fmt.Errorf(
				`error decoding 'role-arn': resource ("%s") must be a role`,
				parsedARN.Resource,
			)

			return nil, err
		}

		info.RoleARN = &parsedARN
	}

	// 2. Parse/validate session-duration
	sessionDurationVal, ok := m["session-duration"]
	if ok && strings.TrimSpace(sessionDurationVal) != "" {
		parsedDur, err := time.ParseDuration(sessionDurationVal)
		if err != nil {
			return nil, fmt.Errorf("error decoding 'session-duration': %w", err)
		}

		if parsedDur.Seconds() < 900 || parsedDur.Seconds() > 43200 {
			err := errors.New(
				"error decoding 'session-duration': duration must be between 900s and 43200s",
			)

			return nil, err
		}

		info.SessionDuration = &parsedDur
	}

	// 3. Assign session-name
	if sessionNameVal, ok := m["session-name"]; ok {
		info.SessionName = sessionNameVal
	}

	// 4. Assign region
	if regionVal, ok := m["region"]; ok {
		info.Region = regionVal
	}

	// 5. Check for unrecognized keys
	var invalidKeys []string

	for k := range m {
		switch k {
		case "role-arn", "session-duration", "session-name", "region":
			// valid keys
		default:
			invalidKeys = append(invalidKeys, k)
		}
	}

	if len(invalidKeys) > 0 {
		slices.Sort(invalidKeys)

		return nil, fmt.Errorf("key(s) [%s] are invalid", strings.Join(invalidKeys, ","))
	}

	// 6. Check required fields
	for _, f := range requiredFields {
		val, ok := m[f]
		if !ok || strings.TrimSpace(val) == "" {
			return nil, errors.New(f + " must be specified")
		}
	}

	return info, nil
}
