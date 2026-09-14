package layer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every field number this engine uses is the one the published schema gives.
//
// **Three were wrong in one day, and every test passed.** The goldens are
// generated from testdata/reapi/reapi_min.proto, which is this repository's own
// transcription: a vector built from a transcription proves that the encoder
// and the transcription agree, and cannot prove the transcription. So
// `execution_metadata` was written to 6 (which is `stdout_digest`), and
// `low_api_version`/`high_api_version` to 3 and 4 (which are
// `deprecated_api_version` and `low_api_version`) - telling every client this
// service was deprecated and named no high version at all.
//
// This reads the published schemas, vendored verbatim beside it, and checks the
// constants against them. It needs no protoc and no network: the failure it
// exists to catch is a number, and the number is in the file.
func TestEveryFieldNumberMatchesTheSchema(t *testing.T) {
	t.Parallel()

	schema := fieldsOfSchemas(t)

	// Every constant, against the message the comment beside it names. The
	// comment is load-bearing and that is the point: a constant whose comment
	// says which field it is can be checked, and one that does not cannot.
	for _, c := range []struct {
		constant, message, field string
		got                      int
	}{
		{"fieldFiles", "Directory", "files", fieldFiles},
		{"fieldDirectories", "Directory", "directories", fieldDirectories},
		{"fieldSymlinks", "Directory", "symlinks", fieldSymlinks},
		{"fieldName", "FileNode", "name", fieldName},
		{"fieldDigest", "FileNode", "digest", fieldDigest},
		{"fieldIsExecutable", "FileNode", "is_executable", fieldIsExecutable},
		{"fieldTarget", "SymlinkNode", "target", fieldTarget},
		{"fieldDigestHash", "Digest", "hash", fieldDigestHash},
		{"fieldDigestSize", "Digest", "size_bytes", fieldDigestSize},

		{"fieldArguments", "Command", "arguments", fieldArguments},
		{"fieldEnv", "Command", "environment_variables", fieldEnv},
		{"fieldOutputFilesOld", "Command", "output_files", fieldOutputFilesOld},
		{"fieldOutputDirsOld", "Command", "output_directories", fieldOutputDirsOld},
		{"fieldCommandPlatform", "Command", "platform", fieldCommandPlatform},
		{"fieldWorkingDir", "Command", "working_directory", fieldWorkingDir},
		{"fieldOutputs", "Command", "output_paths", fieldOutputs},

		{"fieldCommandDigest", "Action", "command_digest", fieldCommandDigest},
		{"fieldInputRoot", "Action", "input_root_digest", fieldInputRoot},
		{"fieldDoNotCache", "Action", "do_not_cache", fieldDoNotCache},
		{"fieldSalt", "Action", "salt", fieldSalt},
		{"fieldPlatform", "Action", "platform", fieldPlatform},
		{"fieldPlatformProps", "Platform", "properties", fieldPlatformProps},

		{"fieldOutputFiles", "ActionResult", "output_files", fieldOutputFiles},
		{"fieldOutputDirs", "ActionResult", "output_directories", fieldOutputDirs},
		{"fieldExitCode", "ActionResult", "exit_code", fieldExitCode},
		{"fieldStdoutRaw", "ActionResult", "stdout_raw", fieldStdoutRaw},
		{"fieldExecMetadata", "ActionResult", "execution_metadata", fieldExecMetadata},
		{"fieldOutFilePath", "OutputFile", "path", fieldOutFilePath},
		{"fieldOutFileDgst", "OutputFile", "digest", fieldOutFileDgst},
		{"fieldOutFileExec", "OutputFile", "is_executable", fieldOutFileExec},
		{"fieldOutDirPath", "OutputDirectory", "path", fieldOutDirPath},
		{"fieldOutDirTree", "OutputDirectory", "tree_digest", fieldOutDirTree},
		{"fieldOutDirRoot", "OutputDirectory", "root_directory_digest", fieldOutDirRoot},
		{"fieldTreeRoot", "Tree", "root", fieldTreeRoot},
		{"fieldTreeChildren", "Tree", "children", fieldTreeChildren},

		{"fieldMetaWorker", "ExecutedActionMetadata", "worker", fieldMetaWorker},
		{"fieldMetaStarted", "ExecutedActionMetadata", "worker_start_timestamp", fieldMetaStarted},
		{"fieldMetaCompleted", "ExecutedActionMetadata", "worker_completed_timestamp", fieldMetaCompleted},

		{"fieldCacheCaps", "ServerCapabilities", "cache_capabilities", fieldCacheCaps},
		{"fieldExecCaps", "ServerCapabilities", "execution_capabilities", fieldExecCaps},
		{"fieldLowAPI", "ServerCapabilities", "low_api_version", fieldLowAPI},
		{"fieldHighAPI", "ServerCapabilities", "high_api_version", fieldHighAPI},
		{"fieldDigestFuncs", "CacheCapabilities", "digest_functions", fieldDigestFuncs},
		{"fieldMaxBatchSize", "CacheCapabilities", "max_batch_total_size_bytes", fieldMaxBatchSize},
		{"fieldExecDigestFunc", "ExecutionCapabilities", "digest_function", fieldExecDigestFunc},
		{"fieldExecEnabled", "ExecutionCapabilities", "exec_enabled", fieldExecEnabled},
		{"fieldExecDigestFns", "ExecutionCapabilities", "digest_functions", fieldExecDigestFns},
		{"fieldSemVerMajor", "SemVer", "major", fieldSemVerMajor},
		{"fieldSemVerMinor", "SemVer", "minor", fieldSemVerMinor},

		{"fieldExecActionDgst", "ExecuteRequest", "action_digest", fieldExecActionDgst},
		{"fieldSkipCacheLookup", "ExecuteRequest", "skip_cache_lookup", fieldSkipCacheLookup},
		{"fieldExecStage", "ExecuteOperationMetadata", "stage", fieldExecStage},
		{"fieldExecMetaDigest", "ExecuteOperationMetadata", "action_digest", fieldExecMetaDigest},

		{"fieldBlobDigests", "FindMissingBlobsRequest", "blob_digests", fieldBlobDigests},
		{"fieldMissingBlobs", "FindMissingBlobsResponse", "missing_blob_digests", fieldMissingBlobs},

		{"fieldStreamResource", "ReadRequest", "resource_name", fieldStreamResource},
		{"fieldReadOffset", "ReadRequest", "read_offset", fieldReadOffset},
		{"fieldReadLimit", "ReadRequest", "read_limit", fieldReadLimit},
		{"fieldStreamData", "ReadResponse", "data", fieldStreamData},
		{"fieldWriteOffset", "WriteRequest", "write_offset", fieldWriteOffset},
		{"fieldFinishWrite", "WriteRequest", "finish_write", fieldFinishWrite},
		{"fieldCommittedSize", "WriteResponse", "committed_size", fieldCommittedSize},
		{"fieldWriteComplete", "QueryWriteStatusResponse", "complete", fieldWriteComplete},
	} {
		want, ok := schema[c.message+"."+c.field]
		if !ok {
			t.Errorf("%s says it is %s.%s, and the schema has no such field"+
				"\n  either the name is wrong or the schema moved under it",
				c.constant, c.message, c.field)

			continue
		}

		if c.got != want {
			t.Errorf("%s is %d and %s.%s is field %d in the schema",
				c.constant, c.got, c.message, c.field, want)
		}
	}
}

