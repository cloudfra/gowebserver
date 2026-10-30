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

include Makefile_core.mk

# https://github.com/rhysd/actionlint/releases
ACTIONLINT_VERSION = 1.7.12
# https://github.com/docker/compose/releases
DOCKERCOMPOSE_VERSION = 5.6.1
# https://developer.hashicorp.com/terraform/install
TERRAFORM_VERSION = 1.17.1
# https://github.com/cloudfra/certtool/releases
CERTTOOL_VERSION = 0.8.1
# https://github.com/hadolint/hadolint/releases
HADOLINT_VERSION = 2.15.1
# https://github.com/t-yuki/gocover-cobertura/releases
GOCOVER_COBERTURA_VERSION = 0.0.0-20180217150009-aaee18c8195c
# https://github.com/mvdan/gofumpt/releases
GOFUMPT_VERSION = 0.12.0
# https://github.com/golangci/golangci-lint/releases
GOLANGCI_LINT_VERSION = 2.15.0
# https://github.com/golang/vuln/releases
GOVULNCHECK_VERSION = 1.9.0
# https://github.com/mgechev/revive/releases
REVIVE_VERSION = 1.17.1
# https://github.com/rvben/rumdl/releases
RUMDL_VERSION = 0.3.2
# https://github.com/koalaman/shellcheck/releases
SHELLCHECK_VERSION = 0.11.0
# https://github.com/terraform-linters/tflint/releases
TFLINT_VERSION = 0.65.0
# https://github.com/aquasecurity/trivy/releases
TRIVY_VERSION = 0.76.1
# https://github.com/goptics/vizb/releases
VIZB_VERSION = 0.23.1
# https://developer.android.com/ndk/downloads
ANDROID_NDK_VERSION = r30
# Minimum Android API level for the NDK's clang target wrappers; 21 is the
# lowest the NDK supports.
ANDROID_API = 21

ifeq ($(OS),Windows_NT)
	DOCKERCOMPOSE_PACKAGE = https://github.com/docker/compose/releases/download/v$(DOCKERCOMPOSE_VERSION)/docker-compose-windows-x86_64.exe
	TERRAFORM_PACKAGE = https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)/terraform_$(TERRAFORM_VERSION)_windows_amd64.zip
	CERTTOOL_PACKAGE = https://github.com/cloudfra/certtool/releases/download/v$(CERTTOOL_VERSION)/certtool-windows_amd64.exe
	HADOLINT_PACKAGE = https://github.com/hadolint/hadolint/releases/download/v$(HADOLINT_VERSION)/hadolint-windows-x86_64.exe
	RUMDL_PACKAGE = https://github.com/rvben/rumdl/releases/download/v$(RUMDL_VERSION)/rumdl-v$(RUMDL_VERSION)-x86_64-pc-windows-msvc.zip
	SHELLCHECK_PACKAGE = https://github.com/koalaman/shellcheck/releases/download/v$(SHELLCHECK_VERSION)/shellcheck-v$(SHELLCHECK_VERSION).zip
	TRIVY_PACKAGE = https://github.com/aquasecurity/trivy/releases/download/v$(TRIVY_VERSION)/trivy_$(TRIVY_VERSION)_windows-64bit.zip
