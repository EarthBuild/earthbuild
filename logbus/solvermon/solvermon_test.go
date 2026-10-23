package solvermon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logstream"
	"github.com/EarthBuild/earthbuild/util/statsstreamparser"
	"github.com/moby/buildkit/client"
	"github.com/opencontainers/go-digest"
)

func TestSolverMonitor_HandleBuildkitStatus_CredentialScrubbing(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		inputData  []byte
		wantPrefix []byte
		stream     int
	}{
		"stdout is scrubbed": {
			inputData:  []byte("curl http://user:secret@example.com/api"),
			wantPrefix: []byte("curl http://user:xxxxx@example.com/api"),
			stream:     1,
		},
		"stderr is scrubbed": {
			inputData:  []byte("error with http://admin:pass123@internal.net"),
			wantPrefix: []byte("error with http://admin:xxxxx@internal.net"),
			stream:     2,
		},
		"stats stream binary data is not scrubbed": {
			inputData:  []byte{0x01, 0x05, 0x00, 0x00, 0x00, '@', ' ', ':', '@'},
			wantPrefix: []byte{0x01, 0x05, 0x00, 0x00, 0x00, '@', ' ', ':', '@'},
			stream:     BuildkitStatsStream,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bus := logbus.New()

			cp, err := bus.Run().NewCommand(
				"cmd-"+name, "echo hello", "target-"+name, "cmd", "linux/amd64",
				false, false, false, earthfile.SourceLocation{}, "", "", "",
			)
			if err != nil {
				t.Fatalf("failed to create command: %v", err)
			}

			sm := New(bus, false)
			vDigest := digest.FromString(name)
			cmdID := "cmd-" + name

			sm.digests[vDigest] = cmdID
			sm.vertices[cmdID] = &vertexMonitor{
				cp:  cp,
				ssp: statsstreamparser.New(),
			}

			dataCopy := make([]byte, len(tt.inputData))
			copy(dataCopy, tt.inputData)

			status := &client.SolveStatus{
				Logs: []*client.VertexLog{
					{
						Vertex: vDigest,
						Stream: tt.stream,
						Data:   dataCopy,
					},
				},
			}

			err = sm.handleBuildkitStatus(status)
			if err != nil {
				t.Fatalf("handleBuildkitStatus returned error: %v", err)
			}

			got := status.Logs[0].Data
			if !bytes.Equal(got, tt.wantPrefix) {
				t.Errorf("logLine.Data = %v, want %v", got, tt.wantPrefix)
			}
		})
	}
}

func TestSolverMonitor_HandleBuildkitStatus_StatsStream_NonFatalOnError(t *testing.T) {
	t.Parallel()

	bus := logbus.New()

	cp, err := bus.Run().NewCommand(
		"cmd-resilient", "echo hello", "target-resilient", "cmd", "linux/amd64",
		false, false, false, earthfile.SourceLocation{}, "", "", "",
	)
	if err != nil {
		t.Fatalf("failed to create command: %v", err)
	}

	sm := New(bus, false)
	vDigest := digest.FromString("resilient-vertex")
	cmdID := "cmd-resilient"

	sm.digests[vDigest] = cmdID
	sm.vertices[cmdID] = &vertexMonitor{
		cp:  cp,
		ssp: statsstreamparser.New(),
	}

	// 1. Send corrupted stats packet that triggers protocol version mismatch (e.g. JSON starting with '{')
	corruptedStatus := &client.SolveStatus{
		Logs: []*client.VertexLog{
			{
				Vertex: vDigest,
				Stream: BuildkitStatsStream,
				Data:   []byte(`{"cpu":{"usage":{"total":999}}}`),
			},
		},
	}

	err = sm.handleBuildkitStatus(corruptedStatus)
	if err != nil {
		t.Fatalf("handleBuildkitStatus returned error %v on corrupted stats, want nil (non-fatal)", err)
	}

	// 2. Following corrupted packet, verify solver monitor still processes a valid stats packet cleanly
	validPayload := `{"cpu":{"usage":{"total":1000}}}`
	buf := make([]byte, 1+4+len(validPayload))
	buf[0] = 1
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(validPayload))) // #nosec G115
	copy(buf[5:], validPayload)

	validStatus := &client.SolveStatus{
		Logs: []*client.VertexLog{
			{
				Vertex: vDigest,
				Stream: BuildkitStatsStream,
				Data:   buf,
			},
		},
	}

	err = sm.handleBuildkitStatus(validStatus)
	if err != nil {
		t.Fatalf("handleBuildkitStatus returned error %v on valid stats after corruption, want nil", err)
	}
}

