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

# Per-project CI preparation hook for Linux and macOS jobs.
#
# The CI workflows run this immediately before "make system-info" in the
# build-linux job (see .github/workflows/deploy.yaml), the build-release job
# (see .github/workflows/create-release.yaml) and the build-temp-release job
# (see .github/workflows/create-temp-release.yaml), so it is the place to
# install or configure whatever this project needs on its runners:
# distribution packages, extra toolchain, runner-local fixes.
#
# What it does today is make sure the archive commands Makefile_testassets.mk
# invokes (7z, lz4, rar, unrar) are available, because "make test-go" needs the
# generated test archives. Every action is guarded by a check first, so on a
# runner that already has them the script only looks for those commands on PATH
# and exits without calling apt at all.
#
# Ubuntu and Debian are handled separately because they name the same tools
# differently: UBUNTU_PACKAGES and DEBIAN_PACKAGES hold the package names for
# each distribution, APT_COMPONENTS_UBUNTU and APT_COMPONENTS_DEBIAN the apt
# components those packages live in. The distribution is read from
# /etc/os-release (ID and ID_LIKE) rather than assumed, and each distribution
# then gets its own steps: enable_apt_components_ubuntu and
# enable_apt_components_debian, plus link_debian_7z_command, which gives a
# Debian runner the "7z" the build calls because Debian's 7-Zip package
# installs "7zz". On a distribution that is not Debian-family (macOS, for
# example) the package step is skipped with a message instead of running apt,
# which the old unconditional apt-get lines could not do.
#
# A non-zero exit status still fails the job, which is the intended way to make
# a prerequisite failure visible. What does change is when the script exits
# non-zero: a runner that already satisfies every prerequisite now exits 0
# without touching apt, where the old script always ran apt-get - and failed
# outright wherever sudo or apt is missing.
#
# scripts/template-go/sync-template.sh copies this file into a project only when
# the project does not already have one and never overwrites an existing copy,
# so anything added below survives a template re-sync.

set -euo pipefail

# --- configuration: the single place a maintainer edits ---------------------

# The commands the build invokes (see Makefile_testassets.mk: "7z a" and
# "tar cf - * | lz4", plus the rar/unrar archives). A prerequisite counts as
# already satisfied when its command is on PATH, which is the only form the
# build can use.
COMMANDS=(7z lz4 rar unrar)

# The package that provides each command above, in the same order, for each
# distribution. Ubuntu's 7-Zip package is p7zip-full and installs "7z".
UBUNTU_PACKAGES=(p7zip-full lz4 rar unrar)

# Debian spells the same package 7zip (Debian 12 and 13 alike) and it installs
# "7zz", so the Debian step below links "7z" to it after installing. rar and
# unrar are named the same on both distributions but live in different apt
# components, see APT_COMPONENTS_* below.
DEBIAN_PACKAGES=(7zip lz4 rar unrar)

# The apt components those packages can live in. Ubuntu keeps rar in
# "multiverse" and p7zip-full in "universe"; Debian has no "multiverse" and
# keeps the non-free tools in "contrib" and "non-free". A component is only
# touched when the runner's apt sources don't already list it.
APT_COMPONENTS_UBUNTU=(universe multiverse)
APT_COMPONENTS_DEBIAN=(contrib non-free)

# Read-only inputs, kept in variables so the checks can be exercised against
# stubbed fixtures; the defaults are what a real runner has. Only BIN_DIR is
# ever written to, and only by the Debian link step below.
OS_RELEASE_FILE="${OS_RELEASE_FILE:-/etc/os-release}"
APT_DIR="${APT_DIR:-/etc/apt}"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"

# --- small helpers ----------------------------------------------------------

log() { printf 'prepare-ci: %s\n' "$*"; }
warn() { printf 'prepare-ci: %s\n' "$*" >&2; }

