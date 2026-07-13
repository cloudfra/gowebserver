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
include Makefile_proto.mk
include Makefile_toolchain.mk

DOCKER_PUSH = --push

GO_WITH_PROXY = go
GO = GOPROXY=off go
GO_RACE=-race

DOCKER = docker
TAR = tar

GO_TEST_COUNT = 25

LINUX_PLATFORMS = linux/386 linux/amd64 linux/arm/v5 linux/arm/v6 linux/arm/v7 linux/arm64 linux/loong64 linux/s390x linux/ppc64 linux/ppc64le linux/riscv64 linux/mips64le linux/mips linux/mipsle linux/mips64
ANDROID_PLATFORMS = android/arm64 # android/386 android/amd64 android/arm android/arm/v5 android/arm/v6 android/arm/v7
WINDOWS_PLATFORMS = windows/386 windows/amd64 windows/arm64 # windows/arm/v5 windows/arm/v6 windows/arm/v7
MAIN_PLATFORMS = windows/amd64 linux/amd64 linux/arm64
IOS_PLATFORMS = # ios/amd64 ios/arm64
DARWIN_PLATFORMS = darwin/amd64 darwin/arm64
DRAGONFLY_PLATFORMS = dragonfly/amd64
FREEBSD_PLATFORMS = freebsd/386 freebsd/amd64 freebsd/arm/v5 freebsd/arm/v6 freebsd/arm/v7 freebsd/arm64
NETBSD_PLATFORMS = netbsd/amd64 netbsd/arm64 netbsd/arm/v5 netbsd/arm/v6 netbsd/arm/v7 # netbsd/386
OPENBSD_PLATFORMS = openbsd/386 openbsd/amd64 openbsd/arm/v5 openbsd/arm/v6 openbsd/arm/v7 openbsd/arm64 # openbsd/mips64
PLAN9_PLATFORMS = # plan9/386 plan9/amd64 plan9/arm/v5 plan9/arm/v6 plan9/arm/v7
SOLARIS_PLATFORMS = solaris/amd64
NICHE_PLATFORMS = js/wasm illumos/amd64 aix/ppc64 $(ANDROID_PLATFORMS) $(DARWIN_PLATFORMS) $(IOS_PLATFORMS) $(DRAGONFLY_PLATFORMS) $(FREEBSD_PLATFORMS) $(NETBSD_PLATFORMS) $(OPENBSD_PLATFORMS) $(PLAN9_PLATFORMS) $(SOLARIS_PLATFORMS)
ALL_PLATFORMS = $(LINUX_PLATFORMS) $(WINDOWS_PLATFORMS) $(NICHE_PLATFORMS)
RELEASE_PLATFORMS = linux/amd64 linux/amd64 windows/amd64 windows/arm64 darwin/arm64

MAIN_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(MAIN_PLATFORMS),build/bin/$(platform)/$(app)$(if $(findstring windows,$(platform)),.exe,)))
WINDOWS_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(WINDOWS_PLATFORMS),build/bin/$(platform)/$(app)$(if $(findstring windows,$(platform)),.exe,)))
WASM_ASSETS = $(foreach app,$(ALL_APPS),build/bin/js/wasm/$(app).html)
ALL_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(ALL_PLATFORMS),build/bin/$(platform)/$(app)$(if $(findstring windows,$(platform)),.exe,))) $(WASM_ASSETS)

CODESIGN_CERT ?= build/certs/codesign.crt
CODESIGN_KEY ?= build/certs/codesign.key

RELEASE_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(RELEASE_PLATFORMS),build/release/$(app)-$(subst /,_,$(platform))$(if $(findstring windows,$(platform)),.exe,)))

WINDOWS_VERSIONS = 1709 1803 1809 1903 1909 2004 20H2 ltsc2022 ltsc2025
BUILDX_BUILDER = buildx-builder
# --provenance/--sbom=false: buildx defaults to emitting an image index (manifest
# list) wrapping a single-platform build to carry attestations. `docker manifest
# create`/`annotate` can't reference into a nested index, which breaks the
# per-platform tags merged by the `images` target below.
DOCKER_EXTRA_FLAGS = --builder $(BUILDX_BUILDER) --provenance=false --sbom=false
# Both Dockerfiles declare BUILD_DATE/VCS_REF/BUILD_VERSION build-args for
# their OCI/label-schema LABELs; without these, every image ships with empty
# version/created/vcs-ref labels.
DOCKER_LABEL_ARGS = --build-arg BUILD_DATE=$(BUILD_DATE) --build-arg VCS_REF=$(SHORT_SHA) --build-arg BUILD_VERSION=$(VERSION)

