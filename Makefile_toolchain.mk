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

# https://github.com/docker/compose/releases
DOCKERCOMPOSE_VERSION = 5.3.1
# https://developer.hashicorp.com/terraform/install
TERRAFORM_VERSION = 1.15.7
# https://github.com/cloudfra/certtool/releases
CERTTOOL_VERSION = 0.2.2
# https://github.com/hadolint/hadolint/releases
HADOLINT_VERSION = 2.14.0
# https://github.com/golang/vuln/releases
GOVULNCHECK_VERSION = 1.5.0
# https://github.com/koalaman/shellcheck/releases
SHELLCHECK_VERSION = 0.11.0
# https://github.com/aquasecurity/trivy/releases
TRIVY_VERSION = 0.72.0
# https://github.com/goptics/vizb/releases
VIZB_VERSION=v0.14.0

ifeq ($(OS),Windows_NT)
	DOCKERCOMPOSE_PACKAGE = https://github.com/docker/compose/releases/download/v$(DOCKERCOMPOSE_VERSION)/docker-compose-windows-x86_64.exe
	TERRAFORM_PACKAGE = https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)/terraform_$(TERRAFORM_VERSION)_windows_amd64.zip
	CERTTOOL_PACKAGE = https://github.com/cloudfra/certtool/releases/download/v$(CERTTOOL_VERSION)/certtool-amd64.exe
	HADOLINT_PACKAGE = https://github.com/hadolint/hadolint/releases/download/v$(HADOLINT_VERSION)/hadolint-windows-x86_64.exe
	SHELLCHECK_PACKAGE = https://github.com/koalaman/shellcheck/releases/download/v$(SHELLCHECK_VERSION)/shellcheck-v$(SHELLCHECK_VERSION).zip
	SHELLCHECK_ARCHIVE = build/archives/shellcheck.zip
	TRIVY_PACKAGE = https://github.com/aquasecurity/trivy/releases/download/v$(TRIVY_VERSION)/trivy_$(TRIVY_VERSION)_windows-64bit.zip
	TRIVY_ARCHIVE = build/archives/trivy.zip
else
	UNAME_S := $(shell uname -s)
	UNAME_ARCH := $(shell uname -m)
	ifeq ($(UNAME_S),Linux)
		ifeq ($(UNAME_ARCH),arm)
			DOCKERCOMPOSE_PACKAGE = https://github.com/docker/compose/releases/download/v$(DOCKERCOMPOSE_VERSION)/docker-compose-linux-aarch64
			TERRAFORM_PACKAGE = https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)/terraform_$(TERRAFORM_VERSION)_linux_arm64.zip
			CERTTOOL_PACKAGE = https://github.com/cloudfra/certtool/releases/download/v$(CERTTOOL_VERSION)/certtool-arm
			HADOLINT_PACKAGE = https://github.com/hadolint/hadolint/releases/download/v$(HADOLINT_VERSION)/hadolint-linux-arm64
			SHELLCHECK_PACKAGE = https://github.com/koalaman/shellcheck/releases/download/v$(SHELLCHECK_VERSION)/shellcheck-v$(SHELLCHECK_VERSION).linux.aarch64.tar.xz
			TRIVY_PACKAGE = https://github.com/aquasecurity/trivy/releases/download/v$(TRIVY_VERSION)/trivy_$(TRIVY_VERSION)_Linux-ARM.tar.gz
		else
			DOCKERCOMPOSE_PACKAGE = https://github.com/docker/compose/releases/download/v$(DOCKERCOMPOSE_VERSION)/docker-compose-linux-x86_64
			TERRAFORM_PACKAGE = https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)/terraform_$(TERRAFORM_VERSION)_linux_amd64.zip
			CERTTOOL_PACKAGE = https://github.com/cloudfra/certtool/releases/download/v$(CERTTOOL_VERSION)/certtool-amd64
			HADOLINT_PACKAGE = https://github.com/hadolint/hadolint/releases/download/v$(HADOLINT_VERSION)/hadolint-linux-x86_64
			SHELLCHECK_PACKAGE = https://github.com/koalaman/shellcheck/releases/download/v$(SHELLCHECK_VERSION)/shellcheck-v$(SHELLCHECK_VERSION).linux.x86_64.tar.xz
			TRIVY_PACKAGE = https://github.com/aquasecurity/trivy/releases/download/v$(TRIVY_VERSION)/trivy_$(TRIVY_VERSION)_Linux-64bit.tar.gz
		endif
		SHELLCHECK_ARCHIVE = build/archives/shellcheck.tar.xz
		TRIVY_ARCHIVE = build/archives/trivy.tar.gz
	endif
	ifeq ($(UNAME_S),Darwin)
		DOCKERCOMPOSE_PACKAGE = https://github.com/docker/compose/releases/download/v$(DOCKERCOMPOSE_VERSION)/docker-compose-darwin-aarch64
		TERRAFORM_PACKAGE = https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)/terraform_$(TERRAFORM_VERSION)_darwin_arm64.zip
		CERTTOOL_PACKAGE = https://github.com/cloudfra/certtool/releases/download/v$(CERTTOOL_VERSION)/certtool-arm64-darwin
		HADOLINT_PACKAGE = https://github.com/hadolint/hadolint/releases/download/v$(HADOLINT_VERSION)/hadolint-macos-arm64
		SHELLCHECK_PACKAGE = https://github.com/koalaman/shellcheck/releases/download/v$(SHELLCHECK_VERSION)/shellcheck-v$(SHELLCHECK_VERSION).darwin.aarch64.tar.xz
		SHELLCHECK_ARCHIVE = build/archives/shellcheck.tar.xz
		TRIVY_PACKAGE = https://github.com/aquasecurity/trivy/releases/download/v$(TRIVY_VERSION)/trivy_$(TRIVY_VERSION)_macOS-ARM64.tar.gz
		TRIVY_ARCHIVE = build/archives/trivy.tar.gz
	endif