# run_privileged runs the command as-is when already root and through sudo
# otherwise. It is only reached for steps that actually need root, and it fails
# with a readable message when neither is available.
run_privileged() {
  if ((EUID == 0)); then
    "$@"
    return 0
  fi
  if ! command -v sudo >/dev/null 2>&1; then
    warn "need root to run '$*' and sudo is not installed."
    return 1
  fi
  sudo "$@"
}

require_command() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    warn "$name is not installed but is required to continue."
    return 1
  fi
  return 0
}

# --- distribution detection, and the per-distribution lists it selects ------

OS_ID=""
OS_ID_LIKE=""
OS_NAME=""
DISTRIBUTION=""

# These two are what the selected distribution's configuration is, filled in
# by load_distribution from the matching *_UBUNTU or *_DEBIAN list above.
PACKAGES=()
COMPONENTS=()

# os_release_field FIELD prints FIELD's value from /etc/os-release with the
# surrounding quotes stripped, or nothing when the file or the key is missing.
os_release_field() {
  local field="$1"
  local value="" line
  if [[ -r "$OS_RELEASE_FILE" ]]; then
    while IFS= read -r line; do
      [[ "$line" == "${field}="* ]] || continue
      value="${line#"${field}="}"
      value="${value%\"}"
      value="${value#\"}"
      break
    done < "$OS_RELEASE_FILE"
  fi
  printf '%s' "$value"
}

# distro_matches NAME is true when NAME is the distribution ID or one of its
# ID_LIKE parents (an Ubuntu derivative reports ID_LIKE=debian, for example).
distro_matches() {
  local want="$1"
  local -a ids=()
  read -r -a ids <<<"$OS_ID $OS_ID_LIKE"
  local id
  for id in ${ids[@]+"${ids[@]}"}; do
    if [[ "${id,,}" == "$want" ]]; then
      return 0
    fi
  done
  return 1
}

# load_distribution reads /etc/os-release and selects one distribution's lists.
# An Ubuntu derivative is treated as Ubuntu and a Debian derivative as Debian;
# anything else returns 1 so the caller can skip the package step.
load_distribution() {
  OS_ID="$(os_release_field ID)"
  OS_ID_LIKE="$(os_release_field ID_LIKE)"
  OS_NAME="$(os_release_field NAME)"
  log "distribution: ${OS_NAME:-unknown} (ID=${OS_ID:-unset}, ID_LIKE=${OS_ID_LIKE:-unset})"

  if distro_matches ubuntu; then
    DISTRIBUTION=ubuntu
    PACKAGES=(${UBUNTU_PACKAGES[@]+"${UBUNTU_PACKAGES[@]}"})
    COMPONENTS=(${APT_COMPONENTS_UBUNTU[@]+"${APT_COMPONENTS_UBUNTU[@]}"})
    return 0
  fi
  if distro_matches debian; then
    DISTRIBUTION=debian
    PACKAGES=(${DEBIAN_PACKAGES[@]+"${DEBIAN_PACKAGES[@]}"})
    COMPONENTS=(${APT_COMPONENTS_DEBIAN[@]+"${APT_COMPONENTS_DEBIAN[@]}"})
    return 0
  fi
  return 1
}

# --- the "should this action happen at all?" checks -------------------------

# apt_cache_ships PKG is true when apt can see a candidate version for PKG,
# i.e. when this distribution provides that name at all. The output is captured
# rather than piped into grep: under "set -o pipefail" a grep -q that exits on
# the first match can SIGPIPE apt-cache and turn a real candidate into a
# non-zero pipeline.
apt_cache_ships() {
  local pkg="$1"
  local out=""
  out="$(apt-cache policy "$pkg" 2>/dev/null)" || return 1
  [[ "$out" == *"Candidate:"* ]]
}