TOOLCHAIN = $(COMMON_TOOLCHAIN) $(PROTOC_TOOLCHAIN)
tools: $(TOOLCHAIN)

all: no-sudo $(ALL_BINARIES)
assets: $(ASSETS)
protos: $(PROTOS)
windows-binaries: $(WINDOWS_BINARIES)

build/packages/%-binaries.zip: $(ALL_BINARIES)
	mkdir -p "$(dir $@)"
	(cd "$(REPOSITORY_ROOT)build/bin/$*/"; zip -qr9 "$(REPOSITORY_ROOT)/$@" *)
	touch "$(REPOSITORY_ROOT)/$@"

release-binaries: $(RELEASE_BINARIES)

build/packages/release.tar.gz: $(RELEASE_BINARIES)
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)/build/release/"; tar -cvf - * | gzip -9 - > "$(REPOSITORY_ROOT)/$@"
	touch "$(REPOSITORY_ROOT)/$@"

build/packages/all-binaries.tar.gz: $(ALL_BINARIES)
	mkdir -p "$(dir $@)"
	cd "$(REPOSITORY_ROOT)build/bin/"; tar -cvf - * | gzip -9 - > "$(REPOSITORY_ROOT)/$@"
	touch "$(REPOSITORY_ROOT)/$@"

ifeq ($(CODESIGN_CERT)|$(CODESIGN_KEY),build/certs/codesign.crt|build/certs/codesign.key)
build/certs/codesign.crt build/certs/codesign.key &: $(CERTTOOL)
	mkdir -p "$(dir $(CODESIGN_CERT))"
	"$(TOOLCHAIN_BIN)/certtool$(EXE)" --code-sign --target=linux --public-certificate="$(CODESIGN_CERT)" --private-key="$(CODESIGN_KEY)"
endif

build/bin/%: $(ASSETS)
	GOOS=$(word 3, $(subst /, ,$(dir $@))) GOARCH=$(word 4, $(subst /, ,$(dir $@))) GOARM=$(subst v,,$(word 5, $(subst /, ,$(dir $@)))) CGO_ENABLED=0 $(GO) build -ldflags="-X '$(GO_PACKAGE)/internal.version=$(VERSION)' -X '$(GO_PACKAGE)/internal.buildstamp=$(BUILD_DATE)'" -o "$(REPOSITORY_ROOT)/$@" cmd/$(basename $(notdir $@))/$(basename $(notdir $@)).go
	touch "$(REPOSITORY_ROOT)/$@"

build/bin/js/wasm/%.html: build/bin/js/wasm/% build/bin/js/wasm/wasm_exec.js
	mkdir -p "$(dir $@)"
	cp -f "$(shell go env GOROOT)/misc/wasm/wasm_exec.html" "$(REPOSITORY_ROOT)/$@"
	sed -i 's/..\/..\/lib\/wasm\///g' "$(REPOSITORY_ROOT)/$@"
	sed -i 's/test\.wasm/$*/g' "$(REPOSITORY_ROOT)/$@"

build/bin/js/wasm/wasm_exec.js:
	mkdir -p "$(dir $@)"
	cp -f "$(shell go env GOROOT)/lib/wasm/wasm_exec.js" "$(REPOSITORY_ROOT)/$@"

wasm-binaries: $(WASM_BINARIES)

lint: lint-go lint-terraform lint-docker lint-yaml lint-shell lint-vuln

ifneq ($(wildcard install/terraform),)
lint-terraform: build/toolchain/bin/terraform$(EXE) build/toolchain/bin/tflint$(EXE) build/toolchain/bin/trivy$(EXE)
	(cd "$(REPOSITORY_ROOT)/install/terraform"; "$(REPOSITORY_ROOT)/build/toolchain/bin/terraform$(EXE)" fmt .)
	"$(REPOSITORY_ROOT)/build/toolchain/bin/tflint$(EXE)" --init --chdir install/terraform
	"$(REPOSITORY_ROOT)/build/toolchain/bin/tflint$(EXE)" --chdir install/terraform
	# tflint covers style/correctness, not security misconfigurations (overly
	# permissive IAM, public storage buckets, missing encryption, etc.) -
	# trivy config (successor to the now-maintenance-mode tfsec, folded into
	# trivy) covers that instead, reusing the same tool already pinned for
	# image scanning rather than adding a second, redundant IaC scanner.
	"$(REPOSITORY_ROOT)/build/toolchain/bin/trivy$(EXE)" config --severity HIGH,CRITICAL --exit-code 1 "$(REPOSITORY_ROOT)/install/terraform"
