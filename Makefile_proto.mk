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

# https://github.com/grpc/grpc-go/releases
GOLANG_GRPC_VERSION = 1.6.2
# https://github.com/protocolbuffers/protobuf-go/releases
GOLANG_PROTOBUF_VERSION = 1.36.12
# https://github.com/grpc-ecosystem/grpc-gateway/releases
GRPC_GATEWAY_VERSION = 2.32.0
# https://github.com/protocolbuffers/protobuf/releases
PROTOC_VERSION = 36.2

ifeq ($(OS),Windows_NT)
	PROTOC_PACKAGE = https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-win64.zip
else
	ifeq ($(UNAME_S),Linux)
		ifeq ($(UNAME_ARCH),arm)
			PROTOC_PACKAGE = https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-linux-aarch_64.zip
		else
			PROTOC_PACKAGE = https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-linux-x86_64.zip
		endif
	endif
	ifeq ($(UNAME_S),Darwin)
		PROTOC_PACKAGE = https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-osx-x86_64.zip
	endif
endif

PROTOC := GOPATH="$(TOOLCHAIN_DIR)" "$(TOOLCHAIN_BIN)/protoc"
PROTOC_INCLUDE_FLAGS = -I "$(REPOSITORY_ROOT)" -I "$(THIRDPARTY_DIR)/grpc_gateway/include/" -I "$(THIRDPARTY_DIR)/google_protobuf/include/"

# protoc otherwise resolves each protoc-gen-* plugin by searching PATH, which
# depends on the toolchain bin directory being on PATH in whatever form the
# current OS/shell expects (this breaks on the Windows CI runner). Pointing
# protoc at each plugin's exact path sidesteps PATH entirely.
PROTOC_GO_PLUGIN = --plugin=protoc-gen-go="$(TOOLCHAIN_BIN)/protoc-gen-go$(EXE)"
PROTOC_GO_GRPC_PLUGIN = --plugin=protoc-gen-go-grpc="$(TOOLCHAIN_BIN)/protoc-gen-go-grpc$(EXE)"
PROTOC_GRPC_GATEWAY_PLUGIN = --plugin=protoc-gen-grpc-gateway="$(TOOLCHAIN_BIN)/protoc-gen-grpc-gateway$(EXE)"
PROTOC_OPENAPIV2_PLUGIN = --plugin=protoc-gen-openapiv2="$(TOOLCHAIN_BIN)/protoc-gen-openapiv2$(EXE)"

PROTOC_TOOLCHAIN = build/toolchain/bin/protoc$(EXE)
PROTOC_TOOLCHAIN += build/toolchain/bin/protoc-gen-go-grpc$(EXE)
PROTOC_TOOLCHAIN += build/toolchain/bin/protoc-gen-go$(EXE)
PROTOC_TOOLCHAIN += build/toolchain/bin/protoc-gen-grpc-gateway$(EXE)
PROTOC_TOOLCHAIN += build/toolchain/bin/protoc-gen-openapiv2$(EXE)

build/archives/protoc.zip:
	mkdir -p $(ARCHIVES_DIR)/
	$(CURL) -o $(ARCHIVES_DIR)/protoc.zip -L $(PROTOC_PACKAGE)
	touch "$(REPOSITORY_ROOT)/$@"

build/toolchain/bin/protoc$(EXE): build/archives/protoc.zip
	mkdir -p "$(dir $@)"
	mkdir -p "$(TOOLCHAIN_DIR)/protoc-temp/"
	cp "$(ARCHIVES_DIR)/protoc.zip" "$(TOOLCHAIN_DIR)/protoc-temp/"
	(cd "$(TOOLCHAIN_DIR)/protoc-temp/"; unzip -q -o protoc.zip)
	cp "$(TOOLCHAIN_DIR)/protoc-temp/bin/protoc$(EXE)" "$(TOOLCHAIN_BIN)/protoc$(EXE)"
	rm -rf "$(TOOLCHAIN_DIR)/protoc-temp/"
	touch "$(REPOSITORY_ROOT)/$@"

build/toolchain/bin/protoc-gen-go-grpc$(EXE): third_party/google_protobuf/include/google/LICENSE third_party/grpc_gateway/include/protoc-gen-openapiv2/LICENSE
	mkdir -p "$(dir $@)"
	cd "$(TOOLCHAIN_BIN)" && $(TOOLCHAIN_GO_INSTALL) google.golang.org/grpc/cmd/protoc-gen-go-grpc@v$(GOLANG_GRPC_VERSION)
	touch "$(REPOSITORY_ROOT)/$@"

build/toolchain/bin/protoc-gen-go$(EXE): third_party/google_protobuf/include/google/LICENSE third_party/grpc_gateway/include/protoc-gen-openapiv2/LICENSE
	mkdir -p "$(dir $@)"
	cd "$(TOOLCHAIN_BIN)" && $(TOOLCHAIN_GO_INSTALL) google.golang.org/protobuf/cmd/protoc-gen-go@v$(GOLANG_PROTOBUF_VERSION)
	touch "$(REPOSITORY_ROOT)/$@"

build/toolchain/bin/protoc-gen-grpc-gateway$(EXE): third_party/google_protobuf/include/google/LICENSE third_party/grpc_gateway/include/protoc-gen-openapiv2/LICENSE
	cd "$(TOOLCHAIN_BIN)" && $(TOOLCHAIN_GO_INSTALL) github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v$(GRPC_GATEWAY_VERSION)
	touch "$(REPOSITORY_ROOT)/$@"