endif

ACTIONLINT = build/toolchain/bin/actionlint$(EXE)
CERTTOOL = build/toolchain/bin/certtool$(EXE)
DOCKER_COMPOSE = build/toolchain/bin/docker-compose$(EXE)
GOCOVER_COBERTURA = build/toolchain/bin/gocover-cobertura$(EXE)
GOFUMPT = build/toolchain/bin/gofumpt$(EXE)
GOLANGCI_LINT = build/toolchain/bin/golangci-lint$(EXE)
GOVULNCHECK = build/toolchain/bin/govulncheck$(EXE)
HADOLINT = build/toolchain/bin/hadolint$(EXE)
REVIVE = build/toolchain/bin/revive$(EXE)
SHELLCHECK = build/toolchain/bin/shellcheck$(EXE)
TERRAFORM = build/toolchain/bin/terraform$(EXE)
TFLINT = build/toolchain/bin/tflint$(EXE)
TRIVY = build/toolchain/bin/trivy$(EXE)
VIZB = build/toolchain/bin/vizb$(EXE)

COMMON_TOOLCHAIN = $(ACTIONLINT) $(CERTTOOL) $(DOCKER_COMPOSE) $(GOCOVER_COBERTURA) $(GOFUMPT) $(GOLANGCI_LINT) $(GOVULNCHECK) $(HADOLINT) $(REVIVE) $(SHELLCHECK) $(TERRAFORM) $(TFLINT) $(TRIVY) $(VIZB)

$(ACTIONLINT):
	mkdir -p $(dir $@)
	GOBIN=$(dir $(REPOSITORY_ROOT)/$@) $(GO_WITH_PROXY) install github.com/rhysd/actionlint/cmd/actionlint@latest

$(CERTTOOL):
	mkdir -p $(dir $@)
	$(CURL) -Lo $@ $(CERTTOOL_PACKAGE)
	chmod +x $@

$(DOCKER_COMPOSE):
	mkdir -p $(dir $@)
	$(CURL) -Lo $@ $(DOCKERCOMPOSE_PACKAGE)
	chmod +x $@

$(GOCOVER_COBERTURA):
	mkdir -p $(dir $@)
	GOBIN=$(dir $(REPOSITORY_ROOT)/$@) $(GO_WITH_PROXY) install github.com/t-yuki/gocover-cobertura@latest

# Stricter formatting than `go fmt` (gofmt superset).
$(GOFUMPT):
	mkdir -p $(dir $@)
	GOBIN=$(dir $(REPOSITORY_ROOT)/$@) $(GO_WITH_PROXY) install mvdan.cc/gofumpt@latest

# golangci-lint's own default config (errcheck, govet, ineffassign,
# staticcheck, unused) already covers what a standalone staticcheck run
# would, so it's the only Go correctness linter wired into `lint`.
$(GOLANGCI_LINT):
	mkdir -p $(dir $@)
	GOBIN=$(dir $(REPOSITORY_ROOT)/$@) $(GO_WITH_PROXY) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

$(GOVULNCHECK):
	mkdir -p $(dir $@)
	GOBIN=$(dir $(REPOSITORY_ROOT)/$@) $(GO_WITH_PROXY) install golang.org/x/vuln/cmd/govulncheck@v$(GOVULNCHECK_VERSION)