else
lint-terraform:
endif

lint-go: build/toolchain/bin/golangci-lint$(EXE) build/toolchain/bin/gofumpt$(EXE) build/toolchain/bin/revive$(EXE)
	$(GO) fmt ./...
	$(GO) mod verify
	"$(REPOSITORY_ROOT)/build/toolchain/bin/gofumpt$(EXE)" -l -w .
	"$(REPOSITORY_ROOT)/build/toolchain/bin/golangci-lint$(EXE)" fmt ./...
	-"$(REPOSITORY_ROOT)/build/toolchain/bin/golangci-lint$(EXE)" run ./...
	-"$(REPOSITORY_ROOT)/build/toolchain/bin/revive$(EXE)" -set_exit_status -exclude=build/... ./...

lint-docker: build/toolchain/bin/hadolint$(EXE)
	$(FIND) cmd -iname 'Dockerfile*' -exec "$(REPOSITORY_ROOT)/build/toolchain/bin/hadolint$(EXE)" {} +

lint-yaml: build/toolchain/bin/actionlint$(EXE) build/toolchain/bin/shellcheck$(EXE)
	"$(REPOSITORY_ROOT)/build/toolchain/bin/actionlint$(EXE)" -shellcheck="$(REPOSITORY_ROOT)/build/toolchain/bin/shellcheck$(EXE)" -config-file "$(REPOSITORY_ROOT)/.github/actionlint.yaml"

lint-shell: build/toolchain/bin/shellcheck$(EXE)
	@scripts="$$($(FIND) . -name '*.sh' -not -path './third_party/*' -not -path './build/*' -not -path '*/testassets/*')"; \
	shellcheck_exclude=""; \
	if [ "$(OS)" = "Windows_NT" ]; then shellcheck_exclude="--exclude=SC1009,SC1017,SC1044,SC1072,SC1073"; fi; \
	if [ -n "$$scripts" ]; then "$(REPOSITORY_ROOT)/build/toolchain/bin/shellcheck$(EXE)" $$shellcheck_exclude $$scripts; fi

lint-vuln: build/toolchain/bin/govulncheck$(EXE)
	-"$(REPOSITORY_ROOT)/build/toolchain/bin/govulncheck$(EXE)" ./...

bench: $(TEST_ASSETS)
	$(GO) test -bench=. -benchmem -tags testing ${SOURCE_DIRS}

benchmark.html: $(TEST_ASSETS) build/toolchain/bin/vizb$(EXE)
	$(GO) test -json -bench=. -benchmem -tags testing ${SOURCE_DIRS} | build/toolchain/bin/vizb$(EXE) -o benchmark.html

test: test-go test-tf

test-go: $(TEST_ASSETS)
	$(GO) test -shuffle=on -tags testing ${SOURCE_DIRS}

ifneq ($(wildcard install/terraform),)
test-tf: build/toolchain/bin/terraform$(EXE) $(TEST_ASSETS)
	# -backend=false: main.tftest.hcl mocks the providers and never touches
	# real state, so there's no need to configure the (real, per-environment)
	# GCS backend just to run tests.
	(cd "$(REPOSITORY_ROOT)install/terraform/"; "$(REPOSITORY_ROOT)/build/toolchain/bin/terraform$(EXE)" init -backend=false)
	(cd "$(REPOSITORY_ROOT)install/terraform/"; "$(REPOSITORY_ROOT)/build/toolchain/bin/terraform$(EXE)" test)
else
test-tf:
endif

test-deflake: $(TEST_ASSETS)
	CGO_ENABLED=1 $(GO) test -shuffle=on -tags testing $(GO_RACE) ${SOURCE_DIRS} -cover -count $(GO_TEST_COUNT) -test.short

coverage.txt: $(ASSETS)
	for sfile in ${SOURCE_DIRS} ; do \
		go test -race "$$sfile" -coverprofile=package.coverage -covermode=atomic; \
		if [ -f package.coverage ]; then \
			cat package.coverage >> coverage.txt; \
			$(RM) package.coverage; \
		fi; \
	done; \
	sed -i '2,$${/mode: /d;}' $@

coverage.xml: coverage.txt build/toolchain/bin/gocover-cobertura$(EXE)
	"$(REPOSITORY_ROOT)/build/toolchain/bin/gocover-cobertura$(EXE)" < $< > $@