build/toolchain/bin/protoc-gen-openapiv2$(EXE): third_party/google_protobuf/include/google/LICENSE third_party/grpc_gateway/include/protoc-gen-openapiv2/LICENSE
	mkdir -p "$(dir $@)"
	cd "$(TOOLCHAIN_BIN)" && $(TOOLCHAIN_GO_INSTALL) github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v$(GRPC_GATEWAY_VERSION)
	touch "$(REPOSITORY_ROOT)/$@"

# The include trees protoc compiles against are vendored into third_party/ and
# checked in, so a build uses them as they are and downloads nothing. Their
# rules have no prerequisites: they used to depend on the downloaded archives,
# which build/ never keeps in CI, so every run re-vendored them, and unpacking
# the whole googleapis repository (about 10,000 files) took up to half a minute
# on Windows. update-project.sh re-vendors them by deleting the LICENSE files
# these rules make; a project without the trees checked in vendors them on its
# first build. Only the files kept are unpacked, and the tree is replaced only
# once everything has been downloaded.
third_party/google_protobuf/include/google/: third_party/google_protobuf/include/google/LICENSE
	touch "$(REPOSITORY_ROOT)/$@"

third_party/google_protobuf/include/google/LICENSE:
	tmp="$(BUILD_DIR)/googleapis.$$$$.d" && rm -rf "$$tmp" && mkdir -p "$$tmp" && \
	$(CURL) -fL -o "$$tmp/protoc.zip" "$(PROTOC_PACKAGE)" && \
	$(CURL) -fL -o "$$tmp/googleapis.zip" "https://github.com/googleapis/googleapis/archive/master.zip" && \
	(cd "$$tmp" && unzip -q protoc.zip 'include/*' && \
		unzip -q googleapis.zip 'googleapis-master/LICENSE' 'googleapis-master/google/api/*' \
			'googleapis-master/google/rpc/*' 'googleapis-master/google/longrunning/*') && \
	dst="$(THIRDPARTY_DIR)/google_protobuf/include/google" && rm -rf "$(THIRDPARTY_DIR)/google_protobuf/include" && \
	mkdir -p "$$dst" && cp -rf "$$tmp/include/google/"* "$$dst/" && \
	cp -rf "$$tmp/googleapis-master/google/api" "$$tmp/googleapis-master/google/rpc" \
		"$$tmp/googleapis-master/google/longrunning" "$$dst/" && \
	cp -f "$$tmp/googleapis-master/LICENSE" "$$dst/LICENSE" && \
	$(FIND) "$$dst/" -type f -name '*BUILD.bazel' -exec rm {} +; \
	status=$$?; rm -rf "$$tmp"; exit $$status

third_party/grpc_gateway/include/protoc-gen-openapiv2/: third_party/grpc_gateway/include/protoc-gen-openapiv2/LICENSE
	touch "$(REPOSITORY_ROOT)/$@"

third_party/grpc_gateway/include/protoc-gen-openapiv2/LICENSE:
	tmp="$(BUILD_DIR)/grpc-gateway.$$$$.d" && rm -rf "$$tmp" && mkdir -p "$$tmp" && \
	$(CURL) -fL -o "$$tmp/grpc-gateway.zip" "https://github.com/grpc-ecosystem/grpc-gateway/archive/master.zip" && \
	(cd "$$tmp" && unzip -q grpc-gateway.zip 'grpc-gateway-main/LICENSE' \
		'grpc-gateway-main/protoc-gen-openapiv2/options/*.proto') && \
	dst="$(THIRDPARTY_DIR)/grpc_gateway/include/protoc-gen-openapiv2" && rm -rf "$(THIRDPARTY_DIR)/grpc_gateway" && \
	mkdir -p "$$dst/options" && \
	cp -f "$$tmp/grpc-gateway-main/protoc-gen-openapiv2/options/"*.proto "$$dst/options/" && \
	cp -f "$$tmp/grpc-gateway-main/LICENSE" "$$dst/LICENSE"; \
	status=$$?; rm -rf "$$tmp"; exit $$status

%_grpc.pb.go: %.proto %.pb.go $(PROTOC_TOOLCHAIN)
	$(PROTOC) $(PROTOC_INCLUDE_FLAGS) $(PROTOC_GO_GRPC_PLUGIN) --go-grpc_out=. --go-grpc_opt=paths=source_relative $<
	$(TOOLCHAIN_GO) fmt $@
	touch "$(REPOSITORY_ROOT)/$@"

%.pb.go: %.proto $(PROTOC_TOOLCHAIN)
	$(PROTOC) $(PROTOC_INCLUDE_FLAGS) $(PROTOC_GO_PLUGIN) --go_out=. --go_opt=paths=source_relative $<
	$(TOOLCHAIN_GO) fmt $@
	touch "$(REPOSITORY_ROOT)/$@"

%.pb.gw.go: %.proto %_grpc.pb.go $(PROTOC_TOOLCHAIN)
	echo $(dir $<)
	$(PROTOC) $(PROTOC_INCLUDE_FLAGS) $(PROTOC_GRPC_GATEWAY_PLUGIN) --grpc-gateway_out . --grpc-gateway_opt paths=source_relative --grpc-gateway_opt logtostderr=true --grpc-gateway_opt allow_delete_body=true $<
	$(SED_REPLACE) 's/proto_0/proto/g' $@
	$(SED_REPLACE) 's/status_0/status/g' $@
	$(TOOLCHAIN_GO) fmt $@
	touch "$(REPOSITORY_ROOT)/$@"

%.swagger.json: %.proto %.pb.gw.go $(PROTOC_TOOLCHAIN)
	$(PROTOC) $(PROTOC_INCLUDE_FLAGS) $(PROTOC_OPENAPIV2_PLUGIN) --openapiv2_out . --openapiv2_opt logtostderr=true $<
	touch "$(REPOSITORY_ROOT)/$@"