else
	UNAME_S := $(shell uname -s)
	UNAME_ARCH := $(shell uname -m)
	ifeq ($(UNAME_S),Linux)
		ifeq ($(UNAME_ARCH),arm)
			DOCKERCOMPOSE_PACKAGE = https://github.com/docker/compose/releases/download/v$(DOCKERCOMPOSE_VERSION)/docker-compose-linux-aarch64
			TERRAFORM_PACKAGE = https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)/terraform_$(TERRAFORM_VERSION)_linux_arm64.zip
			CERTTOOL_PACKAGE = https://github.com/cloudfra/certtool/releases/download/v$(CERTTOOL_VERSION)/certtool-linux_arm64
			HADOLINT_PACKAGE = https://github.com/hadolint/hadolint/releases/download/v$(HADOLINT_VERSION)/hadolint-linux-arm64
			RUMDL_PACKAGE = https://github.com/rvben/rumdl/releases/download/v$(RUMDL_VERSION)/rumdl-v$(RUMDL_VERSION)-aarch64-unknown-linux-gnu.tar.gz
			SHELLCHECK_PACKAGE = https://github.com/koalaman/shellcheck/releases/download/v$(SHELLCHECK_VERSION)/shellcheck-v$(SHELLCHECK_VERSION).linux.aarch64.tar.xz
			TRIVY_PACKAGE = https://github.com/aquasecurity/trivy/releases/download/v$(TRIVY_VERSION)/trivy_$(TRIVY_VERSION)_Linux-ARM.tar.gz
		else
			DOCKERCOMPOSE_PACKAGE = https://github.com/docker/compose/releases/download/v$(DOCKERCOMPOSE_VERSION)/docker-compose-linux-x86_64
			TERRAFORM_PACKAGE = https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)/terraform_$(TERRAFORM_VERSION)_linux_amd64.zip
			CERTTOOL_PACKAGE = https://github.com/cloudfra/certtool/releases/download/v$(CERTTOOL_VERSION)/certtool-linux_amd64
			HADOLINT_PACKAGE = https://github.com/hadolint/hadolint/releases/download/v$(HADOLINT_VERSION)/hadolint-linux-x86_64
			RUMDL_PACKAGE = https://github.com/rvben/rumdl/releases/download/v$(RUMDL_VERSION)/rumdl-v$(RUMDL_VERSION)-x86_64-unknown-linux-gnu.tar.gz
			SHELLCHECK_PACKAGE = https://github.com/koalaman/shellcheck/releases/download/v$(SHELLCHECK_VERSION)/shellcheck-v$(SHELLCHECK_VERSION).linux.x86_64.tar.xz
			TRIVY_PACKAGE = https://github.com/aquasecurity/trivy/releases/download/v$(TRIVY_VERSION)/trivy_$(TRIVY_VERSION)_Linux-64bit.tar.gz
		endif
	endif
	ifeq ($(UNAME_S),Darwin)
		DOCKERCOMPOSE_PACKAGE = https://github.com/docker/compose/releases/download/v$(DOCKERCOMPOSE_VERSION)/docker-compose-darwin-aarch64
		TERRAFORM_PACKAGE = https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)/terraform_$(TERRAFORM_VERSION)_darwin_arm64.zip
		CERTTOOL_PACKAGE = https://github.com/cloudfra/certtool/releases/download/v$(CERTTOOL_VERSION)/certtool-darwin_arm64
		HADOLINT_PACKAGE = https://github.com/hadolint/hadolint/releases/download/v$(HADOLINT_VERSION)/hadolint-macos-arm64
		RUMDL_PACKAGE = https://github.com/rvben/rumdl/releases/download/v$(RUMDL_VERSION)/rumdl-v$(RUMDL_VERSION)-aarch64-apple-darwin.tar.gz
		SHELLCHECK_PACKAGE = https://github.com/koalaman/shellcheck/releases/download/v$(SHELLCHECK_VERSION)/shellcheck-v$(SHELLCHECK_VERSION).darwin.aarch64.tar.xz
		TRIVY_PACKAGE = https://github.com/aquasecurity/trivy/releases/download/v$(TRIVY_VERSION)/trivy_$(TRIVY_VERSION)_macOS-ARM64.tar.gz
	endif
endif

