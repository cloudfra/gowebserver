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
# runner that already has them the script only reads the package database and
# exits without calling apt at all.
#
# The distribution is read from /etc/os-release (ID and ID_LIKE) instead of
# assumed, so the same script covers Ubuntu and Debian. Package names the two
# spell differently carry both names in PREREQUISITES, and the apt components
# the non-free tools live in are listed per distribution in
# APT_COMPONENTS_UBUNTU / APT_COMPONENTS_DEBIAN. On a distribution that is not
# Debian-family (macOS, for example) the package step is skipped with a message
# instead of running apt, which the old unconditional apt-get lines could not
# do.
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

# Prerequisites needed on the Linux runners, one per entry, formatted as
# "<commands>|<candidate packages>":
#
#   <commands>            the commands the build invokes for it (see
#                         Makefile_testassets.mk). The prerequisite is already
#                         satisfied when any one of these is on PATH, or when
#                         any one of the candidate packages below is installed.
#   <candidate packages>  apt packages that provide it, in preference order.
#                         More than one name is listed when Ubuntu and Debian
#                         spell the same thing differently.
#
# The notes below are the Ubuntu-vs-Debian differences worth knowing before
# editing this list:
#   * p7zip-full is the Ubuntu name. Debian 13 (trixie) still resolves that
#     name, but only as a transitional package that depends on "7zip", which
#     installs "7zz" rather than "7z". "7zip" is listed as the second name so a
#     Debian runner can install it directly.
#   * lz4 is "lz4" on both.
#   * rar and unrar are in the non-free component on Debian and in multiverse
#     on Ubuntu, so a mirror that doesn't carry them simply doesn't have them;
#     the install step reports that instead of hiding it.
PREREQUISITES=(
  "lz4|lz4"
  "7z 7zz|p7zip-full 7zip"
  "rar|rar"
  "unrar|unrar"
)

# Packages installed only when a component below has to be enabled and the tool
# that enables it is missing. Same "<commands>|<candidate packages>" format.
# software-properties-common ships on Ubuntu; Debian does not ship it at all, in
# which case the component step warns and continues instead of failing.
REPOSITORY_TOOLS=(
  "add-apt-repository|software-properties-common"
)

# apt components the packages above can live in, per distribution. Ubuntu has
# "universe"; Debian has no "universe" and keeps the non-free tools in
# "contrib"/"non-free". A component is only enabled when the runner's apt
# sources don't already list it.
APT_COMPONENTS_UBUNTU=(universe)
APT_COMPONENTS_DEBIAN=(contrib non-free)

# Read-only inputs, kept in variables so the checks can be exercised against
# stubbed fixtures; the defaults are what a real runner has. Neither is ever
# written to.
OS_RELEASE_FILE="${OS_RELEASE_FILE:-/etc/os-release}"
APT_DIR="${APT_DIR:-/etc/apt}"

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

# --- distribution detection -------------------------------------------------

OS_ID=""
OS_ID_LIKE=""
OS_NAME=""

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

