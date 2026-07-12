# Copyright 2019 Jeremy Edwards
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

include Makefile_testassets.mk

prefix = /usr
bindir = $(prefix)/bin
sharedir = $(prefix)/share
mandir = $(sharedir)/man
man1dir = $(mandir)/man1

RM = rm
ZIP = zip
RAR = rar
TAR = tar
SEVENZIP = 7z
ECHO = @echo
GO = GO111MODULE=on go
DOCKER = DOCKER_CLI_EXPERIMENTAL=enabled docker
KIND = kind
HELM = helm

EXE_EXTENSION =
SHORT_SHA = $(shell git rev-parse --short=7 HEAD | tr -d [:punct:])
DIRTY_VERSION = v0.0.0-$(SHORT_SHA)
VERSION = $(shell git describe --tags || (echo $(DIRTY_VERSION) && exit 1))
BUILD_DATE = $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
TAG := $(VERSION)
PKG := github.com/jeremyje/gowebserver/v2

SOURCE_DIRS=$(shell go list ./... | grep -v '/vendor/')
export PATH := $(PWD)/bin/toolchain:$(PATH):/root/go/bin:/usr/lib/go-1.9/bin:/usr/local/go/bin:/usr/go/bin
BINARY_NAME=gowebserver
MAN_PAGE_NAME=${BINARY_NAME}.1
REPOSITORY_ROOT := $(patsubst %/,%,$(dir $(abspath Makefile)))

REGISTRY = docker.io/jeremyje
GOWEBSERVER_IMAGE = $(REGISTRY)/gowebserver

GO_TOOLCHAIN_DIR = $(dir $(abspath golang.mk))bin/toolchain

# https://go.dev/doc/install/source#environment
LINUX_PLATFORMS = linux_386 linux_amd64 linux_arm_v5 linux_arm_v6 linux_arm_v7 linux_arm64 linux_loong64 linux_s390x linux_ppc64 linux_ppc64le linux_riscv64 linux_mips64le linux_mips linux_mipsle linux_mips64
ANDROID_PLATFORMS = android_arm64 # android_386 android_amd64 android_arm android_arm_v5 android_arm_v6 android_arm_v7
WINDOWS_PLATFORMS = windows_386 windows_amd64 windows_arm64 # windows_arm_v5 windows_arm_v6 windows_arm_v7
MAIN_PLATFORMS = windows_amd64 linux_amd64 linux_arm64
IOS_PLATFORMS = # ios_amd64 ios_arm64
DARWIN_PLATFORMS = darwin_amd64 darwin_arm64
DRAGONFLY_PLATFORMS = dragonfly_amd64
FREEBSD_PLATFORMS = freebsd_386 freebsd_amd64 freebsd_arm_v5 freebsd_arm_v6 freebsd_arm_v7 freebsd_arm64
NETBSD_PLATFORMS = netbsd_amd64 netbsd_arm_v5 netbsd_arm_v6 netbsd_arm_v7 netbsd_arm64 # netbsd_386 
OPENBSD_PLATFORMS = openbsd_386 openbsd_amd64 openbsd_arm_v5 openbsd_arm_v6 openbsd_arm_v7 openbsd_arm64 # openbsd_mips64
PLAN9_PLATFORMS = # plan9_386 plan9_amd64 plan9_arm_v5 plan9_arm_v6 plan9_arm_v7
SOLARIS_PLATFORMS = solaris_amd64
NICHE_PLATFORMS = js_wasm illumos_amd64 aix_ppc64 $(ANDROID_PLATFORMS) $(DARWIN_PLATFORMS) $(IOS_PLATFORMS) $(DRAGONFLY_PLATFORMS) $(FREEBSD_PLATFORMS) $(NETBSD_PLATFORMS) $(OPENBSD_PLATFORMS) $(PLAN9_PLATFORMS) $(SOLARIS_PLATFORMS)
ALL_PLATFORMS = $(LINUX_PLATFORMS) $(WINDOWS_PLATFORMS) $(NICHE_PLATFORMS)
WASM_ASSETS = install/wasm/wasm_exec.js install/wasm/wasm_exec.html install/wasm/gowebserver.wasm
ASSETS = $(TEST_ARCHIVES)
ALL_APPS = gowebserver

ALL_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(ALL_PLATFORMS),bin/go/$(platform)/$(app)$(if $(findstring windows_,$(platform)),.exe,)))
WINDOWS_VERSIONS = 1709 1803 1809 1903 1909 2004 20H2 ltsc2022 ltsc2025
BUILDX_BUILDER = buildx-builder
DOCKER_BUILDER_FLAG = --builder $(BUILDX_BUILDER) --provenance=false
space := $(null) #
comma := ,