# Not a Go module, so it's fetched as a prebuilt binary like terraform/docker-compose above.
$(HADOLINT):
	mkdir -p $(dir $@)
	$(CURL) -o $@ -L $(HADOLINT_PACKAGE)
	chmod +x $@

# Style/doc-comment linter; not covered by golangci-lint's default config.
$(REVIVE):
	mkdir -p $(dir $@)
	GOBIN=$(dir $(REPOSITORY_ROOT)/$@) $(GO_WITH_PROXY) install github.com/mgechev/revive@latest

# Also not a Go module. Unlike hadolint, shellcheck ships as an archive (a
# .zip with shellcheck.exe on Windows, a .tar.xz with a versioned
# subdirectory everywhere else), so it needs extracting rather than a
# straight download. actionlint auto-detects it on PATH (which already
# includes this toolchain dir), so no separate lint invocation is needed.
$(SHELLCHECK): $(SHELLCHECK_ARCHIVE)
	mkdir -p $(dir $@)
	mkdir -p $(TOOLCHAIN_DIR)/shellcheck-temp/
ifeq ($(HOST_OS),windows)
	(cd $(TOOLCHAIN_DIR)/shellcheck-temp/ && unzip -q -j $(REPOSITORY_ROOT)/$<)
else
	tar -xJf $< -C $(TOOLCHAIN_DIR)/shellcheck-temp/ --strip-components=1
endif
	cp $(TOOLCHAIN_DIR)/shellcheck-temp/shellcheck$(EXE) $(TOOLCHAIN_BIN)/shellcheck$(EXE)
	chmod +x $(TOOLCHAIN_BIN)/shellcheck$(EXE)
	rm -rf $(TOOLCHAIN_DIR)/shellcheck-temp/

$(TERRAFORM): build/archives/terraform.zip
	mkdir -p $(dir $@)
	mkdir -p $(TOOLCHAIN_DIR)/terraform-temp/
	cp $(ARCHIVES_DIR)/terraform.zip $(TOOLCHAIN_DIR)/terraform-temp/
	(cd $(TOOLCHAIN_DIR)/terraform-temp/ && unzip -q -j terraform.zip)
	cp $(TOOLCHAIN_DIR)/terraform-temp/terraform$(EXE) $(TOOLCHAIN_BIN)/terraform$(EXE)
	rm -rf $(TOOLCHAIN_DIR)/terraform-temp/

$(TFLINT):
	mkdir -p $(dir $@)
	GOBIN=$(dir $(REPOSITORY_ROOT)/$@) $(GO_WITH_PROXY) install github.com/terraform-linters/tflint@latest

# Also not a Go module; ships as an archive like shellcheck, but unlike
# shellcheck the binary sits at the archive root with no versioned
# subdirectory, so no --strip-components/-j is needed.
$(TRIVY): $(TRIVY_ARCHIVE)
	mkdir -p $(dir $@)
	mkdir -p $(TOOLCHAIN_DIR)/trivy-temp/
ifeq ($(HOST_OS),windows)
	(cd $(TOOLCHAIN_DIR)/trivy-temp/ && unzip -q $(REPOSITORY_ROOT)/$<)
else
	tar -xzf $< -C $(TOOLCHAIN_DIR)/trivy-temp/
endif
	cp $(TOOLCHAIN_DIR)/trivy-temp/trivy$(EXE) $(TOOLCHAIN_BIN)/trivy$(EXE)
	chmod +x $(TOOLCHAIN_BIN)/trivy$(EXE)
	rm -rf $(TOOLCHAIN_DIR)/trivy-temp/

$(VIZB):
	# https://github.com/goptics/vizb
	GOBIN=$(TOOLCHAIN_BIN) $(GO_WITH_PROXY) install github.com/goptics/vizb@$(VIZB_VERSION)

build/archives/terraform.zip:
	mkdir -p $(ARCHIVES_DIR)/
	$(CURL) -o $(ARCHIVES_DIR)/terraform.zip -L $(TERRAFORM_PACKAGE)
	touch $@

$(SHELLCHECK_ARCHIVE):
	mkdir -p $(ARCHIVES_DIR)/
	$(CURL) -o $@ -L $(SHELLCHECK_PACKAGE)
	touch $@

$(TRIVY_ARCHIVE):
	mkdir -p $(ARCHIVES_DIR)/
	$(CURL) -o $@ -L $(TRIVY_PACKAGE)
	touch $@