load_distribution() {
  OS_ID="$(os_release_field ID)"
  OS_ID_LIKE="$(os_release_field ID_LIKE)"
  OS_NAME="$(os_release_field NAME)"
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

is_debian_family() {
  distro_matches debian || distro_matches ubuntu
}

# apt_components_wanted prints the apt components to consider for this
# distribution, one per line.
apt_components_wanted() {
  local -a components=()
  if distro_matches ubuntu; then
    components=(${APT_COMPONENTS_UBUNTU[@]+"${APT_COMPONENTS_UBUNTU[@]}"})
  else
    components=(${APT_COMPONENTS_DEBIAN[@]+"${APT_COMPONENTS_DEBIAN[@]}"})
  fi
  local component
  for component in ${components[@]+"${components[@]}"}; do
    printf '%s\n' "$component"
  done
}

# --- the "should this action happen at all?" checks -------------------------

# package_installed PKG is true when dpkg already has PKG installed. It reads
# the local package database only, so it works with no apt lists, no network
# and no root. The -f format string stays single-quoted: dpkg-query wants
# ${Status} literally, the shell must not expand it.
package_installed() {
  local pkg="$1"
  local status=""
  status="$(dpkg-query -W -f='${Status}' "$pkg" 2>/dev/null)" || return 1
  [[ "$status" == "install ok installed" ]]
}

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

# entry_provider "commands|packages" prints what already satisfies an entry
# ("command lz4" or "package lz4") and returns 0, or returns 1 when nothing
# does.
entry_provider() {
  local entry="$1"
  local -a commands=() packages=()
  read -r -a commands <<<"${entry%%|*}"
  read -r -a packages <<<"${entry#*|}"
  local name
  for name in ${commands[@]+"${commands[@]}"}; do
    if command -v "$name" >/dev/null 2>&1; then
      printf 'command %s' "$name"
      return 0
    fi
  done
  for name in ${packages[@]+"${packages[@]}"}; do
    if package_installed "$name"; then
      printf 'package %s' "$name"
      return 0
    fi
  done
  return 1
}

# first_shipped_package "pkgA pkgB" prints the first candidate this
# distribution actually ships and returns 0, or returns 1 when it ships none.
first_shipped_package() {
  local -a candidates=()
  read -r -a candidates <<<"$1"
  local pkg
  for pkg in ${candidates[@]+"${candidates[@]}"}; do
    if apt_cache_ships "$pkg"; then
      printf '%s' "$pkg"
      return 0
    fi
  done
  return 1
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

MISSING_ENTRIES=()

# check_prerequisites walks PREREQUISITES and records only the entries that are
# not already satisfied, printing what it found either way.
check_prerequisites() {
  local entry provider
  for entry in ${PREREQUISITES[@]+"${PREREQUISITES[@]}"}; do
    if provider="$(entry_provider "$entry")"; then
      log "already available: ${entry%%|*} (found $provider)"
      continue
    fi
    log "not available: ${entry%%|*} (candidates: ${entry#*|})"
    MISSING_ENTRIES+=("$entry")
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

# enable_apt_components adds the components listed at the top, one at a time,
# and only the ones the runner's sources don't already have. When the tool that
# adds them is missing it installs that package first; when this distribution
# doesn't ship that package either, it warns and lets the install step below
# report anything that then turns out to be unavailable.
enable_apt_components() {
  local -a wanted=() missing=()
  mapfile -t wanted < <(apt_components_wanted)
  local component
  for component in ${wanted[@]+"${wanted[@]}"}; do
    if apt_component_enabled "$component"; then
      log "apt component already enabled: $component"
    else
      missing+=("$component")
    fi
  done
  if ((${#missing[@]} == 0)); then
    return 0
  fi
  local entry provider tool
  for entry in ${REPOSITORY_TOOLS[@]+"${REPOSITORY_TOOLS[@]}"}; do
    if provider="$(entry_provider "$entry")"; then
      log "already available: ${entry%%|*} (found $provider)"
      break
    fi
    if ! tool="$(first_shipped_package "${entry#*|}")"; then
      warn "this distribution ships none of ${entry#*|}, so ${missing[*]} cannot be enabled automatically."
      warn "enable them under $APT_DIR/sources.list.d if a prerequisite is reported unavailable below."
      return 0
    fi
    log "installing $tool (provides ${entry%%|*})"
    run_privileged apt-get -y -q install "$tool"
    if ! entry_provider "$entry" >/dev/null; then
      warn "$tool was installed but ${entry%%|*} still is not on PATH - skipping the component step."
      return 0
    fi
    break
  done
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

# install_missing_prerequisites installs the missing entries in a single apt
# call, after resolving each entry to a package name this distribution actually
# ships. It fails the run when no candidate name exists, which keeps visible the
# prerequisite failure the old script left to apt to report.
install_missing_prerequisites() {
  local -a to_install=()
  local entry label pkg unavailable=0
  for entry in ${MISSING_ENTRIES[@]+"${MISSING_ENTRIES[@]}"}; do
    label="${entry%%|*}"
    if ! pkg="$(first_shipped_package "${entry#*|}")"; then
      warn "no apt package provides '$label' (tried: ${entry#*|}) - enable the component it lives in, see APT_COMPONENTS_* at the top of this file"
      unavailable=1
      continue
    fi
    log "package for '$label': $pkg"
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
  load_distribution
  log "distribution: ${OS_NAME:-unknown} (ID=${OS_ID:-unset}, ID_LIKE=${OS_ID_LIKE:-unset})"

  if ! is_debian_family; then
    log "apt packaging does not apply to this distribution - skipping the package step"
    return 0
  fi

  require_command dpkg-query
  require_command apt-get
  require_command apt-cache

  check_prerequisites
  if ((${#MISSING_ENTRIES[@]} == 0)); then
    log "every prerequisite is already available - apt is not touched"
    return 0
  fi

  # The package lists are needed to learn which candidate names this
  # distribution ships, so refresh them once here, then widen the component
  # list if needed, then install everything still missing in one call.
  apt_get_update
  enable_apt_components
  install_missing_prerequisites

  log "prerequisites ready"
  return 0
}

main "$@"
