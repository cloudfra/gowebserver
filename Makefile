# Copyright 2026 Cloudfra
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

REGISTRY = docker.io/cloudfra
PROTOS =
TEST_ASSETS = $(TEST_ARCHIVES)
ASSETS = $(PROTOS)
GO_PACKAGE = github.com/cloudfra/gowebserver
ALL_APPS = gowebserver
PRODUCTION=1
RUMDL_IGNORE = internal/gowebserver/testing/testassets/**
PLAN9_PLATFORMS = 
IOS_PLATFORMS = 
NETBSD_PLATFORMS = netbsd/amd64 netbsd/arm64 netbsd/arm/v5 netbsd/arm/v6 netbsd/arm/v7 # netbsd/386 

JAVASCRIPT_DIR = pkg/gowebserver/template
JAVASCRIPT_SERVER = build/bin/$(HOST_OS)/$(HOST_ARCH)/gowebserver$(EXE)

# Before Makefile_build.mk: it adds test-js to the TEST_TARGETS that the test
# rule there is made from.
include Makefile_javascript.mk
include Makefile_build.mk

# ffmpeg, for the tests that run the real one (thumbnails of photos and
# videos), which find it on PATH and are skipped without it: BtbN's GPL build
# of the FFMPEG_VERSION release branch, fetched by pkg/ffmpeg, which checks its
# checksum. It's installed into the toolchain cache like the template's tools
# (see TOOLCHAIN_CACHE in Makefile_core.mk), and pkg/ffmpeg keeps the download
# in the user cache directory. BtbN has no build for macOS or 32-bit arm, where
# the tests that need it are skipped. It's set here rather than in the
# template-managed Makefile_toolchain.mk, which a template sync replaces, and
# below the include so the ffmpeg rule isn't the default goal.
FFMPEG_VERSION = 9.0
FFMPEG_BUILD_linux-x86_64 = linux64-gpl-$(FFMPEG_VERSION).tar.xz
FFMPEG_BUILD_linux-aarch64 = linuxarm64-gpl-$(FFMPEG_VERSION).tar.xz
FFMPEG_BUILD_linux-arm64 = $(FFMPEG_BUILD_linux-aarch64)
FFMPEG_BUILD_windows-x86_64 = win64-gpl-$(FFMPEG_VERSION).zip
FFMPEG_PACKAGE = $(if $(FFMPEG_BUILD_$(TOOLCHAIN_HOST)),https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n$(FFMPEG_VERSION)-latest-$(FFMPEG_BUILD_$(TOOLCHAIN_HOST)))
FFMPEG := $(call tool_path,ffmpeg,$(FFMPEG_VERSION),$(FFMPEG_PACKAGE))
# Only ffmpeg's directory is put on PATH; every other tool is run by its path.
export PATH := $(patsubst %/,%,$(dir $(FFMPEG))):$(PATH)

ifneq ($(FFMPEG_PACKAGE),)
tools: $(FFMPEG)
endif

$(FFMPEG):
	$(GO) run ./internal/tools/installffmpeg -url "$(FFMPEG_PACKAGE)" -o "$@.$$$$.tmp" && \
	{ mv -f "$@.$$$$.tmp" "$@" || [ -f "$@" ]; }; \
	status=$$?; rm -f "$@.$$$$.tmp"; exit $$status

run: clean assets lint
	$(GO) run cmd/gowebserver/gowebserver.go -http.port 8181 -path=. -verbose -debug -enhancedindex=true

multirun: clean assets lint
	$(GO) run cmd/gowebserver/gowebserver.go -path=./cmd/,./pkg/,. -verbose=true -servepath=mains,code,root -http.port 8181 -enhancedindex=true -debug

run-wasm: clean assets lint build/bin/js/wasm/gowebserver build/bin/js/wasm/gowebserver.html
	$(GO) run cmd/gowebserver/gowebserver.go -http.port 8181 -path=$(REPOSITORY_ROOT)/build/bin/js/wasm/ -verbose

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

.PHONY : run multirun run-wasm kind-create kind-delete template test-codecov