func TestSolverMonitor_HandleBuildkitStatus_MixedStreams(t *testing.T) {
	t.Parallel()

	bus := logbus.New()

	cp, err := bus.Run().NewCommand(
		"cmd-mixed", "echo hello", "target-mixed", "cmd", "linux/amd64",
		false, false, false, earthfile.SourceLocation{}, "", "", "",
	)
	if err != nil {
		t.Fatalf("failed to create command: %v", err)
	}

	sm := New(bus, false)
	vDigest := digest.FromString("mixed-vertex")
	cmdID := "cmd-mixed"

	sm.digests[vDigest] = cmdID
	sm.vertices[cmdID] = &vertexMonitor{
		cp:  cp,
		ssp: statsstreamparser.New(),
	}

	statsPayload := `{"cpu":{"usage":{"total":42}}}`
	statsBuf := make([]byte, 1+4+len(statsPayload))
	statsBuf[0] = 1
	binary.LittleEndian.PutUint32(statsBuf[1:5], uint32(len(statsPayload))) // #nosec G115
	copy(statsBuf[5:], statsPayload)

	status := &client.SolveStatus{
		Logs: []*client.VertexLog{
			{
				Vertex: vDigest,
				Stream: 1, // stdout
				Data:   []byte("curl http://user:secret@example.com/api"),
			},
			{
				Vertex: vDigest,
				Stream: BuildkitStatsStream,
				Data:   statsBuf,
			},
			{
				Vertex: vDigest,
				Stream: 2, // stderr
				Data:   []byte("err from http://admin:pass@internal.net"),
			},
		},
	}

	err = sm.handleBuildkitStatus(status)
	if err != nil {
		t.Fatalf("handleBuildkitStatus returned error: %v", err)
	}

	wantStdout := []byte("curl http://user:xxxxx@example.com/api")
	if got := status.Logs[0].Data; !bytes.Equal(got, wantStdout) {
		t.Errorf("stdout = %s, want %s", got, wantStdout)
	}

	if got := status.Logs[1].Data; !bytes.Equal(got, statsBuf) {
		t.Errorf("stats stream modified: got %v, want %v", got, statsBuf)
	}

	wantStderr := []byte("err from http://admin:xxxxx@internal.net")
	if got := status.Logs[2].Data; !bytes.Equal(got, wantStderr) {
		t.Errorf("stderr = %s, want %s", got, wantStderr)
	}
}

type genericLogCollector struct {
	msgs []string
}

func (c *genericLogCollector) Write(d *logstream.Delta) {
	dl := d.GetDeltaLog()
	if dl != nil && dl.GetCommandId() == logbus.GenericDefault {
		c.msgs = append(c.msgs, string(dl.GetData()))
	}
}

func TestSolverMonitor_HandleBuildkitStatus_StatsDecodeErrorDebug(t *testing.T) {
	t.Parallel()

	const echoOp = "RUN echo hello"

	tests := []struct {
		err        error
		name       string
		vertexName string
		wantSubstr string
		wantCount  int
		debug      bool
	}{
		{
			name:       "debug false drops malformed stats silently",
			vertexName: echoOp,
			wantCount:  0,
			debug:      false,
		},
		{
			name:       "debug true reports malformed stats once per vertex",
			vertexName: echoOp,
			wantSubstr: `dropping malformed runc stats for "` + echoOp + `":`,
			wantCount:  1,
			debug:      true,
		},
		{ // #nosec G101 -- false positive: synthetic credentials used to test credential scrubbing
			name:       "debug true scrubs credentials in vertex operation",
			vertexName: "RUN curl https://user:secret@example.com/api",
			wantSubstr: `dropping malformed runc stats for "RUN curl https://user:xxxxx@example.com/api":`,
			wantCount:  1,
			debug:      true,
		},
		{ // #nosec G101 -- false positive: synthetic credentials used to test credential scrubbing
			name:       "debug true scrubs credentials in decode error message",
			vertexName: echoOp,
			err:        errors.New("failed decoding stats from http://admin:secretpass@cluster.internal"),
			wantSubstr: "http://admin:xxxxx@cluster.internal",
			wantCount:  1,
			debug:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bus := logbus.New()
			collector := &genericLogCollector{}
			bus.AddRawSubscriber(collector)

			sm := New(bus, tt.debug)

			if tt.err != nil {
				sm.reportStatsDecodeError(tt.vertexName)(tt.err)
			} else {
				vDigest := digest.FromString("stats-debug-vertex")
				corrupted := []byte(`{"cpu":{"usage":{"total":100}}}`)

				status := &client.SolveStatus{
					Vertexes: []*client.Vertex{
						{
							Digest: vDigest,
							Name:   tt.vertexName,
						},
					},
					Logs: []*client.VertexLog{
						{
							Vertex: vDigest,
							Stream: BuildkitStatsStream,
							Data:   corrupted,
						},
						{
							Vertex: vDigest,
							Stream: BuildkitStatsStream,
							Data:   corrupted,
						},
					},
				}

				err := sm.handleBuildkitStatus(status)
				if err != nil {
					t.Fatalf("handleBuildkitStatus returned error %v, want nil", err)
				}
			}

			if len(collector.msgs) != tt.wantCount {
				t.Fatalf("generic log count = %d, want %d (msgs: %v)", len(collector.msgs), tt.wantCount, collector.msgs)
			}

			if tt.wantCount > 0 {
				if !strings.Contains(collector.msgs[0], tt.wantSubstr) {
					t.Errorf("generic log = %q, want substring %q", collector.msgs[0], tt.wantSubstr)
				}

				if strings.Contains(collector.msgs[0], "secretpass") {
					t.Errorf("generic log contains unscrubbed credential: %q", collector.msgs[0])
				}
			}
		})
	}
}