# Each tool is installed once into TOOLCHAIN_CACHE (see Makefile_core.mk), in a
# directory keyed by its version and source, and run from there. A Go tool's
# source is its X_GO_PACKAGE (not X_PACKAGE, which update-project.sh takes to be
# a download URL to check).
ACTIONLINT_GO_PACKAGE = github.com/rhysd/actionlint/cmd/actionlint
ACTIONLINT = $(call go_tool_path,actionlint,$(ACTIONLINT_VERSION),$(ACTIONLINT_GO_PACKAGE))
CERTTOOL = $(call tool_path,certtool,$(CERTTOOL_VERSION),$(CERTTOOL_PACKAGE))
DOCKER_COMPOSE = $(call tool_path,docker-compose,$(DOCKERCOMPOSE_VERSION),$(DOCKERCOMPOSE_PACKAGE))
GOCOVER_COBERTURA_GO_PACKAGE = github.com/t-yuki/gocover-cobertura
GOCOVER_COBERTURA = $(call go_tool_path,gocover-cobertura,$(GOCOVER_COBERTURA_VERSION),$(GOCOVER_COBERTURA_GO_PACKAGE))
GOFUMPT_GO_PACKAGE = mvdan.cc/gofumpt
GOFUMPT = $(call go_tool_path,gofumpt,$(GOFUMPT_VERSION),$(GOFUMPT_GO_PACKAGE))
GOLANGCI_LINT_GO_PACKAGE = github.com/golangci/golangci-lint/v2/cmd/golangci-lint
GOLANGCI_LINT = $(call go_tool_path,golangci-lint,$(GOLANGCI_LINT_VERSION),$(GOLANGCI_LINT_GO_PACKAGE))
GOVULNCHECK_GO_PACKAGE = golang.org/x/vuln/cmd/govulncheck
GOVULNCHECK = $(call go_tool_path,govulncheck,$(GOVULNCHECK_VERSION),$(GOVULNCHECK_GO_PACKAGE))
HADOLINT = $(call tool_path,hadolint,$(HADOLINT_VERSION),$(HADOLINT_PACKAGE))
REVIVE_GO_PACKAGE = github.com/mgechev/revive
REVIVE = $(call go_tool_path,revive,$(REVIVE_VERSION),$(REVIVE_GO_PACKAGE))
RUMDL = $(call tool_path,rumdl,$(RUMDL_VERSION),$(RUMDL_PACKAGE))
SHELLCHECK = $(call tool_path,shellcheck,$(SHELLCHECK_VERSION),$(SHELLCHECK_PACKAGE))
TERRAFORM = $(call tool_path,terraform,$(TERRAFORM_VERSION),$(TERRAFORM_PACKAGE))
TFLINT_GO_PACKAGE = github.com/terraform-linters/tflint
TFLINT = $(call go_tool_path,tflint,$(TFLINT_VERSION),$(TFLINT_GO_PACKAGE))
TRIVY = $(call tool_path,trivy,$(TRIVY_VERSION),$(TRIVY_PACKAGE))
VIZB_GO_PACKAGE = github.com/goptics/vizb
VIZB = $(call go_tool_path,vizb,$(VIZB_VERSION),$(VIZB_GO_PACKAGE))

# The Android NDK supplies the C toolchain that android/386, android/amd64 and
# android/arm need for cgo external linking. It is ~700 MB, so it is not part
# of COMMON_TOOLCHAIN (`make tools`); only those binaries depend on it (see
# Makefile_build.mk). Only the linux-x86_64 host package is wired up.
ANDROID_NDK = $(call tool_dir,android-ndk,$(ANDROID_NDK_VERSION),$(ANDROID_NDK_PACKAGE))/android-ndk
ANDROID_NDK_BIN = $(ANDROID_NDK)/toolchains/llvm/prebuilt/linux-x86_64/bin
ANDROID_NDK_CLANG = $(ANDROID_NDK_BIN)/clang
# Kept as a variable (rather than inline in the download rule) so
# scripts/template-go/update-project.sh can expand the exact URL this build
# would fetch and HEAD-check a candidate NDK release before rewriting the pin.
ANDROID_NDK_PACKAGE = https://dl.google.com/android/repository/android-ndk-$(ANDROID_NDK_VERSION)-linux.zip

COMMON_TOOLCHAIN = $(ACTIONLINT) $(CERTTOOL) $(DOCKER_COMPOSE) $(GOCOVER_COBERTURA) $(GOFUMPT) $(GOLANGCI_LINT) $(GOVULNCHECK) $(HADOLINT) $(REVIVE) $(RUMDL) $(SHELLCHECK) $(TERRAFORM) $(TFLINT) $(TRIVY) $(VIZB)