deps:
	$(GO_WITH_PROXY) get -u ./...
	$(GO_WITH_PROXY) mod tidy
	$(GO_WITH_PROXY) mod download

clean:
	rm -f coverage.txt
	-chmod -R +w build/
	rm -rf build/
	rm -rf output/

presubmit: no-sudo tools lint all test-deflake release-binaries

ensure-builder:
	-$(DOCKER) buildx create --name $(BUILDX_BUILDER)

ALL_DOCKER_IMAGES = $(foreach app,$(ALL_APPS),docker-image-$(app))
docker-images: no-sudo $(ALL_DOCKER_IMAGES)
docker-image-%: build/bin/linux/amd64/% ensure-builder
	$(DOCKER) buildx build $(DOCKER_EXTRA_FLAGS) --platform linux/amd64 --build-arg BINARY_PATH=$< $(DOCKER_LABEL_ARGS) --build-arg BINARY_NAME=$* -f cmd/$*/Dockerfile -t $(REGISTRY)/$*:$(TAG) . $(DOCKER_PUSH)

ALL_SCAN_IMAGES = $(foreach app,$(ALL_APPS),scan-image-$(app))
scan-images: $(ALL_SCAN_IMAGES)

# docker-image-%/linux-images/windows-images use plain `buildx build`, which
# never loads the result into the local Docker daemon for ordinary (non-tag)
# runs - so there's nothing there for trivy to scan. This target does its own
# single-arch --load build purely so trivy has a local image to scan,
# independent of the release push pipeline.
scan-image-%: build/bin/linux/amd64/% build/toolchain/bin/trivy$(EXE) ensure-builder
	$(DOCKER) buildx build $(DOCKER_EXTRA_FLAGS) --platform linux/amd64 --build-arg BINARY_PATH=$< $(DOCKER_LABEL_ARGS) --build-arg BINARY_NAME=$* -f cmd/$*/Dockerfile -t $(REGISTRY)/$*:$(TAG)-scan --load .
	"$(REPOSITORY_ROOT)/build/toolchain/bin/trivy$(EXE)" image --severity HIGH,CRITICAL --exit-code 1 $(REGISTRY)/$*:$(TAG)-scan

ALL_IMAGES = $(foreach app,$(ALL_APPS),$(REGISTRY)/$(app))
# https://github.com/docker-library/official-images#architectures-other-than-amd64
images: no-sudo linux-images windows-images
	for image in $(ALL_IMAGES) ; do \
		$(DOCKER) manifest rm $$image:$(TAG) 2>/dev/null || true ; \
		$(DOCKER) manifest create $$image:$(TAG) $(foreach winver,$(WINDOWS_VERSIONS),$${image}:$(TAG)-windows_amd64-$(winver)) $(foreach platform,$(LINUX_PLATFORMS),$${image}:$(TAG)-$(subst /,_,$(platform))) ; \
		for winver in $(WINDOWS_VERSIONS) ; do \
			windows_version=`$(DOCKER) manifest inspect mcr.microsoft.com/windows/nanoserver:$${winver} | jq -r '.manifests[0].platform["os.version"]'`; \
			$(DOCKER) manifest annotate --os-version $${windows_version} $${image}:$(TAG) $${image}:$(TAG)-windows_amd64-$${winver} ; \
		done ; \
		$(DOCKER) manifest push $$image:$(TAG) ; \
	done

.SECONDEXPANSION:

ALL_LINUX_IMAGES = $(foreach app,$(ALL_APPS),$(foreach platform,$(LINUX_PLATFORMS),linux-image-$(app)-$(subst /,_,$(platform))))
linux-images: $(ALL_LINUX_IMAGES)

# Stems here are "<app>-<platform>" with platform underscore-joined (e.g.
# "example-linux_arm_v7"): GNU Make strips everything before the last "/"
# when matching a slash-free pattern, so a literal "/" in the platform
# portion would never match this rule. $(call platform,...)/$(call appname,...)
# (defined near the bottom of this file) split the stem back apart.
linux-image-%: build/bin/$$(subst _,/,$$(call platform,$$*))/$$(call appname,$$*) ensure-builder
	$(DOCKER) buildx build $(DOCKER_EXTRA_FLAGS) --platform $(subst _,/,$(call platform,$*)) --build-arg BINARY_PATH=$< $(DOCKER_LABEL_ARGS) --build-arg BINARY_NAME=$(call appname,$*) -f cmd/$(call appname,$*)/Dockerfile -t $(REGISTRY)/$(call appname,$*):$(TAG)-$(call platform,$*) . $(DOCKER_PUSH)

