#!/usr/bin/env bash
#
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

set -euo pipefail

function InstallPackages() {
  sudo apt-get update
  sudo apt-get -y -q install osslsigncode binutils openssl
}

# CheckPackages reports whether every binary the signing recipes in
# Makefile_build.mk invoke is already on PATH, so a runner that was set up
# by a previous run skips the apt-get pass.
function CheckPackages() {
  command -v osslsigncode >/dev/null 2>&1 &&
    command -v objcopy >/dev/null 2>&1 &&
    command -v openssl >/dev/null 2>&1
}

function Main() {
  if ! CheckPackages; then
    InstallPackages
  fi
}

Main
