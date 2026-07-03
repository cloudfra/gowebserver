# Go Web Server

A simple, convenient, reliable, well tested HTTP/HTTPS web server to host static files.
It can host a local directory or contents of a zip file.

```bash
# Download (linux amd64, see Downloads for other builds)
curl -o gowebserver -O -L https://github.com/jeremyje/gowebserver/releases/download/v3.6.0/server-amd64; chmod +x gowebserver

# Host the current directory.
./gowebserver

# Host your home directory.
./gowebserver --path=${HOME}

# Host a zip file from the internet.
./gowebserver --path=https://github.com/jeremyje/gowebserver/archive/v3.6.0.zip

# Install in your Kubernetes Cluster.
kubectl apply -f https://raw.githubusercontent.com/jeremyje/gowebserver/main/install/kubernetes.yaml
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
* Metrics export to Prometheus.
* Prebuild binaries for all major OSes.

## Downloads

|   OS   | Arch  | Link
|--------|-------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------
|Linux   | amd64 | `curl -O -L https://github.com/jeremyje/gowebserver/releases/download/v3.6.0/server-amd64`
|Linux   | arm   | `curl -O -L https://github.com/jeremyje/gowebserver/releases/download/v3.6.0/server-arm`
|Linux   | arm64 | `curl -O -L https://github.com/jeremyje/gowebserver/releases/download/v3.6.0/server-arm64`
|Windows | amd64 | `$ProgressPreference = 'SilentlyContinue'; Invoke-WebRequest -Uri "https://github.com/jeremyje/gowebserver/releases/download/v3.6.0/server-amd64.exe" -OutFile "server-amd64.exe" -UseBasicParsing`
|macOS   | amd64 | `curl -O -L https://github.com/jeremyje/gowebserver/releases/download/v3.6.0/server-amd64-darwin`
|macOS   | arm64 | `curl -O -L https://github.com/jeremyje/gowebserver/releases/download/v3.6.0/server-arm64-darwin`

## Docker Images

* [gowebserver](https://hub.docker.com/r/jeremyje/gowebserver/tags)

```bash
docker pull docker.io/jeremyje/gowebserver
```

## Build

![example workflow](https://github.com/jeremyje/gowebserver/actions/workflows/deploy.yml/badge.svg) [![Go Report Card](https://goreportcard.com/badge/github.com/jeremyje/gowebserver)](https://goreportcard.com/report/github.com/jeremyje/gowebserver) [![Go Reference](https://pkg.go.dev/badge/github.com/jeremyje/gowebserver.svg)](https://pkg.go.dev/github.com/jeremyje/gowebserver) [![codecov](https://codecov.io/gh/jeremyje/gowebserver/branch/main/graph/badge.svg)](https://codecov.io/gh/jeremyje/gowebserver)

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
git clone git@github.com:jeremyje/gowebserver.git
# Build the Code
make -j$(nproc)
```

## Test

```bash
make test
make bench
```

## Example

Sample code for embedding a HTTP/HTTPS server in your application.

```go
// Package main provides a web server to serve the file system of the host system. This is very insecure!
package main

import (
  "github.com/jeremyje/gowebserver/v2/pkg/gowebserver"
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
