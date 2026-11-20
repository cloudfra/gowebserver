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
make test-js          # Run the templates' JavaScript tests (also part of make test)
make benchmark-js     # Run the templates' JavaScript benchmarks
make test-browser     # Run the pages in headless Chromium and Firefox
make benchmark-browser  # Time the file browser in a headless browser
make browse TARGET=.  # Open a URL or folder headless: logs, timings, screenshot
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
├── ffmpeg/               # Runs ffmpeg; finds or downloads (on demand, after license acceptance) the binary
└── gowebserver/          # The handlers: what a program that embeds gowebserver links (NewHandler, SPA, Compress, Mount), and WebServer
    └── server/           # The standalone server: Run, Load (flags, YAML), logging (zap), certificates, Prometheus/OTLP exporters, pprof, CORS, every ufs driver

internal/gowebserver/testing/  # Test utilities and embedded test archives
└── embedhost/                 # A host program embedding NewHandler, for the browser tests
```

### The two packages

`pkg/gowebserver` is all a program that embeds gowebserver links, so it must stay lean: it imports no exporter, no zap, no CORS, no certificate tooling, nothing that registers on `http.DefaultServeMux` (`net/http/pprof`, `expvar`), and of ufs's drivers only `drivers/core` (local folders and archives). The rest is opt-in by import: the program adds `drivers/gitfs`, `drivers/gcsfs` or `drivers/all` for other `Path`s and `drivers/boltfs` for a thumbnail cache file. `deps_test.go` builds `testdata/embedfs` (an `fs.FS`) and `testdata/embedpath` (a local `Path`) and fails if they link any of that; keep the list there current. `pkg/gowebserver/server` is the standalone server, built on `NewInstrumented` with an `Instrumentation` (its providers, `/metrics`, pprof and tracez routes, CORS); `cmd/gowebserver` calls `server.Run()`.

### Core Components (pkg/gowebserver/)

- **config.go**: The configuration's types (`Config` and the rest), which `server.Load` fills from flags and YAML
- **instrumentation.go**: `Instrumentation`, what the standalone server adds to a `WebServer`, and `monitoringContext`, where the handlers report (meters, tracers, logger, diagnostics)
- **server/config.go**: CLI flags and YAML config loading. Flags are registered on a `flag.FlagSet` that `Load` makes (`registerFlags`), never on `flag.CommandLine`, and nothing happens at import time: a program that embeds the package keeps its own flags
- **server/gowebserver.go**: `Run`, `New` (the standalone `WebServer`), logging and certificates
- **server/monitoring.go**: Prometheus metrics, OpenTelemetry tracing, zpages; **server/pprof.go**: the pprof endpoints
- **handler.go**: `NewHandler`, `Options`, `Handler.Close`: one content source's handlers as an `http.Handler` for another Go program to mount (behind `http.StripPrefix`). It must have no global side effects: no flags, routes, listeners or `os.Exit`, never the otel/zap globals or slog's default logger (logs, meters and tracers come from `Options`, nil being discard/no-op, through `monitoringContext`), no goroutine that outlives `Close`. Everything it serves or links to stays under its mount: links are relative to the page (`mountRoot`, `relativeURL`), never from `/`, and optional endpoints (upload, `__diag`) are inside the handler, off unless asked for. The standalone server (`Serve`) is built from the same pieces (`newContentHandler`, `newThumbnailer`, `startDiagnostics`)
- **spa.go**: `SPA`: a built single-page app from an `fs.FS`; its index for every route that isn't a file, `immutable` on its hashed assets
- **gzip.go**: `gzipHandler` and the exported `Compress`/`CompressWith`; a body without a Content-Length is held until it's `minSize` long before deciding; nested, only the outermost compresses
- **server/pprof.go**: the standalone server's pprof endpoints from `runtime/pprof`. Never import `net/http/pprof` (or `expvar`) in `pkg/gowebserver`: its `init` registers on `http.DefaultServeMux`, which a host may serve. (`/debug/vars` still comes with ufs's archive support, through `bodgit/sevenzip` and `go4.org/readerutil`; `deps_test.go` checks that's the only way)
- Logging on a request's path: Debug at most, and never a file or folder name (log `errCategory(err)`, not the error, whose text names paths); answer filesystem errors with `fsError` (404/403/500 with a generic body)
- **httpserver.go**: `WebServer` interface, HTTP/HTTPS listener setup, handler registration
- **httpsredirect.go**: Wraps the HTTPS listener: a connection whose first byte starts an HTTP method (TLS starts with 0x16) gets a redirect to `https://` on the same host, port, path and query; everything else goes on to TLS untouched. Sniffed per connection under `httpsFirstByteTimeout`, then `httpsRedirectTimeout` for a plain request, so `Accept` never waits on a client. Both ports serve through `newHTTPServer` (httpserver.go), whose `ReadHeaderTimeout` also bounds the TLS handshake; it sets no read or write timeout, which would cut off large uploads and downloads
- **filesystem.go**: `FileSystem` interface for content sources
- **filesystem_*.go**: Implementations for local dirs, archives, git repos, nested archives
- **index.go / customindex.go**: Directory listing templates (basic and custom css UI)
- **upload.go**: Multi-file upload with MD5 token validation
- **richview.go**: Syntax-highlighted text viewer (`?view=rich`)
- **thumbnail.go**: Resized images and video frames (`?width=`/`?height=`), made once and stored
- **thumbnail_metrics.go**: Where thumbnail time goes: `thumbnail_*` Prometheus metrics, a span per stage (wait, decode, scale, encode, store), and a `Server-Timing` header on each thumbnail for the browser's network panel
- **folderpreview.go**: A folder's preview for the file browser's list (`folder/?preview`, JSON): its first `folderPreviewItems` photos and videos by name, or, with none, the nearest ones below it within `folderPreviewDepth` levels and `folderPreviewBudget` entries read. Kept per folder and modification time for `folderPreviewFor`. `folders.js` shows it as a strip in the row, loading only as many as fit, and tints the row with the strip's `vividColor`
- **subfolders.go**: A folder's subfolders, in its listing's order (`folder/?folders`, JSON; the root index answers `/?folders` with its serve paths). The breadcrumb's panel asks a crumb's parent for it, when the panel opens
- **diagnostics.go**: `-debug.diagnostics=FILE` (`debug: diagnostics:` in YAML), off by default: the server's and the file browser's events (thumbnails, ffmpeg runs, what the page loads and shows) as one readable line each, with every file and folder name replaced by an HMAC token and errors by a category. Anything new written there must stay free of names: tokens via the `*diagLog`'s `anon`/`anonPath`/`anonDir`, errors via `errCategory`, and a page field only with a validator in `diagClientText`. The log is per server or embedded handler (`monitoringContext.diag`, nil when off), never a global; gather an event's fields only when `d.enabled()`. The page sends its events to `__diag` at the root of the handler that served it
- **request_metrics.go**: What otelhttp's request metrics leave out: the `handler` label (file, listing, thumbnail, richview, upload, proxy, index) on them, time to first byte, requests in progress; and `stageMetrics`, the span-and-histogram per stage that listings and the text viewer use. A handler says what it is with `setRequestHandler`
- **templates.go**: Loads the page templates; see below

