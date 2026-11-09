# Go Web Server

A simple, convenient, reliable, well tested HTTP/HTTPS web server to host static files.
It can host a local directory or contents of a zip file.

```bash
# Download (linux amd64, see Downloads for other builds)
curl -o gowebserver -O -L https://github.com/cloudfra/gowebserver/releases/download/v3.8.1/gowebserver-linux_amd64; chmod +x gowebserver

# Host the current directory.
./gowebserver

# Host your home directory.
./gowebserver --path=${HOME}

# Host a zip file from the internet.
./gowebserver --path=https://github.com/cloudfra/gowebserver/archive/v3.8.1.zip

# Install in your Kubernetes Cluster.
kubectl apply -f https://raw.githubusercontent.com/cloudfra/gowebserver/main/install/kubernetes.yaml
```

## Windows Service

```powershell
sc.exe create gowebserver DisplayName= "Go Web Server" start= delayed-auto binpath= "C:\apps\gowebserver.exe -configfile=C:\apps\gowebserver.yaml"
sc.exe description gowebserver "Web server for files on your hard drive with a rich browsing experience. Change settings in C:\apps\gowebserver.yaml"
sc.exe failure gowebserver reset= 0 actions= restart/1000
sc.exe start gowebserver
```

## Features

* Zero-config required, hosts on port 80 or 8080 based on root and supports Cloud9's $PORT variable.
* HTTP and HTTPs serving
* Automatic HTTPs certificate generation
* Optional configuration by flags or YAML config file.
* Host local or HTTP served static files from:
  * Local directory (current directory is default)
  * ZIP archive
  * Tarball archive (.tar, .tar.bz2, .tar.gz, .tar.lz4, .tar.xz)
  * 7-zip
  * RAR
  * Git repository (HTTPS, SSH)