$(ACTIONLINT):
	$(call GO_INSTALL_TOOL,$(ACTIONLINT_GO_PACKAGE)@$(call go_ref,$(ACTIONLINT_VERSION)))

$(CERTTOOL):
	$(call DOWNLOAD_TOOL,$(CERTTOOL_PACKAGE))

$(DOCKER_COMPOSE):
	$(call DOWNLOAD_TOOL,$(DOCKERCOMPOSE_PACKAGE))

$(GOCOVER_COBERTURA):
	$(call GO_INSTALL_TOOL,$(GOCOVER_COBERTURA_GO_PACKAGE)@$(call go_ref,$(GOCOVER_COBERTURA_VERSION)))

# Stricter formatting than `go fmt` (gofmt superset).
$(GOFUMPT):
	$(call GO_INSTALL_TOOL,$(GOFUMPT_GO_PACKAGE)@$(call go_ref,$(GOFUMPT_VERSION)))

# golangci-lint's own default config (errcheck, govet, ineffassign,
# staticcheck, unused) already covers what a standalone staticcheck run
# would, so it's the only Go correctness linter wired into `lint`.
$(GOLANGCI_LINT):
	$(call GO_INSTALL_TOOL,$(GOLANGCI_LINT_GO_PACKAGE)@$(call go_ref,$(GOLANGCI_LINT_VERSION)))

$(GOVULNCHECK):
	$(call GO_INSTALL_TOOL,$(GOVULNCHECK_GO_PACKAGE)@$(call go_ref,$(GOVULNCHECK_VERSION)))

# Not a Go module, so it's fetched as a prebuilt binary like docker-compose above.
$(HADOLINT):
	$(call DOWNLOAD_TOOL,$(HADOLINT_PACKAGE))

# Style/doc-comment linter; not covered by golangci-lint's default config.
$(REVIVE):
	$(call GO_INSTALL_TOOL,$(REVIVE_GO_PACKAGE)@$(call go_ref,$(REVIVE_VERSION)))

$(RUMDL):
	$(call INSTALL_FROM_ARCHIVE,$(RUMDL_PACKAGE))

# Also not a Go module. Unlike hadolint, shellcheck ships as an archive (a
# .zip with shellcheck.exe on Windows, a .tar.xz with a versioned
# subdirectory everywhere else); INSTALL_FROM_ARCHIVE finds the binary in
# either layout. lint-yaml passes its path to actionlint.
$(SHELLCHECK):
	$(call INSTALL_FROM_ARCHIVE,$(SHELLCHECK_PACKAGE))

$(TERRAFORM):
	$(call INSTALL_FROM_ARCHIVE,$(TERRAFORM_PACKAGE))

$(TFLINT):
	$(call GO_INSTALL_TOOL,$(TFLINT_GO_PACKAGE)@$(call go_ref,$(TFLINT_VERSION)))

# Also not a Go module; ships as an archive like shellcheck.
$(TRIVY):
	$(call INSTALL_FROM_ARCHIVE,$(TRIVY_PACKAGE))

# https://github.com/goptics/vizb
$(VIZB):
	$(call GO_INSTALL_TOOL,$(VIZB_GO_PACKAGE)@$(call go_ref,$(VIZB_VERSION)))

# The NDK is a whole directory tree rather than one binary, so it is unpacked
# next to its final location and renamed into place as a directory.
$(ANDROID_NDK_CLANG):
	tmp="$(ANDROID_NDK).$$$$.d" && rm -rf "$$tmp" && mkdir -p "$$tmp" && \
	$(CURL) -fL -o "$$tmp/ndk.zip" "$(ANDROID_NDK_PACKAGE)" && \
	(cd "$$tmp" && unzip -q ndk.zip) && \
	{ [ -d "$(ANDROID_NDK)" ] || mv "$$tmp/android-ndk-$(ANDROID_NDK_VERSION)" "$(ANDROID_NDK)"; }; \
	status=$$?; rm -rf "$$tmp"; exit $$status