ifeq ($(OS),Windows_NT)
	HOST_OS = windows
	HOST_PLATFORM = windows_amd64
	EXE_EXTENSION = .exe
else
	UNAME_S := $(shell uname -s)
	ifeq ($(UNAME_S),Linux)
		HOST_OS = linux
		ifeq ($(UNAME_ARCH),arm)
			HOST_PLATFORM = linux_arm
		else
			HOST_PLATFORM = linux_amd64
		endif
	endif
	ifeq ($(UNAME_S),Darwin)
		HOST_OS = darwin
		HOST_PLATFORM = darwin_amd64
	endif
endif

all: $(ALL_BINARIES) assets
assets: $(ASSETS) $(WASM_ASSETS)

bin/go/%: $(ASSETS)
	GOOS=$(firstword $(subst _, ,$(notdir $(abspath $(dir $@))))) GOARCH=$(word 2, $(subst _, ,$(notdir $(abspath $(dir $@))))) GOARM=$(subst v,,$(word 3, $(subst _, ,$(notdir $(abspath $(dir $@)))))) CGO_ENABLED=0 \
		$(GO) build -o $@ \
		-ldflags '-X $(PKG)/pkg/gowebserver.version=$(VERSION)' \
		cmd/$(basename $(notdir $@))/$(basename $(notdir $@)).go
	touch $@

SHORT_APP_NAMES = server
RELEASE_BINARY_SUFFIXES = amd64 arm arm64 386 arm amd64-darwin arm64-darwin amd64.exe 386.exe
RELEASE_BINARIES = $(foreach appname,$(SHORT_APP_NAMES),$(foreach relbin,$(RELEASE_BINARY_SUFFIXES),bin/release/$(appname)-$(relbin)))

release-binaries: $(RELEASE_BINARIES)

bin/release/server-amd64: bin/go/linux_amd64/gowebserver
	mkdir -p bin/release/ && cp $< $@

bin/release/server-arm: bin/go/linux_arm_v7/gowebserver
	mkdir -p bin/release/ && cp $< $@

bin/release/server-arm64: bin/go/linux_arm64/gowebserver
	mkdir -p bin/release/ && cp $< $@

bin/release/server-386: bin/go/linux_386/gowebserver
	mkdir -p bin/release/ && cp $< $@

bin/release/server-amd64-darwin: bin/go/darwin_amd64/gowebserver
	mkdir -p bin/release/ && cp $< $@

bin/release/server-arm64-darwin: bin/go/darwin_arm64/gowebserver
	mkdir -p bin/release/ && cp $< $@

bin/release/server-amd64.exe: bin/go/windows_amd64/gowebserver.exe
	mkdir -p bin/release/ && cp $< $@

bin/release/server-386.exe: bin/go/windows_386/gowebserver.exe
	mkdir -p bin/release/ && cp $< $@

bin/release/server-arm64.exe: bin/go/windows_arm64/gowebserver.exe
	mkdir -p bin/release/ && cp $< $@

dist: bin/release.tar.gz

bin/release.tar.gz: $(ALL_BINARIES)
	mkdir -p bin/
	cd bin/go/; $(TAR) -I 'gzip -9' -cf ../release.tar.gz *

lint: $(ASSETS)
	$(GO) fmt ${SOURCE_DIRS}
	$(GO) vet ${SOURCE_DIRS}

clean:
	$(RM) -f ${BINARY_NAME} ${BINARY_NAME}-* cert.pem rsa.pem release.tar.gz $(ASSETS) *.tar.bz2 *.snap
	$(RM) -rf parts/ prime/ snap/.snapcraft/ stage/ *.snap
	$(RM) -rf upload/
	$(RM) -rf toolchain/
	$(RM) -rf bin/
	$(RM) -f install/wasm/wasm_exec.js
	$(RM) -f install/wasm/wasm_exec.html

check: test

install/wasm/gowebserver.wasm: bin/go/js_wasm/gowebserver
	mkdir -p $(dir $@)
	cp -f $< $@

install/wasm/wasm_exec.js:
	mkdir -p $(dir $@)
	cp -f $(shell go env GOROOT)/lib/wasm/wasm_exec.js $@

install/wasm/wasm_exec.html:
	mkdir -p $(dir $@)
	cp -f $(shell go env GOROOT)/misc/wasm/wasm_exec.html $@
	sed -i 's/..\/..\/lib\/wasm\///g' $@

test: $(ASSETS)
	$(GO) test -race ${SOURCE_DIRS}

test-10: $(ASSETS)
	$(GO) test -race ${SOURCE_DIRS} -count 10

coverage: $(ASSETS)
	$(GO) test -cover ${SOURCE_DIRS}

