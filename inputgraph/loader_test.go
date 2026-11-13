package inputgraph

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/util/buildkitskipper/hasher"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_containsShellExpr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc string
		val  string
		want bool
	}{
		{
			desc: "single",
			val:  "$(echo 'hello')",
			want: true,
		},
		{
			desc: "nested",
			val:  "$(echo -n $(cat /tmp/x))",
			want: true,
		},
		{
			desc: "invalid 1",
			val:  "$($()",
			want: false,
		},
		{
			desc: "invalid 2",
			val:  ")$(",
			want: false,
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			t.Parallel()

			got := containsShellExpr(c.val)
			require.Equal(t, c.want, got)
		})
	}
}

func Test_evalConditions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		val  string
		want [2]bool
	}{
		{
			val:  `[ -f "file" ]`,
			want: [2]bool{false, false},
		},
		{
			val:  "[ true ]",
			want: [2]bool{true, true},
		},
		{
			val:  "[ false ]",
			want: [2]bool{false, true},
		},
		{
			val:  `[ -n "foo" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ -n "" ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ -z "" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ -z "foo" ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ "foo" == "bar" ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ "foo" = "bar" ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ "foo" != "bar" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ "foo" > "bar" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ "foo" < "bar" ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ "2" -eq "2" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ "2" -eq "4" ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ "2" -ne "4" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ "2" -gt "1" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ "2" -gt "10" ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ "2" -gt "foo" ]`,
			want: [2]bool{false, false},
		},
		{
			val:  `[ "2" -gt "foo" ]`,
			want: [2]bool{false, false},
		},
		{
			val:  `[ "2" -lt "100" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ "2" -lt "1" ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ "2" -le "2" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ "4" -le "2" ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ true ] && [ false ]`,
			want: [2]bool{false, true},
		},
		{
			val:  `[ true ] && [ -z "" ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ true ] && [ -z "" ] || [ false ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ 2 -gt 1 ] || [ "foo" == "bar" ] && [ false ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ 2 -gt 0 ] || [ "foo" == "foo" ] && [ true ]`,
			want: [2]bool{true, true},
		},
		{
			val:  `[ 2 -lt 0 ] && [ "foo" == "foo" ] || [ true ]`,
			want: [2]bool{true, true},
		},
	}

	for _, c := range cases {
		t.Run(c.val, func(t *testing.T) {
			t.Parallel()

			v := strings.Fields(c.val)
			got, ok := evalConditions(v)
			require.Equal(t, c.want, [2]bool{got, ok})
		})
	}
}

func Test_handleCopy_Heredoc(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr string
		cmd     earthfile.Command
	}{
		{
			name: "single heredoc source skips disk stat",
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"<<EOF", "/dest/path"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "echo hello\n"},
				},
			},
		},
		{
			name: "multiple chained heredoc sources skip disk stat",
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"<<app.conf", "<<'init.sh'", "/etc/"},
				Heredocs: []earthfile.Heredoc{
					{Name: "app.conf", Content: "key=v1\n"},
					{Name: "init.sh", Content: "#!/bin/sh\n"},
				},
			},
		},
		{
			name: "if-exists chomped heredoc source skips disk stat",
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"--if-exists", "<<-EOF", "/dest/path"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "\thi\n", Chomp: true},
				},
			},
		},
		{
			name:    "non-heredoc <<<missing source stats disk and returns error",
			wantErr: "failed to stat file",
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"<<<missing", "/dest/path"},
			},
		},
		{
			name:    "COPY --from rejected even with heredoc",
			wantErr: "COPY --from is not supported",
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"--from=alpine", "<<EOF", "/dest/path"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "echo hello\n"},
				},
			},
		},
		{
			name:    "missing destination rejected",
			wantErr: "COPY must include a source and destination",
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target := domain.Target{
				LocalPath: t.TempDir(),
				Target:    "build",
			}
			l := &loader{
				target: target,
				hasher: hasher.New(),
				varCollection: variables.NewCollection(variables.NewCollectionOpt{
					Target:   target,
					Features: &features.Features{},
				}),
			}

			err := l.handleCopy(t.Context(), tt.cmd)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
		})
	}
}

