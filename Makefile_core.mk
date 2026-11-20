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

COMMA := ,
EXE =
FIND = find

REPOSITORY_ROOT := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
BUILD_DIR = $(REPOSITORY_ROOT)/build
ARCHIVES_DIR = $(BUILD_DIR)/archives
TOOLCHAIN_DIR = $(BUILD_DIR)/toolchain
THIRDPARTY_DIR = $(REPOSITORY_ROOT)/third_party

TOOLCHAIN_GO = go
CURL = curl --retry 5 --retry-connrefused

# Pinned toolchain releases are installed here and run from there, each tool
# into a directory of its own (see tool_dir below). A version bump installs
# only that tool, and an installed tool is never rebuilt or re-extracted, so a
# build with a populated cache does no toolchain work at all. It defaults to
# build/toolchain, which `make clean` removes as before; CI points it at a
# directory outside the workspace (see .github/workflows/deploy.yaml) that
# survives `actions/checkout`'s `git clean -ffdx` and `make clean`, so each
# release is installed once per runner. `make clean-cache` empties it.
TOOLCHAIN_CACHE ?= $(TOOLCHAIN_DIR)
override TOOLCHAIN_CACHE := $(abspath $(TOOLCHAIN_CACHE))

# Jobs on one runner share TOOLCHAIN_CACHE, so every recipe below works in a
# per-process scratch name next to $@ and renames the finished file into place:
# a concurrent job never sees a partial file, and curl -f keeps an HTTP error
# page from being installed. If the rename fails because another job already
# installed $@ (Windows will not replace a running .exe), its copy is used.
#
# Download the prebuilt binary at URL to $@.
DOWNLOAD_TOOL = mkdir -p "$(dir $@)" && $(CURL) -fL -o "$@.$$$$.tmp" "$(1)" && \
	chmod +x "$@.$$$$.tmp" && { mv -f "$@.$$$$.tmp" "$@" || [ -f "$@" ]; }; \
	status=$$?; rm -f "$@.$$$$.tmp"; exit $$status
# Unpack the release archive at URL and install the file in it named like $@
# (wherever the archive keeps it) as $@.
INSTALL_FROM_ARCHIVE = tmp="$@.$$$$.d" && rm -rf "$$tmp" && mkdir -p "$$tmp" && \
	archive="$$tmp/$(notdir $(1))" && $(CURL) -fL -o "$$archive" "$(1)" && \
	(cd "$$tmp" && case "$$archive" in *.zip) unzip -q "$$archive" ;; *) tar -xf "$$archive" ;; esac) && \
	bin="$$($(FIND) "$$tmp" -type f -name "$(notdir $@)" | head -n 1)" && [ -n "$$bin" ] && \
	chmod +x "$$bin" && { mv -f "$$bin" "$@" || [ -f "$@" ]; }; \
	status=$$?; rm -rf "$$tmp"; exit $$status
# go install PACKAGE@VERSION as $@. Go's module and build caches stay in the
# default GOPATH/GOCACHE, outside the repo: a read-only module cache under
# build/ is what made the Windows runner's `git clean -ffdx` take over a
# minute.
#
# The tool is built for this machine even when the build cross-compiles:
# GOOS, GOARCH and the rest are cleared, or a GOOS from the command line would
# build a binary this machine can't run, and a GOAMD64=v3 one that an older
# CPU sharing the cache can't. Built without cgo, it needs no C toolchain and
# no shared libraries.
GO_INSTALL_TOOL = tmp="$@.$$$$.d" && rm -rf "$$tmp" && mkdir -p "$$tmp" && \
	GOOS= GOARCH= GOARM= GO386= GOAMD64= GOARM64= CGO_ENABLED=0 \
	GOBIN="$$tmp" $(TOOLCHAIN_GO) install $(1) && \
	{ mv -f "$$tmp/$(notdir $@)" "$@" || [ -f "$@" ]; }; \
	status=$$?; rm -rf "$$tmp"; exit $$status

# go_ref VERSION is the @ suffix go install takes for a pinned VERSION.
go_ref = $(if $(filter latest,$(1)),latest,v$(1))

