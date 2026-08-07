package dockerfile_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/dockerfile"
)

const testImageTag = "my-app:v1.0.0"

func TestConvert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		dfContent string
		imageTag  string
		want      string
	}{
		{
			name: "single stage puts everything directly into build target",
			dfContent: `FROM alpine:3.18
RUN echo hello
`,
			imageTag: "my-image:latest",
			want: `
VERSION 0.8

# This Earthfile was converted from a Dockerfile using 'earth docker-build'.
# Conversion is done on a best-effort basis and might not follow all
# best practices. Please visit https://docs.earthbuild.dev for guides.

# build builds the final image
build:
	FROM alpine:3.18
	RUN echo hello
	SAVE IMAGE my-image:latest
`,
		},
		{
			name: "multi stage with named AS stages preserves stage names and creates build entrypoint wrapper",
			dfContent: `FROM golang:1.26 AS builder
RUN go build -o /app .

FROM alpine:3.18 AS runner
COPY --from=builder /app /app
`,
			imageTag: testImageTag,
			want: `
VERSION 0.8

# This Earthfile was converted from a Dockerfile using 'earth docker-build'.
# Conversion is done on a best-effort basis and might not follow all
# best practices. Please visit https://docs.earthbuild.dev for guides.

builder:
	FROM golang:1.26
	RUN go build -o /app .
	SAVE ARTIFACT /app app

runner:
	FROM alpine:3.18
	COPY +builder/app /app
	SAVE IMAGE my-app:v1.0.0

# build is the default entrypoint
build:
	BUILD +runner
`,
		},
		{
			name: "multi stage unnamed uses stage-1 target for intermediate and build target for final stage",
			dfContent: `FROM golang:1.26
RUN go build -o /app .

FROM alpine:3.18
COPY --from=0 /app /app
`,
			imageTag: testImageTag,
			want: `
VERSION 0.8

# This Earthfile was converted from a Dockerfile using 'earth docker-build'.
# Conversion is done on a best-effort basis and might not follow all
# best practices. Please visit https://docs.earthbuild.dev for guides.

stage-1:
	FROM golang:1.26
	RUN go build -o /app .
	SAVE ARTIFACT /app app

# build builds the final image
build:
	FROM alpine:3.18
	COPY +stage-1/app /app
	SAVE IMAGE my-app:v1.0.0
`,
		},
		{
			name: "multi stage where intermediate stage is named build preserves build and runner target names",
			dfContent: `FROM golang:1.26 AS build
RUN go build -o /app .

FROM alpine:3.18 AS runner
COPY --from=build /app /app
`,
			imageTag: testImageTag,
			want: `
VERSION 0.8

# This Earthfile was converted from a Dockerfile using 'earth docker-build'.
# Conversion is done on a best-effort basis and might not follow all
# best practices. Please visit https://docs.earthbuild.dev for guides.

build:
	FROM golang:1.26
	RUN go build -o /app .
	SAVE ARTIFACT /app app

runner:
	FROM alpine:3.18
	COPY +build/app /app
	SAVE IMAGE my-app:v1.0.0
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			_, err := dockerfile.Convert(&buf, strings.NewReader(tt.dfContent), "earth docker-build", tt.imageTag, "")
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}

			if got := buf.String(); got != tt.want {
				t.Errorf("Convert output mismatch.\nGOT:\n%s\nWANT:\n%s", got, tt.want)
			}
		})
	}
}