* Thumbnails: add `width` and/or `height` to an image or video URL to get a resized copy (see [Thumbnails](#thumbnails)).
* Metrics export to Prometheus.
* Prebuild binaries for all major OSes.

## ffmpeg

Some features use [ffmpeg](https://ffmpeg.org). gowebserver doesn't ship it. It uses, in order:

1. The program set with `ffmpeg.path` (`-ffmpeg.path`), or the folder it's in: gowebserver looks for `ffmpeg` (`ffmpeg.exe` on Windows) there and in the folder's `bin` folder. When `ffmpeg.path` is set, the server won't start unless it finds ffmpeg there and ffmpeg runs, and the error says why.
2. The `ffmpeg` installed on `PATH`.
3. A copy downloaded the first time it's needed, only when both `ffmpeg.installOnDemand` and `ffmpeg.acceptLicense` are set.

The downloadable builds are GPL, so gowebserver won't download one until you accept [ffmpeg's license](https://ffmpeg.org/legal.html). By default the download is the latest release from [BtbN's FFmpeg-Builds](https://github.com/BtbN/FFmpeg-Builds) for Linux or Windows on amd64 or arm64, checked against its published SHA-256. `ffmpeg.sourceUrl` names another build instead: a `.tar.xz`, `.tar.gz` or `.zip` holding an `ffmpeg` binary. Append `#sha256=<hex>` to the URL to have it verified. Downloads, with their license files, are kept in the user cache directory (`~/.cache/gowebserver/ffmpeg` on Linux) and reused.

```yaml
ffmpeg:
  acceptLicense: true
  path: ""            # empty: use the installed ffmpeg, if any
  installOnDemand: true
  sourceUrl: ""       # empty: the latest release for this platform
```

Without ffmpeg, the features that need it are turned off.

## Thumbnails

Request an image or video with `width` and/or `height` query parameters to get a thumbnail instead of the file, e.g. `photo.jpg?width=512&height=512`. The enhanced index (`-enhancedindex`) uses these for its photo grid, slideshow and video posters.

| Parameter | Meaning |
| --- | --- |
| `width`, `height` | Size in pixels, 1 to 4096. With only one, the other follows the file's aspect ratio (never enlarged). |
| `fit` | How the file fills a `width` x `height` box: `cover` fills it and crops the overflow (default), `contain` fits inside and pads with `background`, `fill` stretches, `inside` fits inside at the file's ratio without padding or enlarging, `outside` covers the box at the file's ratio without cropping or enlarging. |
| `format` | `webp`, `jpeg`, `png` or `gif`. Defaults to `webp` when the browser accepts it and [ffmpeg](#ffmpeg) can write it, otherwise `png` for images with transparency and `jpeg` for the rest. `webp` needs ffmpeg; without it the default is used. |
| `background` | Padding color for `fit=contain`: `RRGGBB`, `RRGGBBAA` or `transparent`. Defaults to transparent for webp, png and gif, black for jpeg. |
| `quality` | WebP or JPEG quality, 1 to 100 (default 75). |

Images in JPEG, PNG, GIF, WebP, BMP and TIFF are supported, and JPEG EXIF orientation is applied. Other image formats are served unchanged. Video thumbnails are the first keyframe 10 seconds or more into the video (or an earlier frame of a shorter one), made with [ffmpeg](#ffmpeg). When that frame is too dark to show anything (a black intro, a fade, titles on black), up to three later ones are tried (30 seconds and a minute in, a quarter and half way through) and the first bright one is used. Without ffmpeg, or while it can't be started, video thumbnail requests return 415 and the file browser shows the videos themselves. A video ffmpeg can't make a thumbnail of gets 415 too, and that's remembered for an hour, so it isn't tried again for every size or page load. Thumbnails are cached in a BoltDB file, so they survive restarts; a thumbnail is replaced when its file changes. Set the file with `thumbnails.cachePath` (`-thumbnails.cachepath`). By default it's in the user cache directory (`~/.cache/gowebserver/thumbnails.db` on Linux), and `memory:` keeps thumbnails in memory only. If the file can't be opened, for example because another gowebserver is using it, thumbnails are kept in memory instead, after waiting up to a minute for the lock. Turn the feature off with `-thumbnails.enabled=false`.

With ffmpeg, thumbnails are cheaper and smaller. It decodes JPEG photos several times faster than Go, and at a half, a quarter or an eighth of their size when the thumbnail needs no more. When the browser accepts WebP (all current browsers do), a thumbnail that only shrinks the file (`fit=outside` or `inside`, or one side given) is made in one run of ffmpeg, from the file to the WebP image. For 12-megapixel photos at the grid's 512-pixel tiles, that takes about 30 ms of one core instead of about 150 ms spread over several, and the files are about a third smaller than JPEG at quality 82, with no difference you can see at that size. Without ffmpeg, photos are decoded in Go and thumbnails are JPEG or PNG. Responses whose format the browser's `Accept` header picked carry `Vary: Accept`.

The enhanced index asks for only a few sizes: 128, 256, 512 or 1024 pixels, covering square photo tiles or 16:9 video tiles (`fit=outside`, so the thumbnail keeps the file's shape and the tile crops it), chosen once for the screen's pixel density. It uses 1024 for slideshow previews. The browser scales these to fit, so each file is rendered at only a couple of sizes.

## Debugging

Settings only for debugging and diagnosing the server are in one block, all off unless set:

```yaml
debug:
  endpoint: /debug    # pprof and OpenTelemetry tracez under this path (-debug.endpoint)
  shutdown: false     # /diediedie shuts the server down, for tests (-debug.shutdown)
  diagnostics: ""     # anonymized browsing diagnostics to this file; see below (-debug.diagnostics)
```

They used to be `monitoring.debugEndpoint` (`-monitoring.debugendpoint`) and a top-level `debug: true` (`-debug`). A configuration file that still uses those stops the server at startup with an error saying where each one is now.

## Diagnostics

To find out where browsing is slow or goes wrong, `-debug.diagnostics=FILE` (`diagnostics: FILE` under `debug:` in the configuration file) has the server and the file browser write what happened to `FILE`, one event a line, as plain text you can read before passing it on. It's off by default.

```text
2026-11-08T21:32:36.042Z server thumbnail file=6e02f171a3.mp4 dir=50408608b6/bb950beaf7 kind=video result=made ms=400 spec=512x288-outside
2026-11-08T21:32:36.064Z server ffmpeg-frame file=6e02f171a3.mp4 at_s=10 luma=141 ms=240 piped=false
2026-11-08T21:32:37.425Z client thumb url="6e02f171a3.mp4?fit=outside&height=288&width=512" kind=strip ok=true ms=380 cache=net
```

What's recorded: each thumbnail request (result, time, error kind), each run of ffmpeg for a video (where in the video, how long, how bright the frame was), and from the browser, the page (how many photos and videos, screen size, protocol), each thumbnail it loaded or gave up on, video tiles, the slideshow's filmstrip, how many tiles on screen are still waiting (every few seconds), and script errors (kind, line and function names).

What isn't: no file or folder name. Each is replaced by a token, the first 10 hex digits of an HMAC-SHA256 under a random key that is made when the server starts and never written, so a file can be followed through one run's log but its name can't be recovered, and tokens don't match between runs. Files keep their extension (`.mp4`); folders keep nothing. Errors are written as a category such as `timeout` or `ffmpeg-invalid-data`, never their message, which can contain paths or a video's title. The server checks what the browser sends: only known fields are kept, URLs are anonymized, and text is kept only where a field has a fixed set of values. The browser's user agent is reduced to a device and browser name (`ipad,safari-18`). The file stops growing at 256 MB, since anyone who can reach the server can send the browser's events while diagnostics are on.

## Downloads

|   OS    | Arch  | Link
| ------- | ----- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
| Linux   | amd64 | `curl -O -L https://github.com/cloudfra/gowebserver/releases/download/v3.8.1/gowebserver-linux_amd64`
| Linux   | arm64 | `curl -O -L https://github.com/cloudfra/gowebserver/releases/download/v3.8.1/gowebserver-linux_arm64`
| Windows | amd64 | `$ProgressPreference = 'SilentlyContinue'; Invoke-WebRequest -Uri "https://github.com/cloudfra/gowebserver/releases/download/v3.8.1/gowebserver-windows_amd64.exe" -OutFile "server.exe" -UseBasicParsing`
| macOS   | arm64 | `curl -O -L https://github.com/cloudfra/gowebserver/releases/download/v3.8.1/gowebserver-darwin_arm64`

## Docker Images

* [gowebserver](https://hub.docker.com/r/cloudfra/gowebserver/tags)

```bash
docker pull docker.io/cloudfra/gowebserver
```

## Build

![example workflow](https://github.com/cloudfra/gowebserver/actions/workflows/deploy.yaml/badge.svg) [![Go Reference](https://pkg.go.dev/badge/github.com/cloudfra/gowebserver.svg)](https://pkg.go.dev/github.com/cloudfra/gowebserver) [![codecov](https://codecov.io/gh/cloudfra/gowebserver/branch/main/graph/badge.svg)](https://codecov.io/gh/cloudfra/gowebserver) [![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cloudfra/gowebserver/badge)](https://scorecard.dev/viewer/?uri=github.com/cloudfra/gowebserver)

Install [Go 1.24 or newer](https://golang.org/dl/).

```bash
echo '# Non-free Repositories' | sudo tee /etc/apt/sources.list.d/debian-nonfree.list > /dev/null
for target in $(lsb_release -c -s)
do
  echo "deb http://deb.debian.org/debian $target contrib non-free non-free-firmware" | sudo tee -a /etc/apt/sources.list.d/debian-nonfree.list > /dev/null
  echo "deb-src http://deb.debian.org/debian $target contrib non-free non-free-firmware" | sudo tee -a /etc/apt/sources.list.d/debian-nonfree.list > /dev/null
done

# Install Dependencies for Building and Testing
sudo apt-add-repository non-free
sudo apt-get update
sudo apt-get -y -q install lz4 p7zip-full rar unrar
```

```bash
# Clone the Codebase
git clone git@github.com:cloudfra/gowebserver.git
# Build the Code
make -j$(nproc)
```

## Test

```bash
make test
make bench
```

## Common make targets

| Target            | Description
| ----------------- | ------------------------------------------------------------------------
| `make all`        | Build binaries for all supported platforms.
| `make run`        | Build and run the server locally on port 8181.
| `make test`       | Run the unit test suite (Go and Terraform).
| `make test-deflake` | Run the Go tests under the race detector to catch flaky tests.
| `make bench`      | Run benchmarks.
| `make lint`       | Run the full lint suite (Go, Terraform, Docker, YAML, shell, markdown, vulnerabilities).
| `make coverage`   | Generate a test coverage report.
| `make deps`       | Download and tidy Go module dependencies.
| `make tools`      | Download the pinned toolchain used by `make lint` and friends.
| `make clean`      | Remove build artifacts.
| `make presubmit`  | Run the same checks CI runs on every push and pull request.
| `make docker-images` | Build the Docker images.

## Project layout

```text
cmd/                      # CLI entry points
└── gowebserver/          # Main web server binary

pkg/                      # Public libraries
└── gowebserver/          # Core server implementation

internal/gowebserver/testing/  # Test utilities and embedded test archives
```

The core server implementation lives in `pkg/gowebserver/`:

* `config.go` — CLI flags and YAML config loading.
* `httpserver.go` — `WebServer` interface, HTTP/HTTPS listener setup, handler registration.
* `filesystem.go` and `filesystem_*.go` — `FileSystem` interface and its implementations for local directories, archives, git repositories, and nested archives.
* `index.go` / `customindex.go` — directory listing templates (basic and custom CSS UI).
* `monitoring.go` — Prometheus metrics, OpenTelemetry tracing, and pprof endpoints.
* `upload.go` — multi-file upload with MD5 token validation.

## Example

Sample code for embedding a HTTP/HTTPS server in your application.

```go
// Package main provides a web server to serve the file system of the host system. This is very insecure!
package main

import (
  "github.com/cloudfra/gowebserver/pkg/gowebserver"
  "go.uber.org/zap"
)

func main() {
  logger, err := zap.NewProduction()
  if err != nil {
    zap.S().Fatal(err)
  }
  if err == nil {
    zap.ReplaceGlobals(logger)
  }
  defer logger.Sync()
  httpServer, err := gowebserver.New(&gowebserver.Config{
    Serve: []gowebserver.Serve{{Source: ".", Endpoint: "/"}},
  })
  if err != nil {
    zap.S().Fatal(err)
  }

  termCh := make(chan error)
  httpServer.Serve(termCh)
}
```