// fieldsOfSchemas reads every `Type name = N;` out of the vendored protos.
//
// A parser for the shape this needs and no more: message bodies, one field per
// line, which is how these files are written. Nested messages are flattened to
// their own name, because that is how they are referred to here.
func fieldsOfSchemas(t *testing.T) map[string]int {
	t.Helper()

	var (
		message = regexp.MustCompile(`^\s*message\s+(\w+)\s*\{`)
		field   = regexp.MustCompile(`^\s*(?:repeated\s+)?[\w.]+\s+(\w+)\s*=\s*(\d+)\s*[;\[]`)
		out     = map[string]int{}
	)

	names, err := filepath.Glob("testdata/schema/*.proto")
	if err != nil || len(names) == 0 {
		t.Fatalf("no vendored schema to check against: %v", err)
	}

	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}

		var stack []string

		for _, line := range strings.Split(string(b), "\n") {
			if m := message.FindStringSubmatch(line); m != nil {
				stack = append(stack, m[1])

				continue
			}

			if strings.TrimSpace(line) == "}" && len(stack) > 0 {
				stack = stack[:len(stack)-1]

				continue
			}

			if len(stack) == 0 {
				continue
			}

			if m := field.FindStringSubmatch(line); m != nil {
				n, convErr := strconv.Atoi(m[2])
				if convErr != nil {
					continue
				}

				out[fmt.Sprintf("%s.%s", stack[len(stack)-1], m[1])] = n
			}
		}
	}

	return out
}
