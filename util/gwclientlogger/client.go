package gwclientlogger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/EarthBuild/earthbuild/util/stringutil"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	digest "github.com/opencontainers/go-digest"
)

type verboseClient struct {
	c gwclient.Client
}

// New returns a new gateway client that logs all calls to the wrapped client.
func New(c gwclient.Client) gwclient.Client {
	return &verboseClient{
		c: c,
	}
}

type safeAttestation struct {
	InToto any    `json:"inToto,omitempty"`
	Path   string `json:"path,omitempty"`
}

type safeResult struct {
	Ref          gwclient.Reference            `json:"ref,omitempty"`
	Refs         map[string]gwclient.Reference `json:"refs,omitempty"`
	Metadata     map[string][]byte             `json:"metadata,omitempty"`
	Attestations map[string][]safeAttestation  `json:"attestations,omitempty"`
}

// safeResultFrom converts a gwclient.Result into a JSON-serializable safeResult,
// omitting non-serializable fields such as Attestation.ContentFunc which cannot
// be marshaled by encoding/json (preventing Staticcheck SA1026).
func safeResultFrom(res *gwclient.Result) *safeResult {
	if res == nil {
		return nil
	}

	sr := &safeResult{
		Ref:      res.Ref,
		Refs:     res.Refs,
		Metadata: res.Metadata,
	}

	if len(res.Attestations) > 0 {
		sr.Attestations = make(map[string][]safeAttestation, len(res.Attestations))
		for k, atts := range res.Attestations {
			for _, a := range atts {
				sr.Attestations[k] = append(sr.Attestations[k], safeAttestation{
					InToto: a.InToto,
					Path:   a.Path,
				})
			}
		}
	}

	return sr
}

// Solve wraps gwclient.Solve.
func (vc *verboseClient) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	reqStr, _ := json.MarshalIndent(req, "", "\t")
	res, err := vc.c.Solve(ctx, req)
	resStr, _ := json.MarshalIndent(safeResultFrom(res), "", "\t")
	msg := fmt.Sprintf("Solve req=%s res=%s; err=%v\n", reqStr, resStr, err)
	fmt.Print(stringutil.ScrubCredentialsAll(msg))

	return res, err
}

// Export wraps gwclient.Export.
func (vc *verboseClient) Export(ctx context.Context, req gwclient.ExportRequest) error {
	reqStr, _ := json.MarshalIndent(req, "", "\t")
	err := vc.c.Export(ctx, req)
	msg := fmt.Sprintf("Export req=%s; err=%v\n", reqStr, err)
	fmt.Print(stringutil.ScrubCredentialsAll(msg))

	return err
}

// ResolveImageConfig wraps gwclient.ResolveImageConfig.
func (vc *verboseClient) ResolveImageConfig(
	ctx context.Context, ref string, opt llb.ResolveImageConfigOpt,
) (string, digest.Digest, []byte, error) {
	s, _ := json.MarshalIndent(opt, "", "\t")
	msg := fmt.Sprintf("ResolveImageConfig %s %s\n", ref, string(s))
	fmt.Print(stringutil.ScrubCredentialsAll(msg))

	return vc.c.ResolveImageConfig(ctx, ref, opt)
}

// BuildOpts wraps gwclient.BuildOpts.
func (vc *verboseClient) BuildOpts() gwclient.BuildOpts {
	opts := vc.c.BuildOpts()
	msg := fmt.Sprintf("BuildOpts res=%v\n", opts)
	fmt.Print(stringutil.ScrubCredentialsAll(msg))

	return opts
}

// Inputs wraps gwclient.Inputs.
func (vc *verboseClient) Inputs(ctx context.Context) (map[string]llb.State, error) {
	inputs, err := vc.c.Inputs(ctx)
	msg := fmt.Sprintf("Inputs=%v err=%v\n", inputs, err)
	fmt.Print(stringutil.ScrubCredentialsAll(msg))

	return inputs, err
}

// NewContainer wraps gwclient.NewContainer.
func (vc *verboseClient) NewContainer(
	ctx context.Context, req gwclient.NewContainerRequest,
) (gwclient.Container, error) {
	s, _ := json.MarshalIndent(req, "", "\t")
	container, err := vc.c.NewContainer(ctx, req)
	msg := fmt.Sprintf("NewContainer req=%s container=%v err=%v\n", string(s), container, err)
	fmt.Print(stringutil.ScrubCredentialsAll(msg))

	return container, err
}

// Warn wraps gwclient.Warn.
func (vc *verboseClient) Warn(ctx context.Context, dgst digest.Digest, msg string, warnOpts gwclient.WarnOpts) error {
	return vc.c.Warn(ctx, dgst, msg, warnOpts)
}
