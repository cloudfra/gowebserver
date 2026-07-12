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

include Makefile_testassets.mk

REGISTRY = docker.io/cloudfra
PROTOS =
TEST_ASSETS = $(TEST_ARCHIVES)
ASSETS = $(PROTOS)
GO_PACKAGE = github.com/cloudfra/gowebserver
ALL_APPS = gowebserver

include Makefile_build.mk

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
