# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

EarthBuild is a community fork of Earthly, a versatile CI/CD framework that runs every pipeline inside containers. It provides repeatable builds using a syntax similar to Dockerfile and Makefile. This is a Go-based project that includes a CLI tool, buildkitd integration, and extensive testing infrastructure.

## Core Architecture

### Main Components

- **cmd/earthly** - Main CLI application entry point and command structure
- **earthfile2llb** - Core interpreter that converts Earthfile syntax to buildkit's LLB format
- **buildkitd** - Integration and configuration for buildkit daemon
- **ast** - Abstract Syntax Tree parsing for Earthfile language (separate Go module)
- **util** - Various utility packages (container utils, git utils, file utils, etc.)
- **domain** - Core domain models and business logic
- **states** - Build state management and caching logic
- **features** - Feature flag system for experimental functionality
- **conslogging** - Console logging and formatting system

### Key Subdirectories

- **tests/** - Extensive integration and end-to-end test suite
- **examples/** - Example projects in various languages (Go, Python, Java, Rust, etc.)
- **buildcontext/** - Build context resolution and git integration
- **dockertar/** - Docker image tar handling
- **regproxy/** - Registry proxy for image handling

## Development Commands

### Building the Project

**For current platform:**
```bash
./earthly +for-own
```

**Platform-specific builds:**
```bash
./earthly +for-linux        # Linux AMD64
./earthly +for-linux-arm64   # Linux ARM64
./earthly +for-darwin        # macOS AMD64
./earthly +for-darwin-m1     # macOS ARM64 (M1)
./earthly +for-windows       # Windows AMD64
```

**All platforms:**
```bash
./earthly +earthly-all
```

### Testing

**Unit tests:**
```bash
./earthly +unit-test
```

**Integration tests (requires Docker credentials):**
```bash
./earthly -P \
  --secret DOCKERHUB_USER=<username> \
  --secret DOCKERHUB_PASS=<password> \
  +test --DOCKERHUB_AUTH=true
```

**All tests including examples:**
```bash
./earthly -P \
  --secret DOCKERHUB_USER=<username> \
  --secret DOCKERHUB_PASS=<password> \
  +test-all --DOCKERHUB_AUTH=true
```

**Specific test groups:**
```bash
./earthly +test-no-qemu-group1  # Non-QEMU test group 1
./earthly +test-ast             # AST parser tests
./earthly +examples             # All examples
```

### Code Quality

**Linting:**
```bash
./earthly +lint                # Go linting with golangci-lint
./earthly +lint-scripts         # Shell script linting with shellcheck
./earthly +lint-all            # All linting checks
./earthly +lint-docs           # Documentation linting
./earthly +lint-changelog      # Changelog validation
```

**Code generation:**
```bash
./earthly +mocks              # Generate Go mocks
```

### Development Utilities

**Parser generation (for AST changes):**
```bash
./earthly ./ast/parser+parser
```

**Debugging build (with delve support):**
```bash
./earthly +for-own --GO_GCFLAGS='all=-N -l'
```

**Update dependencies:**
```bash
./earthly +deps               # Update Go dependencies
./earthly +npm-update-all     # Update all Node.js dependencies
```

## Testing Guidelines

### Test Structure

- **Unit tests** - Located alongside source code (`*_test.go`)
- **Integration tests** - In `tests/` directory with `.earth` and `.sh` files
- **Example tests** - Each example has its own test targets
- **AST tests** - In `ast/tests/` with JSON expected outputs

### Running Single Tests

```bash
./earthly ./tests+<test-name>              # Single integration test
./earthly +unit-test --testname=<name>     # Single unit test
./earthly ./examples/<lang>+docker         # Single example test
```

### Test Configuration

Tests can be configured with build args:
- `DOCKERHUB_AUTH=true` - Use Docker Hub authentication
- `DOCKERHUB_MIRROR=<url>` - Use Docker registry mirror
- `DOCKERHUB_MIRROR_AUTH=true` - Authenticate to registry mirror

## Go Module Structure

This is a multi-module repository:
- Main module: `github.com/EarthBuild/earthbuild`
- AST module: `github.com/EarthBuild/earthbuild/ast`
- Delta utility: `github.com/EarthBuild/earthbuild/util/deltautil`

The modules are decoupled - submodules cannot depend on the main module.

## Configuration

- Built binaries use `~/.earthly-dev/config.yml` by default
- Development can override with `EARTHLY_CONFIG` environment variable
- Buildkit additional config via `buildkit_additional_config` in config

## CI/CD Integration

The project includes extensive CI targets:
- `+prerelease` - Build prerelease versions
- `+ci-release` - Build CI release with specific tags
- `+earthly-docker` - Build Docker images
- Multiple test groups for parallel CI execution

## Debugging

- Use `--GO_GCFLAGS='all=-N -l'` to disable optimizations for delve
- Enable buildkit scheduler debug with `BUILDKIT_SCHEDULER_DEBUG=1`
- Use `earthly --debug` for verbose output
- Interactive debugger available in some test scenarios

## Important Notes

- This is a community fork endorsed by the original Earthly team
- Uses buildkit for underlying container operations
- Extensive shell script testing with shellcheck
- Vale for documentation linting and spell checking
- Multi-platform builds supported (Linux AMD64/ARM64, macOS AMD64/ARM64, Windows AMD64)