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
└── gowebserver/          # Core server implementation

internal/gowebserver/testing/  # Test utilities and embedded test archives
```

### Core Components (pkg/gowebserver/)

- **config.go**: CLI flags and YAML config loading
- **httpserver.go**: `WebServer` interface, HTTP/HTTPS listener setup, handler registration
- **httpsredirect.go**: Wraps the HTTPS listener: a connection whose first byte starts an HTTP method (TLS starts with 0x16) gets a redirect to `https://` on the same host, port, path and query; everything else goes on to TLS untouched. Sniffed per connection under `httpsFirstByteTimeout`, then `httpsRedirectTimeout` for a plain request, so `Accept` never waits on a client. Both ports serve through `newHTTPServer` (httpserver.go), whose `ReadHeaderTimeout` also bounds the TLS handshake; it sets no read or write timeout, which would cut off large uploads and downloads
- **filesystem.go**: `FileSystem` interface for content sources
- **filesystem_*.go**: Implementations for local dirs, archives, git repos, nested archives
- **index.go / customindex.go**: Directory listing templates (basic and custom css UI)
- **monitoring.go**: Prometheus metrics, OpenTelemetry tracing, pprof endpoints
- **upload.go**: Multi-file upload with MD5 token validation
- **richview.go**: Syntax-highlighted text viewer (`?view=rich`)
- **videometa.go**: A video's metadata from the files beside it (`NAME.chapters.vtt`, `NAME.markers.json`, `NAME[.LANG].vtt|srt`, `NAME.thumbs.vtt`) and, with ffmpeg, its container (chapters, length): `NAME.EXT?meta` (JSON), `NAME.EXT?sprite` (scrub preview sprite, stored like a thumbnail) and `NAME.srt?vtt`. Sets the Content-Type of videos and captions from its own table (never the global `mime` table) and `nosniff` on what the content handler serves. Every URL it gives is relative to the video, so the content works under any mount. The listing hides a video's sidecars and puts badges on its tile (`listingSidecars`); `player.js` is the gallery's dock that shows it all, and asks for `?meta` only when a video becomes the current slide. Its timeline preview shows the sprite only once loaded, and until then (or without one) a frame from a muted second copy of the video, never an empty box
- **thumbnail.go**: Resized images and video frames (`?width=`/`?height=`), made once and stored
- **mediainfo.go** (with **mediainfo_image.go**, **mediainfo_media.go**): A photo's, video's or song's details for the slideshow's info panel (`info.js`), from the file's own metadata: `NAME.EXT?info` (JSON). Its own parsers read only the headers (EXIF, XMP, ICC from JPEG/PNG/WebP/TIFF/HEIF; MP4 and QuickTime boxes; ID3v2, FLAC and Ogg tags), seeking past media data, or reading past it up to `mediaInfoSkipMax` when the filesystem can't seek (archives); a video adds what ffmpeg says (`videoMetaHandler.probe`). The document is ranked (`mediaGroupRank`, `mediaFieldRank`). A location's country is looked up offline in `geodata/worldmap.json` (Natural Earth, made by `internal/tools/worldmap`), which `FOLDER/?worldmap` serves for the panel to draw: the page never asks another server for a map
- **thumbnail_metrics.go**: Where thumbnail time goes: `thumbnail_*` Prometheus metrics, a span per stage (wait, decode, scale, encode, store), and a `Server-Timing` header on each thumbnail for the browser's network panel
- **folderpreview.go**: A folder's preview for the file browser's list (`folder/?preview`, JSON): its first `folderPreviewItems` photos and videos by name, or, with none, the nearest ones below it within `folderPreviewDepth` levels and `folderPreviewBudget` entries read. Kept per folder and modification time for `folderPreviewFor`. `folders.js` shows it as a strip in the row, loading only as many as fit, and tints the row with the strip's `vividColor`
- **subfolders.go**: A folder's subfolders, in its listing's order (`folder/?folders`, JSON; the root index answers `/?folders` with its serve paths). The breadcrumb's panel asks a crumb's parent for it, when the panel opens
- **diagnostics.go**: `-debug.diagnostics=FILE` (`debug: diagnostics:` in YAML), off by default: the server's and the file browser's events (thumbnails, ffmpeg runs, what the page loads and shows) as one readable line each, with every file and folder name replaced by an HMAC token and errors by a category. Anything new written there must stay free of names: tokens via `anon`/`anonPath`/`anonDir`, errors via `errCategory`, and a page field only with a validator in `diagClientText`. Gather an event's fields only when `diagEnabled()`
- **request_metrics.go**: What otelhttp's request metrics leave out: the `handler` label (file, listing, thumbnail, videometa, mediainfo, richview, upload, proxy, index) on them, time to first byte, requests in progress; and `stageMetrics`, the span-and-histogram per stage that listings and the text viewer use. A handler says what it is with `setRequestHandler`
- **templates.go**: Loads the page templates; see below

### Page templates (pkg/gowebserver/template/)

