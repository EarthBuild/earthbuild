package oidcutil

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/util/parseutil"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/mitchellh/mapstructure"
)

type AWSOIDCInfo struct {
	RoleARN         *arn.ARN       `mapstructure:"role-arn"`
	SessionDuration *time.Duration `mapstructure:"session-duration"`
	SessionName     string         `mapstructure:"session-name"`
	Region          string         `mapstructure:"region"`
}

var (
	requiredFields    = []string{"role-arn", "session-name"}
	decodeCFGTemplate = mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			timeDurationValidationsHookFunc(func(input time.Duration) error {
				if input.Seconds() < 900 || input.Seconds() > 43200 {
					return errors.New("duration must be between 900s and 43200s")
				}
				return nil
			}),
			stringToARN(func(input *arn.ARN) error {
				if input.Service != "iam" {
					return fmt.Errorf(`aws service ("%s") must be "iam"`, input.Service)
				}
				if !strings.HasPrefix(input.Resource, "role/") {
					return fmt.Errorf(`resource ("%s") must be an aws role"`, input.Resource)
				}
				return nil
			}),
		),
		WeaklyTypedInput: true,
	}
)

func newDecodeCFG(
	result any, metadata *mapstructure.Metadata, template mapstructure.DecoderConfig,
) *mapstructure.DecoderConfig {
	res := template
	res.Result = result
	res.Metadata = metadata

	return &res
}

func (oi *AWSOIDCInfo) String() string {
	if oi == nil {
		return ""
	}

	sb := strings.Builder{}
	if oi.SessionName != "" {
		sb.WriteString("session-name=" + oi.SessionName)
	}

	if oi.RoleARN != nil {
		sb.WriteString(",role-arn=" + oi.RoleARN.String())
	}

	if oi.Region != "" {
		sb.WriteString(",region=" + oi.Region)
	}

	if oi.SessionDuration != nil {
		sb.WriteString(",session-duration=" + oi.SessionDuration.String())
	}

	return strings.TrimPrefix(sb.String(), ",")
}

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
	metadata := &mapstructure.Metadata{}
	decodeCFG := newDecodeCFG(info, metadata, decodeCFGTemplate)
	decoder, _ := mapstructure.NewDecoder(decodeCFG)

	err = decoder.Decode(m)
	if err != nil {
		return nil, err
	}

	if len(metadata.Unused) > 0 {
		return nil, &mapstructure.Error{
			Errors: []string{fmt.Sprintf("key(s) [%s] are invalid", strings.Join(metadata.Unused, ","))},
		}
	}

	for _, f := range requiredFields {
		if slices.Contains(metadata.Unset, f) {
			return nil, &mapstructure.Error{Errors: []string{f + " must be specified"}}
		}
	}

	return info, nil
}

func stringToARN(validators ...func(input *arn.ARN) error) mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}

		if t != reflect.TypeFor[arn.ARN]() {
			return data, nil
		}

		res, err := arn.Parse(data.(string))
		if err != nil {
			return nil, err
		}

		for _, validator := range validators {
			err = validator(&res)
			if err != nil {
				return nil, err
			}
		}

		return &res, nil
	}
}

func timeDurationValidationsHookFunc(validators ...func(input time.Duration) error) mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}

		if t != reflect.TypeFor[time.Duration]() {
			return data, nil
		}

		// Convert it by parsing
		parsed, err := time.ParseDuration(data.(string))
		if err != nil {
			return nil, err
		}

		for _, validator := range validators {
			err := validator(parsed)
			if err != nil {
				return nil, err
			}
		}

		return parsed, nil
	}
}