ALL_WINDOWS_IMAGES = $(foreach app,$(ALL_APPS),$(foreach winver,$(WINDOWS_VERSIONS),windows-image-$(app)-$(winver)))
windows-images: $(ALL_WINDOWS_IMAGES)

# Stems here are "<app>-<winver>" (e.g. "example-ltsc2022"); reuse the same
# platform/appname split even though the trailing token is a Windows version,
# not an OS/arch pair.
windows-image-%: build/bin/windows/amd64/$$(call appname,$$*).exe ensure-builder
	$(DOCKER) buildx build $(DOCKER_EXTRA_FLAGS) --platform windows/amd64 --build-arg BINARY_PATH=$< $(DOCKER_LABEL_ARGS) --build-arg BINARY_NAME=$(call appname,$*) -f cmd/$(call appname,$*)/Dockerfile.windows --build-arg WINDOWS_VERSION=$(call platform,$*) -t $(REGISTRY)/$(call appname,$*):$(TAG)-windows_amd64-$(call platform,$*) . $(DOCKER_PUSH)

.SECONDEXPANSION:

# "appname-linux_arm_v5" -> "linux_arm_v5"
platform = $(lastword $(subst -, ,$(basename $(1))))
# strip "-<platform>" to recover app name (hyphen-safe)
appname  = $(patsubst %-$(call platform,$(1)),%,$(basename $(1)))
# source path: build/bin/linux/arm/v5/appname (no extension on sources)
rel2bin  = build/bin/$(subst _,/,$(call platform,$(1)))/$(call appname,$(1))$(if $(findstring windows,$(platform)),.exe,)

# Debian/Ubuntu's binutils package ships objcopy built with only the x86 BFD
# backends (elf64-x86-64, elf32-i386) - it cannot parse ARM/MIPS/PPC/RISC-V/
# LoongArch/s390x ELF at all, regardless of how valid those bytes are. So
# only these two Linux architectures can actually be embed-signed with it;
# every other Linux platform falls through to the plain-copy case below,
# same as darwin/bsd/other platforms with no apt-installable signing tool.
LINUX_OBJCOPY_SIGNABLE_PLATFORMS = linux_386 linux_amd64

build/release/%: $$(call rel2bin,$$*) $(CODESIGN_CERT) $(CODESIGN_KEY)
	@mkdir -p $(@D)
	cp "$<" "$@"
	touch "$(REPOSITORY_ROOT)/$@"
	$(if $(findstring windows,$(call platform,$*)),osslsigncode sign -certs $(CODESIGN_CERT) -key $(CODESIGN_KEY) -in $@ -out $@.signed && mv $@.signed $@ && chmod +x $@,)
	$(if $(filter $(LINUX_OBJCOPY_SIGNABLE_PLATFORMS),$(call platform,$*)),openssl cms -sign -binary -in $@ -signer $(CODESIGN_CERT) -inkey $(CODESIGN_KEY) -outform DER -out $@.sig && objcopy --add-section .cloudfra_signature=$@.sig --set-section-flags .cloudfra_signature=noload$(COMMA)readonly $@ && rm -f $@.sig,)

no-sudo:
ifndef ALLOW_BUILD_WITH_SUDO
ifeq ($(shell whoami),root)
	@echo "ERROR: Running Makefile as root (or sudo)"
	@echo "Please follow the instructions at https://docs.docker.com/install/linux/linux-postinstall/ if you are trying to sudo run the Makefile because of the 'Cannot connect to the Docker daemon' error."
	@echo "NOTE: sudo/root do not have the authentication token to talk to any GCP service via gcloud."
	exit 1
endif
endif

system-info:
	@echo "Number of Processors"
	@echo "$(shell nproc)"
	@echo ""
	@echo "Kernel Version"
	@uname -a
	@echo ""
	@echo "Storage Metrics"
	@df -h
	@echo "Tools"
	-which tar
	-which unzip
	-which zip
	-which rar
	-which unrar
	-which gzip
	-which gunzip
	-which 7z
	-which bzip2

sync-upstream:
	-git fetch origin; git add -A; git commit -m"Save pending changes."; git rebase -i origin/main 

.PHONY: all tools assets protos windows-binaries run lint lint-go lint-terraform lint-docker lint-yaml lint-shell lint-vuln bench test tf-test test-deflake ensure-builder docker-images scan-images images linux-images windows-images upgrade-deps deps clean presubmit system-info release-binaries no-sudo sync-upstream