func hashEarthfileTarget(t *testing.T, dir string) string {
	t.Helper()

	cons := conslogging.New(os.Stderr, &sync.Mutex{}, 0, conslogging.Info, false)
	hash, _, err := HashTarget(t.Context(), HashOpt{
		Log: cons,
		Target: domain.Target{
			LocalPath: dir,
			Target:    "build",
		},
	})
	require.NoError(t, err)

	encoded := hex.EncodeToString(hash)
	require.NotEmpty(t, encoded)

	return encoded
}

func hashSingleCommand(c earthfile.Command) string {
	l := &loader{hasher: hasher.New()}
	l.hashCommand(c)

	return hex.EncodeToString(l.hasher.GetHash())
}

func TestHashCommand_HeredocInvariants(t *testing.T) {
	t.Parallel()

	nonHeredocCmd := earthfile.Command{
		Name:     earthfile.CmdRun,
		Args:     []string{"echo", "hello"},
		ExecMode: false,
	}

	baseline := hasher.New()
	baseline.HashString(string(nonHeredocCmd.Name))
	baseline.HashJSONMarshalled(nonHeredocCmd.Args)
	baseline.HashBool(nonHeredocCmd.ExecMode)
	wantLegacyNonHeredocHash := hex.EncodeToString(baseline.GetHash())

	baseRun := earthfile.Command{
		Name: earthfile.CmdRun,
		Args: []string{"<<EOF"},
		Heredocs: []earthfile.Heredoc{
			{
				Name:    "EOF",
				Content: "echo v1\n",
				Chomp:   false,
				Expand:  true,
			},
		},
	}
	baseCopy := earthfile.Command{
		Name: earthfile.CmdCopy,
		Args: []string{"<<app.conf", "/etc/app.conf"},
		Heredocs: []earthfile.Heredoc{
			{
				Name:    "app.conf",
				Content: "key=v1\n",
				Chomp:   false,
				Expand:  true,
			},
		},
	}

	baseRunHash := hashSingleCommand(baseRun)
	baseCopyHash := hashSingleCommand(baseCopy)

	tests := []struct {
		name      string
		wantHash  string
		cmd       earthfile.Command
		wantEqual bool
	}{
		{
			name:      "non-heredoc command preserves legacy hash",
			wantHash:  wantLegacyNonHeredocHash,
			cmd:       nonHeredocCmd,
			wantEqual: true,
		},
		{
			name:      "heredoc SourceLocation shift preserves hash",
			wantHash:  baseRunHash,
			wantEqual: true,
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF"},
				SourceLocation: earthfile.SourceLocation{
					File:        "Earthfile",
					StartLine:   42,
					StartColumn: 5,
				},
				Heredocs: []earthfile.Heredoc{
					{
						Name:    "EOF",
						Content: "echo v1\n",
						Chomp:   false,
						Expand:  true,
						SourceLocation: earthfile.SourceLocation{
							File:        "Earthfile",
							StartLine:   43,
							StartColumn: 1,
						},
					},
				},
			},
		},
		{
			name:      "RUN content mutation changes hash",
			wantHash:  baseRunHash,
			wantEqual: false,
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "echo v2\n", Chomp: false, Expand: true},
				},
			},
		},
		{
			name:      "RUN name mutation changes hash",
			wantHash:  baseRunHash,
			wantEqual: false,
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "END", Content: "echo v1\n", Chomp: false, Expand: true},
				},
			},
		},
		{
			name:      "RUN chomp mutation changes hash",
			wantHash:  baseRunHash,
			wantEqual: false,
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "echo v1\n", Chomp: true, Expand: true},
				},
			},
		},
		{
			name:      "RUN expand mutation changes hash",
			wantHash:  baseRunHash,
			wantEqual: false,
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "echo v1\n", Chomp: false, Expand: false},
				},
			},
		},
		{
			name:      "COPY content mutation changes hash",
			wantHash:  baseCopyHash,
			wantEqual: false,
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"<<app.conf", "/etc/app.conf"},
				Heredocs: []earthfile.Heredoc{
					{Name: "app.conf", Content: "key=v2\n", Chomp: false, Expand: true},
				},
			},
		},
		{
			name:      "COPY name mutation changes hash",
			wantHash:  baseCopyHash,
			wantEqual: false,
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"<<app.conf", "/etc/app.conf"},
				Heredocs: []earthfile.Heredoc{
					{Name: "other.conf", Content: "key=v1\n", Chomp: false, Expand: true},
				},
			},
		},
		{
			name:      "COPY chomp mutation changes hash",
			wantHash:  baseCopyHash,
			wantEqual: false,
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"<<app.conf", "/etc/app.conf"},
				Heredocs: []earthfile.Heredoc{
					{Name: "app.conf", Content: "key=v1\n", Chomp: true, Expand: true},
				},
			},
		},
		{
			name:      "COPY expand mutation changes hash",
			wantHash:  baseCopyHash,
			wantEqual: false,
			cmd: earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: []string{"<<app.conf", "/etc/app.conf"},
				Heredocs: []earthfile.Heredoc{
					{Name: "app.conf", Content: "key=v1\n", Chomp: false, Expand: false},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := hashSingleCommand(tt.cmd)
			if tt.wantEqual {
				assert.Equal(t, tt.wantHash, got)
				return
			}

			assert.NotEqual(t, tt.wantHash, got)
		})
	}
}