SHORT_SHA = $(or $(SHORT_SHA_),$(eval SHORT_SHA_ := $(if $(GITHUB_SHA),$(shell printf '%.7s' "$(GITHUB_SHA)"),$(shell git rev-parse --short=7 HEAD 2>/dev/null)))$(SHORT_SHA_))
DIRTY_VERSION = v0.0.0-$(or $(SHORT_SHA),unknown)
VERSION = $(or $(VERSION_),$(eval VERSION_ := $(if $(filter tag,$(GITHUB_REF_TYPE)),$(GITHUB_REF_NAME),$(shell git describe --tags 2>/dev/null || echo $(DIRTY_VERSION))))$(VERSION_))
BUILD_DATE = $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
TAG = $(VERSION)

SOURCE_DIRS=$(shell go list ./... | grep -v '/vendor/')

ifeq ($(OS),Windows_NT)
	HOST_OS = windows
	HOST_PLATFORM = windows_amd64
	HOST_ARCH = amd64
	# Give priority to /usr/bin because it conflicts with C:\Windows\system32 within Msys32 environment.
	FIND = /usr/bin/find.exe
	EXE = .exe
	SED_REPLACE = sed -i
else
	UNAME_S := $(shell uname -s)
	UNAME_ARCH := $(shell uname -m)
	ifeq ($(UNAME_S),Linux)
		HOST_OS = linux
		SED_REPLACE = sed -i
		ifneq ($(filter arm aarch64,$(UNAME_ARCH)),)
			HOST_PLATFORM = linux_arm
			HOST_ARCH = arm
		else
			HOST_PLATFORM = linux_amd64
			HOST_ARCH = amd64
		endif
	endif
	ifeq ($(UNAME_S),Darwin)
		HOST_OS = darwin
		SED_REPLACE = sed -i ''
		ifeq ($(UNAME_ARCH),arm64)
			HOST_PLATFORM = darwin_arm64
			HOST_ARCH = arm64
		else
			HOST_PLATFORM = darwin_amd64
			HOST_ARCH = amd64
		endif
	endif
endif

# Many projects, pinned to different versions, share one TOOLCHAIN_CACHE on a
# runner, so each tool goes in a directory named only by the machine, the tool
# and the version:
#
# - the machine's OS and exact CPU (uname -m tells arm64 from 32-bit arm,
#   which HOST_PLATFORM doesn't) separate the cache subdirectories, so the
#   cache can be shared by machines of every kind;
# - the tool and its pinned version decide which binary belongs there.
#
# Nothing else goes into the name: no project, no download URL, no hash of the
# install recipe. Projects made from this template pin the same tools and
# install them the same way, so each one finds another project's install
# already on the runner and installs only what is missing.
#
# Naming by version alone assumes the version identifies the install. If an
# install recipe below changes without a version bump, bump the version or run
# `make clean-cache` to clear what the old recipe left behind; a Go tool
# rebuilt by a newer Go is reused too, for the same reason.
TOOLCHAIN_HOST := $(HOST_OS)-$(shell uname -m)
TOOLCHAIN_HOST_CACHE = $(TOOLCHAIN_CACHE)/$(TOOLCHAIN_HOST)
# tool_dir TOOL,VERSION names the directory TOOL is installed into. A version
# of `latest` names no binary, so its directory carries the date and is
# fetched again each day instead of being kept forever.
tool_dir = $(TOOLCHAIN_HOST_CACHE)/$(1)-$(2)$(if $(filter latest,$(2)),-$(TOOLCHAIN_DATE))
# tool_path TOOL,VERSION names the installed binary of a downloaded tool.
tool_path = $(call tool_dir,$(1),$(2))/$(1)$(EXE)
# go_tool_path TOOL,VERSION names the binary go install builds.
go_tool_path = $(call tool_path,$(1),$(2))

# TOOLCHAIN_DATE is worked out once per make run, when first used.
TOOLCHAIN_DATE = $(or $(TOOLCHAIN_DATE_),$(eval TOOLCHAIN_DATE_ := $(shell date -u +%Y-%m-%d))$(TOOLCHAIN_DATE_))
