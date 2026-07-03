# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Go Web Server is a static file HTTP/HTTPS server that can serve content from local directories, archives (zip, tar, 7z, rar), git repositories, and HTTP reverse proxy. It includes auto-generated HTTPS certificates, Prometheus metrics, and OpenTelemetry tracing.

## Build Commands

```bash
make all              # Build binaries for all platforms
make clean            # Clean build artifacts
make test             # Run unit tests with race detection
make test-10          # Run tests 10 times (CI standard)
make benchmark        # Run benchmarks
make coverage         # Generate coverage report
make lint             # Run go fmt and go vet
make deps             # Download and tidy dependencies
make run              # Build and run locally on port 8181
make presubmit        # Run complete CI checks locally
```

Go version: 1.24+

## Running a Single Test

```bash
go test -race -v -run TestName ./pkg/gowebserver/
```

## Architecture

```bash
cmd/                      # CLI entry points
└── gowebserver/          # Main web server binary

pkg/                      # Public libraries
└── gowebserver/          # Core server implementation

internal/gowebserver/testing/  # Test utilities and embedded test archives
```

### Core Components (pkg/gowebserver/)

- **config.go**: CLI flags and YAML config loading
- **httpserver.go**: `WebServer` interface, HTTP/HTTPS listener setup, handler registration
- **filesystem.go**: `FileSystem` interface for content sources
- **filesystem_*.go**: Implementations for local dirs, archives, git repos, nested archives
- **index.go / customindex.go**: Directory listing templates (basic and custom css UI)
- **monitoring.go**: Prometheus metrics, OpenTelemetry tracing, pprof endpoints
- **upload.go**: Multi-file upload with MD5 token validation
- **custom-index.html**: Enhanced HTML template for file browsing. Includes advanced preview features for images and videos.

### Request Flow

1. `cmd/gowebserver/gowebserver.go` calls `gowebserver.Run()`
2. `Run()` loads config, initializes logging, creates `WebServer`
3. `WebServer.Serve()` sets up handlers and listens on configured ports

## Testing

Tests use Go's standard `testing` package with `testify` for assertions. Test archives are embedded in `internal/gowebserver/testing/` and accessed via helper functions:

- `MustZipFilePath()`, `MustRarFilePath()`, `Mus7ZipFilePath()`, etc.
- `MustCreateTempFile()`, `MustWriteTempFile()` for temp file creation with auto-cleanup

## CI/CD

GitHub Actions runs on push to main, tags, and PRs:

- Build: `make clean deps lint all`
- Test: `make test-10 benchmark coverage.txt`
- Code quality: misspell, hadolint, codespell, CodeQL
- Release: Tag-triggered multi-platform binary releases