# apt_component_enabled COMPONENT is true when the runner's apt sources already
# list COMPONENT. This is a cheap textual check over the one-line deb entries
# and the deb822 Components: fields under $APT_DIR, not a full sources parser,
# and it needs no apt lists.
apt_component_enabled() {
  local component="$1"
  local -a files=() found=()
  if [[ -f "$APT_DIR/sources.list" ]]; then
    files+=("$APT_DIR/sources.list")
  fi
  if [[ -d "$APT_DIR/sources.list.d" ]]; then
    mapfile -t found < <(
      find "$APT_DIR/sources.list.d" -maxdepth 1 -type f \
        \( -name '*.list' -o -name '*.sources' \) 2>/dev/null
    )
    files+=(${found[@]+"${found[@]}"})
  fi
  if ((${#files[@]} == 0)); then
    return 1
  fi
  local lines=""
  lines="$(grep -hE '^[[:space:]]*(deb|deb-src)[[:space:]]|^[[:space:]]*Components:' "${files[@]}" 2>/dev/null)" || true
  local pattern="(^|[[:space:]])${component}([[:space:]]|$)"
  [[ "$lines" =~ $pattern ]]
}

# --- the steps, each guarded by the checks above ----------------------------

MISSING_PACKAGES=()

# check_prerequisites walks COMMANDS against this distribution's PACKAGES -
# same order, one package per command - and records the package for every
# command that is not on PATH yet, printing what it found either way. PATH is
# the only test used because a command the build cannot find is not a satisfied
# prerequisite however apt labels it.
check_prerequisites() {
  local index command pkg
  for index in "${!COMMANDS[@]}"; do
    command="${COMMANDS[index]}"
    pkg="${PACKAGES[index]}"
    if command -v "$command" >/dev/null 2>&1; then
      log "already available: $command (found on PATH)"
    else
      log "not available: $command (package $pkg)"
      MISSING_PACKAGES+=("$pkg")
    fi
  done
  return 0
}

# apt_get_update refreshes the package lists, at most once per run and only
# when a caller has already established that something may need installing.
APT_UPDATED=false
apt_get_update() {
  if [[ "$APT_UPDATED" == true ]]; then
    log "package lists already refreshed this run - not running apt-get update again"
    return 0
  fi
  APT_UPDATED=true
  log "running apt-get update"
  run_privileged apt-get update
}

# enable_apt_components_ubuntu enables the Ubuntu components listed at the top,
# one at a time, and only the ones the runner's sources don't already have. On
# Ubuntu the tool that enables them is add-apt-repository, from
# software-properties-common, which is installed here when it is missing.
enable_apt_components_ubuntu() {
  local -a missing=()
  local component
  for component in ${COMPONENTS[@]+"${COMPONENTS[@]}"}; do
    if apt_component_enabled "$component"; then
      log "apt component already enabled: $component"
    else
      missing+=("$component")
    fi
  done
  if ((${#missing[@]} == 0)); then
    return 0
  fi
  if ! command -v add-apt-repository >/dev/null 2>&1; then
    if ! apt_cache_ships software-properties-common; then
      warn "this Ubuntu release ships no software-properties-common, so ${missing[*]} cannot be enabled automatically."
      warn "enable them under $APT_DIR/sources.list.d if a prerequisite is reported unavailable below."
      return 0
    fi
    log "installing software-properties-common (provides add-apt-repository)"
    run_privileged apt-get -y -q install software-properties-common
    if ! command -v add-apt-repository >/dev/null 2>&1; then
      warn "software-properties-common was installed but add-apt-repository still is not on PATH - skipping the component step."
      return 0
    fi
  fi
  local changed=0
  for component in ${missing[@]+"${missing[@]}"}; do
    log "enabling apt component: $component"
    run_privileged add-apt-repository "$component"
    changed=1
  done
  if ((changed == 1)); then
    APT_UPDATED=false
    apt_get_update
  fi
  return 0
}

# enable_apt_components_debian enables the Debian components listed at the top,
# one at a time, and only the ones the runner's sources don't already have.
# Debian runners normally already carry contrib and non-free, so this usually
# changes nothing; when they don't, it uses add-apt-repository only if the
# runner already has it, because Debian images do not necessarily carry the
# package that ships it. Rather than edit the runner's sources by hand here, the
# missing components are reported and the install step below shows exactly what
# that leaves unavailable.
enable_apt_components_debian() {
  local -a missing=()
  local component
  for component in ${COMPONENTS[@]+"${COMPONENTS[@]}"}; do
    if apt_component_enabled "$component"; then
      log "apt component already enabled: $component"
    else
      missing+=("$component")
    fi
  done
  if ((${#missing[@]} == 0)); then
    return 0
  fi
  if ! command -v add-apt-repository >/dev/null 2>&1; then
    warn "this Debian runner has no add-apt-repository, so ${missing[*]} cannot be enabled automatically."
    warn "enable them under $APT_DIR/sources.list.d if a prerequisite is reported unavailable below."
    return 0
  fi
  local changed=0
  for component in ${missing[@]+"${missing[@]}"}; do
    log "enabling apt component: $component"
    run_privileged add-apt-repository "$component"
    changed=1
  done
  if ((changed == 1)); then
    APT_UPDATED=false
    apt_get_update
  fi
  return 0
}

# link_debian_7z_command gives Debian runners the "7z" the build calls. Debian's
# 7-Zip package installs "7zz" instead (see DEBIAN_PACKAGES above), so without
# this a Debian runner would pass the package check and still fail the .7z
# archive rule in Makefile_testassets.mk. It only acts when neither command is
# usable and nothing is already at the link destination, so a runner that has
# "7z", or was prepared by an earlier run, is left alone.
link_debian_7z_command() {
  local target="$BIN_DIR/7z"
  local source=""
  if command -v 7z >/dev/null 2>&1; then
    return 0
  fi
  source="$(command -v 7zz 2>/dev/null)" || {
    warn "no 7z and no 7zz on PATH - the .7z test archive in Makefile_testassets.mk cannot be built."
    return 1
  }
  if [[ -e "$target" ]]; then
    log "$target already exists - not linking it again"
    return 0
  fi
  log "linking $target to $source (Debian's 7zip package installs 7zz, the build calls 7z)"
  run_privileged ln -s "$source" "$target"
}

# install_missing_packages installs what check_prerequisites recorded in a
# single apt call, after confirming apt has a candidate for each name. It fails
# the run when a name is unavailable, which keeps visible the prerequisite
# failure the old script left to apt to report.
install_missing_packages() {
  local -a to_install=()
  local pkg unavailable=0
  for pkg in ${MISSING_PACKAGES[@]+"${MISSING_PACKAGES[@]}"}; do
    if ! apt_cache_ships "$pkg"; then
      warn "no apt package named $pkg (the components it can live in are listed in APT_COMPONENTS_${DISTRIBUTION^^} at the top of this file)"
      unavailable=1
      continue
    fi
    to_install+=("$pkg")
  done
  if ((unavailable == 1)); then
    return 1
  fi
  if ((${#to_install[@]} == 0)); then
    log "nothing left to install"
    return 0
  fi
  log "installing: ${to_install[*]}"
  run_privileged apt-get -y -q install ${to_install[@]+"${to_install[@]}"}
}

# --- main -------------------------------------------------------------------

main() {
  if ! load_distribution; then
    log "apt packaging does not apply to this distribution - skipping the package step"
    return 0
  fi

  require_command apt-get
  require_command apt-cache

  check_prerequisites
  if ((${#MISSING_PACKAGES[@]} == 0)); then
    log "every prerequisite is already available - apt is not touched"
    return 0
  fi

  # The package lists are needed to learn whether apt has a candidate for a
  # missing package, so refresh them once here, then do this distribution's
  # own steps: widen its component list, install everything in one call, and
  # finish the distribution-specific command setup.
  apt_get_update
  if [[ "$DISTRIBUTION" == ubuntu ]]; then
    enable_apt_components_ubuntu
    install_missing_packages
  else
    enable_apt_components_debian
    install_missing_packages
    link_debian_7z_command
  fi

  log "prerequisites ready"
  return 0
}

main "$@"