type targetFixture struct {
	files     map[string]string
	earthfile string
}

func writeAndHashTarget(t *testing.T, fx targetFixture) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Earthfile"), []byte(fx.earthfile), 0o600))

	for relPath, content := range fx.files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, relPath), []byte(content), 0o600))
	}

	return hashEarthfileTarget(t, dir)
}

func TestHashTarget_Heredoc(t *testing.T) {
	t.Parallel()

	const baseHeredocEarthfile = `VERSION 0.8
FROM alpine:3.18

build:
    RUN <<EOF
echo one
EOF
    COPY <<app.conf /etc/app.conf
port=8080
app.conf
`

	const shiftedHeredocEarthfile = `# top-level comment

# another comment shifting every line number
VERSION 0.8
FROM alpine:3.18


# comment right above target
build:
    # comment inside target
    RUN <<EOF
echo one
EOF
    COPY <<app.conf /etc/app.conf
port=8080
app.conf
`

	const localTripleAngleEarthfile = `VERSION 0.8
FROM alpine:3.18

build:
    COPY <<<localfile /dest
`

	tests := []struct {
		name         string
		before       targetFixture
		after        targetFixture
		wantSameHash bool
	}{
		{
			name:         "comments and blank lines shifting SourceLocation preserve target hash",
			before:       targetFixture{earthfile: baseHeredocEarthfile},
			after:        targetFixture{earthfile: shiftedHeredocEarthfile},
			wantSameHash: true,
		},
		{
			name:         "editing RUN heredoc body changes target hash",
			before:       targetFixture{earthfile: baseHeredocEarthfile},
			after:        targetFixture{earthfile: strings.Replace(baseHeredocEarthfile, "echo one", "echo two", 1)},
			wantSameHash: false,
		},
		{
			name:         "editing COPY heredoc body changes target hash",
			before:       targetFixture{earthfile: baseHeredocEarthfile},
			after:        targetFixture{earthfile: strings.Replace(baseHeredocEarthfile, "port=8080", "port=9090", 1)},
			wantSameHash: false,
		},
		{
			name:   "quoting COPY heredoc delimiter changes Expand and target hash",
			before: targetFixture{earthfile: baseHeredocEarthfile},
			after: targetFixture{
				earthfile: strings.Replace(baseHeredocEarthfile, "<<app.conf", "<<'app.conf'", 1),
			},
			wantSameHash: false,
		},
		{
			name: "unchanged COPY <<<localfile disk content preserves target hash",
			before: targetFixture{
				earthfile: localTripleAngleEarthfile,
				files:     map[string]string{"<<<localfile": "same-bytes\n"},
			},
			after: targetFixture{
				earthfile: localTripleAngleEarthfile,
				files:     map[string]string{"<<<localfile": "same-bytes\n"},
			},
			wantSameHash: true,
		},
		{
			name: "modifying COPY <<<localfile disk content changes target hash",
			before: targetFixture{
				earthfile: localTripleAngleEarthfile,
				files:     map[string]string{"<<<localfile": "initial-bytes\n"},
			},
			after: targetFixture{
				earthfile: localTripleAngleEarthfile,
				files:     map[string]string{"<<<localfile": "updated-bytes\n"},
			},
			wantSameHash: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			beforeHash := writeAndHashTarget(t, tt.before)
			afterHash := writeAndHashTarget(t, tt.after)
			assert.Equal(t, tt.wantSameHash, beforeHash == afterHash, "beforeHash=%s afterHash=%s", beforeHash, afterHash)
		})
	}
}