### Page templates (pkg/gowebserver/template/)

Each template has a directory, `template/NAME/NAME.html`, with its CSS and JavaScript in files beside it. A page is served as one response, so `loadTemplate` puts them back inline: a line that is only `{{include "FILE"}}` becomes that file's content. Included files are plain CSS or JavaScript; anything that needs a template action stays in the HTML (an action in an included file is an error).

- **custom-index/**: Enhanced file browser. Its scripts (`util.js`, `diag.js`, `thumbs.js`, `data.js`, `folders.js`, `accent.js`, `list.js`, `grid.js`, `scrubber.js`, `slideshow.js`, `main.js`) are included in that order inside one function, so they share a scope. Media is emitted as a compact JS array and rendered by a virtualized grid (only rows near the viewport are in the DOM); the folders and files likewise (`DirEntry.ListJS`), rendered by a virtualized list (`list.js`, every row one height); the slideshow mounts only the current slide and its neighbors. Keep these properties when editing, since folders can hold tens of thousands of files and photos. `testdata/test-modernindex.html` is a golden render of it; regenerate with the `cp` command `TestIndexHTTPHandlerServeHTTP` prints.
- **page-theme/**: Templates both pages include (`page-style`, `page-script`): design tokens, header, toolbar, button hover, theme switcher, the title that travels into the toolbar. Change shared look and behavior there, not in either page; `createTemplate` parses it into every page's template set. The breadcrumb starts at `HOME`: the server's `/`, or, for an embedded handler, its mount, from `#crumbs`' `data-home` (relative to the page) and `data-title`. `hover-panel.js` there is the one dwell, pointer gate and reveal for panels that open when the pointer rests on something (`crumb-panel.js`, the breadcrumb's sibling folders; `folders.js`, a row's strip slideshow); it's `window.gowsHoverPanel` to the file browser's scripts. A new hover panel goes through it
- **rich-view/**: The text viewer's page
- **upload/**: The upload page
- **template-index/**: The plain directory listing

JavaScript tests come in two kinds, both driven by `Makefile_javascript.mk` (Node and npm are assumed installed; nothing is installed beside the sources):

- `template/NAME/test/*.test.js` and `template/NAME/bench/*.bench.js` run on Node alone (`node:test`, no packages) against the stand-in browser in `template/harness.js`, which has no layout: a test sets the sizes it needs. `make test-js` (part of `make test`, through `TEST_TARGETS`) and `make benchmark-js`.
- `template/NAME/browser/*.browser.js` run in real headless browsers (Playwright, installed with the browsers into the toolchain cache, `TOOLCHAIN_CACHE`) against the real server, through `template/browser.js` (`serve`), or against `internal/gowebserver/testing/embedhost`, a host program that mounts `NewHandler` at `/media/` beside its own API and app (`serveEmbedded`). `make test-browser` runs them in each of `HEADLESS_BROWSERS` (chromium and firefox; webkit needs system libraries). It is not part of `make test`.

To see how a page actually behaves, use the browser rather than guessing: `make browse TARGET=<url or folder> [SCREENSHOT=file.png] [BROWSER=firefox]` prints what the page logged, what failed, timings and grid counts and saves a screenshot (read it with the Read tool); `make benchmark-browser [PHOTOS=5000] [PHOTO_SIZE=3000x2000] [THUMBNAILS=off]` times loading and scrolling a generated folder, cold and warm. It scrolls twice: `fling` is too fast for the grid to load anything, and `browse` is slow with pauses, which is when pictures load, fade in and color the backdrop, so judge scrolling by `browse`. These rebuild the server first, since the templates are embedded in it.

Only `template/*/*.{html,css,js}` is embedded, so keep tests in those subdirectories.

Filesystem handlers are mounted with `http.StripPrefix`, so `r.URL.Path` lacks the serve path (e.g. `/e`). Build links and redirects relative to the request (`relativeURL`, `redirectRelative`), never from `r.URL.Path`.

### Monitoring (install/prometheus, install/grafana)

`install/prometheus/rules` holds the alerts (with `promtool test rules` tests beside them) and `install/grafana/dashboards` the dashboards; both name the server's metrics, and `install/prometheus/README.md` lists them. When a metric or label is added, renamed or removed, change them together. `install/compose` mounts both.

### Request Flow

1. `cmd/gowebserver/gowebserver.go` calls `server.Run()`
2. `Run()` loads config, initializes logging, creates `WebServer` (`server.New`, which is `gowebserver.NewInstrumented` with its exporters and CORS)
3. `WebServer.Serve()` sets up handlers and listens on configured ports

## Testing

Tests use Go's standard `testing` package with `testify` for assertions. Test archives are embedded in `internal/gowebserver/testing/` and accessed via helper functions:

- `MustZipFilePath()`, `MustRarFilePath()`, `Mus7ZipFilePath()`, etc.
- `MustCreateTempFile()`, `MustWriteTempFile()` for temp file creation with auto-cleanup

## CI/CD

GitHub Actions (`deploy.yaml`) runs on push to main and PRs:

- Build: `make clean deps lint all`
- Test: `make test-10 benchmark coverage.txt`
- Code quality: misspell, hadolint, codespell, CodeQL
- Release: `create-release.yaml`, on a `v*` tag push, runs `make test` once, then publishes the multi-platform binaries and the container images