coverage.txt: $(ASSETS)
	for sfile in ${SOURCE_DIRS} ; do \
		go test -race "$$sfile" -coverprofile=package.coverage -covermode=atomic; \
		if [ -f package.coverage ]; then \
			cat package.coverage >> coverage.txt; \
			$(RM) package.coverage; \
		fi; \
	done

bench: benchmark
benchmark: $(ASSETS)
	$(GO) test -benchmem -bench=. ${SOURCE_DIRS}

test-all: test test-10 benchmark coverage

run: clean assets lint
	$(GO) run cmd/gowebserver/gowebserver.go -http.port 8181 -path=. -verbose -debug -enhancedindex=true

multirun: clean assets lint
	$(GO) run cmd/gowebserver/gowebserver.go -path=./cmd/,./pkg/,. -verbose=true -servepath=mains,code,root -http.port 8181 -enhancedindex=true -debug

install: gowebserver
	mkdir -p $(DESTDIR)$(bindir) $(DESTDIR)$(man1dir)
	install ${BINARY_NAME} $(DESTDIR)$(bindir)
	install -m 0644 ${MAN_PAGE_NAME} $(DESTDIR)$(man1dir)

deps:
	$(GO) get -u ./...
	$(GO) mod tidy
	$(GO) mod download

ensure-builder:
	-$(DOCKER) buildx create --name $(BUILDX_BUILDER)

ALL_IMAGES = $(GOWEBSERVER_IMAGE)
# https://github.com/docker-library/official-images#architectures-other-than-amd64
images: DOCKER_PUSH = --push
images: linux-images windows-images
	-$(DOCKER) manifest rm $(GOWEBSERVER_IMAGE):$(TAG)

	for image in $(ALL_IMAGES) ; do \
		$(DOCKER) manifest create $$image:$(TAG) $(foreach winver,$(WINDOWS_VERSIONS),$${image}:$(TAG)-windows_amd64-$(winver)) $(foreach platform,$(LINUX_PLATFORMS),$${image}:$(TAG)-$(platform)) ; \
		for winver in $(WINDOWS_VERSIONS) ; do \
			windows_version=`$(DOCKER) manifest inspect mcr.microsoft.com/windows/nanoserver:$${winver} | jq -r '.manifests[0].platform["os.version"]'`; \
			$(DOCKER) manifest annotate --os-version $${windows_version} $${image}:$(TAG) $${image}:$(TAG)-windows_amd64-$${winver} ; \
		done ; \
		$(DOCKER) manifest push $$image:$(TAG) ; \
	done

gowebserver-image: bin/go/linux_amd64/gowebserver
	$(DOCKER) build --build-arg BINARY_PATH=$< -f cmd/gowebserver/Dockerfile -t $(GOWEBSERVER_IMAGE):localtest .

ALL_LINUX_IMAGES = $(foreach app,$(ALL_APPS),$(foreach platform,$(LINUX_PLATFORMS),linux-image-$(app)-$(platform)))
linux-images: $(ALL_LINUX_IMAGES)

linux-image-gowebserver-%: bin/go/%/gowebserver ensure-builder
	$(DOCKER) buildx build $(DOCKER_BUILDER_FLAG) --platform $(subst _,/,$*) --build-arg BINARY_PATH=$< -f cmd/gowebserver/Dockerfile -t $(GOWEBSERVER_IMAGE):$(TAG)-$* . $(DOCKER_PUSH)

ALL_WINDOWS_IMAGES = $(foreach app,$(ALL_APPS),$(foreach winver,$(WINDOWS_VERSIONS),windows-image-$(app)-$(winver)))
windows-images: $(ALL_WINDOWS_IMAGES)

windows-image-gowebserver-%: bin/go/windows_amd64/gowebserver.exe ensure-builder
	$(DOCKER) buildx build $(DOCKER_BUILDER_FLAG) --platform windows/amd64 -f cmd/gowebserver/Dockerfile.windows --build-arg WINDOWS_VERSION=$* -t $(GOWEBSERVER_IMAGE):$(TAG)-windows_amd64-$* . $(DOCKER_PUSH)

presubmit: clean check coverage all release-binaries images

kind-create:
	$(KIND) create cluster --config=$(REPOSITORY_ROOT)/install/kind/kind-cluster.yaml
# kubectl config set clusters.kind-kind.server https://192.168.86.36:6443

kind-delete:
	$(KIND) delete cluster

install/kubernetes.yaml:
	$(HELM) template gowebserver install/helm > install/kubernetes.yaml

template: install/kubernetes.yaml

test-codecov:
	curl -X POST --data-binary @codecov.yml https://codecov.io/validate

run-wasm: clean assets lint
	$(GO) run cmd/gowebserver/gowebserver.go -http.port 8181 -path=install/wasm/ -verbose

.PHONY : all assets dist lint clean check test test-10 coverage bench benchmark test-all install run deps presubmit gowebserver-image