Each template has a directory, `template/NAME/NAME.html`, with its CSS and JavaScript in files beside it. A page is served as one response, so `loadTemplate` puts them back inline: a line that is only `{{include "FILE"}}` becomes that file's content. Included files are plain CSS or JavaScript; anything that needs a template action stays in the HTML (an action in an included file is an error).

- **custom-index/**: Enhanced file browser. Its scripts (`util.js`, `diag.js`, `thumbs.js`, `data.js`, `folders.js`, `accent.js`, `list.js`, `grid.js`, `scrubber.js`, `player.js`, `info.js`, `slideshow.js`, `main.js`) are included in that order inside one function, so they share a scope. Media is emitted as a compact JS array and rendered by a virtualized grid (only rows near the viewport are in the DOM); the folders and files likewise (`DirEntry.ListJS`), rendered by a virtualized list (`list.js`, every row one height); the slideshow mounts only the current slide and its neighbors. Keep these properties when editing, since folders can hold tens of thousands of files and photos. `testdata/test-modernindex.html` is a golden render of it; regenerate with the `cp` command `TestIndexHTTPHandlerServeHTTP` prints.
- **page-theme/**: Templates both pages include (`page-style`, `page-script`): design tokens, header, toolbar, button hover, theme switcher, the title that travels into the toolbar. Change shared look and behavior there, not in either page; `createTemplate` parses it into every page's template set. `hover-panel.js` there is the one dwell, pointer gate and reveal for panels that open when the pointer rests on something (`crumb-panel.js`, the breadcrumb's sibling folders; `folders.js`, a row's strip slideshow); it's `window.gowsHoverPanel` to the file browser's scripts. A new hover panel goes through it
- **rich-view/**: The text viewer's page
- **upload/**: The upload page
- **template-index/**: The plain directory listing

JavaScript tests come in two kinds, both driven by `Makefile_javascript.mk` (Node and npm are assumed installed; nothing is installed beside the sources):

- `template/NAME/test/*.test.js` and `template/NAME/bench/*.bench.js` run on Node alone (`node:test`, no packages) against the stand-in browser in `template/harness.js`, which has no layout: a test sets the sizes it needs. `make test-js` (part of `make test`, through `TEST_TARGETS`) and `make benchmark-js`.
- `template/NAME/browser/*.browser.js` run in real headless browsers (Playwright, installed with the browsers into the toolchain cache, `TOOLCHAIN_CACHE`) against the real server, through `template/browser.js`. `make test-browser` runs them in each of `HEADLESS_BROWSERS` (chromium and firefox; webkit needs system libraries). It is not part of `make test`.

To see how a page actually behaves, use the browser rather than guessing: `make browse TARGET=<url or folder> [SCREENSHOT=file.png] [BROWSER=firefox]` prints what the page logged, what failed, timings and grid counts and saves a screenshot (read it with the Read tool); `make benchmark-browser [PHOTOS=5000] [PHOTO_SIZE=3000x2000] [THUMBNAILS=off]` times loading and scrolling a generated folder, cold and warm. It scrolls twice: `fling` is too fast for the grid to load anything, and `browse` is slow with pauses, which is when pictures load, fade in and color the backdrop, so judge scrolling by `browse`. These rebuild the server first, since the templates are embedded in it.

Only `template/*/*.{html,css,js}` is embedded, so keep tests in those subdirectories.

Filesystem handlers are mounted with `http.StripPrefix`, so `r.URL.Path` lacks the serve path (e.g. `/e`). Build links and redirects relative to the request (`relativeURL`, `redirectRelative`), never from `r.URL.Path`.

### Monitoring (install/prometheus, install/grafana)

`install/prometheus/rules` holds the alerts (with `promtool test rules` tests beside them) and `install/grafana/dashboards` the dashboards; both name the server's metrics, and `install/prometheus/README.md` lists them. When a metric or label is added, renamed or removed, change them together. `install/compose` mounts both.

### Request Flow

1. `cmd/gowebserver/gowebserver.go` calls `gowebserver.Run()`
2. `Run()` loads config, initializes logging, creates `WebServer`
3. `WebServer.Serve()` sets up handlers and listens on configured ports

## Testing

Tests use Go's standard `testing` package with `testify` for assertions. Test archives are embedded in `internal/gowebserver/testing/` and accessed via helper functions:

- `MustZipFilePath()`, `MustRarFilePath()`, `Mus7ZipFilePath()`, etc.
- `MustCreateTempFile()`, `MustWriteTempFile()` for temp file creation with auto-cleanup
- `MustMediaFile()`: photos and videos with metadata in them (`internal/gowebserver/testing/media`, remade by `TestWriteMediaFixtures`), also read by the browser tests

## CI/CD

GitHub Actions (`deploy.yaml`) runs on push to main and PRs:

- Build: `make clean deps lint all`
- Test: `make test-10 benchmark coverage.txt`
- Code quality: misspell, hadolint, codespell, CodeQL
- Release: `create-release.yaml`, on a `v*` tag push, runs `make test` once, then publishes the multi-platform binaries and the container images
