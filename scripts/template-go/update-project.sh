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

# Bring a checkout up to date in one pass:
#
#   1. fast-forward it to the newest template-go release tag
#   2. bump the pinned versions in every Makefile_*.mk (tool versions in
#      Makefile_toolchain.mk, protoc in Makefile_proto.mk) to the newest
#      upstream release of each tool
#   3. bump the terraform provider pins in any *.tf, and keep the
#      required_version constraint aligned with TERRAFORM_VERSION
#   4. re-vendor third_party/ from the upstream archives the makefiles fetch
#   5. move the Go modules to their latest releases through the repo's own
#      `make deps` target, then verify the checksums
#
# Step 2 only touches pins that hold a concrete version; a pin set to `latest`
# is left alone. Every tool is pinned, so builds are reproducible and an
# installed tool is only replaced when its pin moves. The exception is
# gocover-cobertura, whose upstream has no tagged release: it is pinned to a
# pseudo-version and has to be bumped by hand.
#
# ANDROID_NDK_VERSION resolves from the android/ndk GitHub releases, whose tags
# are the download-suffix the makefile builds its URL from (r28c, r29, r30).
# Those tags are not semver, so the comparison below is the only place NDK
# versions are ordered.
#
# Every candidate version is verified before the pin is rewritten. Tools whose
# binaries are downloaded as archives (terraform, trivy, rumdl, shellcheck,
# hadolint, docker-compose, certtool) get their package URL expanded by make
# itself -- so the check uses the exact URL the build would fetch, and the
# script cannot drift out of sync with the makefile's per-OS URL tables. Tools
# installed with `go install` are verified against the Go module proxy, which
# is the authority for the version string the makefile passes to it.
#
# Nothing is committed and nothing is pushed: the script leaves the changes in
# the working tree for review.
#
# Usage: scripts/template-go/update-project.sh [options]
#
# Safe to re-run: it converges on "already up to date" every time.

set -euo pipefail

# Defaults live here (not just in usage) so --help and the actual behavior
# always agree.
SCRIPT_SOURCE_REPO="git@github.com:cloudfra/template-go.git"

# --- argument parsing -------------------------------------------------------

usage() {
  cat >&2 <<EOF
Usage: $0 [options]

Update the checkout to the newest template-go release, bump the pinned tool
versions in Makefile_toolchain.mk, and move the Go modules to their latest
releases with the repo's own "make deps" target. Changes are left
uncommitted for review.

Options:
      --ref REF      Branch or tag to sync to (default: newest release tag)
      --repo URL     Clone URL to fetch the template from
                     (default: $SCRIPT_SOURCE_REPO)
  -n, --dry-run      Report what would change without touching anything
  -c, --check        Dry run; exit 1 if an update is available (for CI)
      --no-sync      Skip the template fast-forward (dependency bumps only)
      --no-deps      Skip the Makefile_*.mk dependency pins
      --no-tf        Skip the terraform provider bumps
      --no-vendored  Skip the third_party re-vendor
  -t, --target DIR   Project root to update (default: the project this script
                     sits in, i.e. two levels above scripts/template-go/)
  -h, --help         Show this help

Environment overrides: TEMPLATE_GO_REPO, GITHUB_TOKEN (raises the GitHub API
rate limit). Requires git, curl and make; go is used when go.mod is present.
EOF
}

TEMPLATE_GO_REPO="${TEMPLATE_GO_REPO:-$SCRIPT_SOURCE_REPO}"
REF=""
TARGET=""
DRY_RUN=false
CHECK=false
DO_SYNC=true
DO_DEPS=true
DO_TF=true
DO_VENDORED=true

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ref)
      REF="${2:?value required for $1}"
      shift 2
      ;;
    --repo)
      TEMPLATE_GO_REPO="${2:?value required for $1}"
      shift 2
      ;;
    -n | --dry-run)
      DRY_RUN=true
      shift
      ;;
    -c | --check)
      DRY_RUN=true
      CHECK=true
      shift
      ;;
    --no-sync)
      DO_SYNC=false
      shift
      ;;
    --no-deps)
      DO_DEPS=false
      shift
      ;;
    --no-tf)
      DO_TF=false
      shift
      ;;
    --no-vendored)
      DO_VENDORED=false
      shift
      ;;
    -t | --target)
      TARGET="${2:?value required for $1}"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

# --- resolve target ---------------------------------------------------------

if [[ -z "$TARGET" ]]; then
  # This script is expected to live in <project>/scripts/template-go/, so the
  # target is two levels above the directory that holds it.
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  TARGET="$(dirname "$(dirname "$SCRIPT_DIR")")"
fi
if [[ ! -d "$TARGET" ]]; then
  echo "Target project root not found: $TARGET" >&2
  exit 1
fi
TARGET="$(cd "$TARGET" && pwd)"
TARGET="${TARGET%/}"

# Pins live in more than one include (tool versions in Makefile_toolchain.mk,
# protoc in Makefile_proto.mk), so every Makefile_*.mk at the root is scanned.
MAKEFILES=()
while IFS= read -r discovered; do
  MAKEFILES+=("$discovered")
done < <(find "$TARGET" -maxdepth 1 -type f -name 'Makefile_*.mk' | sort)

UPDATE_COUNT=0

# --- temp scratch space -----------------------------------------------------

SYNC_TMP=""
cleanup() {
  if [[ -n "${SYNC_TMP:-}" && -d "${SYNC_TMP:-}" ]]; then
    rm -rf -- "$SYNC_TMP"
  fi
  return 0
}
trap cleanup EXIT INT TERM

# --- helpers ----------------------------------------------------------------

# git_in ARGS runs git inside the target tree.
git_in() {
  git -C "$TARGET" "$@"
}

# vercmp A B prints "newer" when B sorts above A, "same" when equal, and
# prints nothing otherwise. Versions are compared numerically segment by
# segment, so 0.2.78 < 0.3.0 and 1.16.5 < 1.16.6.
vercmp() {
  local a="$1" b="$2" top
  if [[ "$a" == "$b" ]]; then
    echo "same"
    return 0
  fi
  top="$(printf '%s\n%s\n' "$a" "$b" | sort -V | tail -n 1)"
  if [[ "$top" == "$b" ]]; then
    echo "newer"
  fi
}

# gh_api URL prints the JSON body, or nothing on a rate-limit/auth failure.
gh_api() {
  local url="$1" auth=()
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
  fi
  if ! curl -fsS --max-time 30 "${auth[@]}" "$url" 2>/dev/null; then
    return 1
  fi
  return 0
}

# latest_github REPO prints the newest release tag of a GitHub repo with any
# leading "v" stripped.
latest_github() {
  local tag
  tag="$(gh_api "https://api.github.com/repos/$1/releases/latest" |
    jq -r '.tag_name // empty')" || return 1
  [[ -n "$tag" ]] || return 1
  echo "${tag#v}"
}

# latest_hashicorp NAME prints the newest version from HashiCorp's releases
# API, which is what releases.hashicorp.com serves the archives from.
latest_hashicorp() {
  local version
  version="$(curl -fsS --max-time 30 \
    "https://api.releases.hashicorp.com/v1/releases/$1/latest" 2>/dev/null |
    jq -r '.version // empty')" || return 1
  [[ -n "$version" ]] || return 1
  echo "${version#v}"
}

# latest_goproxy MODULE prints the latest version the Go module proxy has for
# MODULE. This is the authority for the version string handed to `go install`,
# which matters where a GitHub mirror lags the real module (x/vuln does).
latest_goproxy() {
  local version
  version="$(curl -fsS --max-time 30 \
    "https://proxy.golang.org/$1/@latest" 2>/dev/null |
    jq -r '.Version // empty')" || return 1
  [[ -n "$version" ]] || return 1
  echo "${version#v}"
}

# package_url VAR VERSION prints the URL make would download for VAR (e.g.
# TERRAFORM_PACKAGE) if VERSION were pinned, expanded through whichever
# Makefile_*.mk currently holds MAKEFILE so the per-OS asset naming stays
# authoritative and the script cannot drift out of sync with it.
package_url() {
  local var="$1" version="$2"
  make -s -f "$HELPERS_MK" -C "$TARGET" PINFILE="$(basename "$MAKEFILE")" \
    "print-$var" "${var%_PACKAGE}_VERSION=$version" 2>/dev/null | tr -d '[:space:]'
}

# url_ok URL is true when the URL can be fetched (following redirects).
url_ok() {
  curl -fsIL --max-time 30 -o /dev/null -L "$1" 2>/dev/null
}

# --- step 1: sync to the newest template-go release -------------------------

sync_template() {
  echo "== template-go sync =="
  if ! git_in rev-parse --git-dir >/dev/null 2>&1; then
    echo "  skip: $TARGET is not a git repository" >&2
    return 0
  fi

  echo "Fetching $TEMPLATE_GO_REPO"
  if ! git_in fetch --quiet --tags --prune "$TEMPLATE_GO_REPO"; then
    echo "  skip: could not fetch $TEMPLATE_GO_REPO" >&2
    return 0
  fi

  local want
  if [[ -n "$REF" ]]; then
    want="$REF"
  else
    want="$(git_in tag --list 'v[0-9]*' --sort=-v:refname | head -n 1)"
  fi
  if [[ -z "$want" ]] || ! git_in rev-parse --verify --quiet "$want^{commit}" >/dev/null; then
    echo "  skip: ref not found after fetch: ${want:-<none>}" >&2
    return 0
  fi

  local head_sha want_sha
  head_sha="$(git_in rev-parse HEAD)"
  want_sha="$(git_in rev-parse "$want^{commit}")"

  if [[ "$head_sha" == "$want_sha" ]]; then
    echo "  already at $want"
    return 0
  fi
  if git_in merge-base --is-ancestor "$want_sha" "$head_sha"; then
    echo "  already at or past $want (newest tag is an ancestor of HEAD)"
    return 0
  fi

  if [[ -n "$(git_in status --porcelain)" ]]; then
    echo "  skip: working tree is dirty; commit or stash before syncing to $want" >&2
    return 0
  fi
  if ! git_in merge-base --is-ancestor "$head_sha" "$want_sha"; then
    echo "  skip: $want is not a fast-forward from HEAD; resolving the divergence is left to you" >&2
    return 0
  fi

  UPDATE_COUNT=$((UPDATE_COUNT + 1))
  if [[ "$DRY_RUN" == true ]]; then
    echo "  would fast-forward $(git_in rev-parse --short HEAD) -> $want"
    return 0
  fi
  echo "  fast-forwarding $(git_in rev-parse --short HEAD) -> $want"
  git_in merge --ff-only --quiet "$want_sha"
  echo "  now at $want"
}

# --- step 2: bump the pinned tool versions ----------------------------------

# Each entry is PIN:SOURCE, where SOURCE is one of
#   github:<owner>/<repo> | hashicorp:<name> | goproxy:<module> | skip
# Pins absent from this list are reported as unmapped rather than guessed at.
PIN_SOURCES=(
  "ACTIONLINT:goproxy:github.com/rhysd/actionlint"
  "DOCKERCOMPOSE:github:docker/compose"
  "GOCOVER_COBERTURA:skip"
  "GOFUMPT:goproxy:mvdan.cc/gofumpt"
  "GOLANGCI_LINT:goproxy:github.com/golangci/golangci-lint/v2"
  "REVIVE:goproxy:github.com/mgechev/revive"
  "TFLINT:goproxy:github.com/terraform-linters/tflint"
  "GOLANG_GRPC:goproxy:google.golang.org/grpc/cmd/protoc-gen-go-grpc"
  "GOLANG_PROTOBUF:goproxy:google.golang.org/protobuf"
  "GRPC_GATEWAY:goproxy:github.com/grpc-ecosystem/grpc-gateway/v2"
  "TERRAFORM:hashicorp:terraform"
  "CERTTOOL:github:cloudfra/certtool"
  "HADOLINT:github:hadolint/hadolint"
  "GOVULNCHECK:goproxy:golang.org/x/vuln"
  "RUMDL:github:rvben/rumdl"
  "SHELLCHECK:github:koalaman/shellcheck"
  "TRIVY:github:aquasecurity/trivy"
  "VIZB:goproxy:github.com/goptics/vizb"
  "PROTOC:github:protocolbuffers/protobuf"
  "ANDROID_NDK:github:android/ndk"
)

# source_for PIN echoes the mapped source, or nothing when unmapped.
source_for() {
  local pin="$1" entry
  for entry in "${PIN_SOURCES[@]}"; do
    if [[ "${entry%%:*}" == "$pin" ]]; then
      echo "${entry#*:}"
      return 0
    fi
  done
  return 1
}

# pin_value VAR [FILE] prints the normalized value of pin VAR, with surrounding
# whitespace, trailing comments and trailing commas stripped. FILE defaults to
# the makefile currently being rewritten.
pin_value() {
  local file="${2-$MAKEFILE}" line value
  line="$(grep -E "^$1[[:space:]]*=" "$file" | head -n 1)"
  [[ -n "$line" ]] || return 1
  value="${line#*=}"
  value="${value//[[:space:]]/}"
  value="${value%%#*}"
  value="${value%%,*}"
  echo "$value"
}

# global_pin_value VAR prints the first value of pin VAR found in any
# Makefile_*.mk, for callers that need a pin from a makefile other than the one
# currently being rewritten.
global_pin_value() {
  local mk value
  for mk in "${MAKEFILES[@]}"; do
    if value="$(pin_value "$1" "$mk")"; then
      echo "$value"
      return 0
    fi
  done
  return 1
}

# latest_version SOURCE PIN prints the newest version for a source spec.
latest_version() {
  local kind="${1%%:*}" arg="${1#*:}"
  case "$kind" in
    github) latest_github "$arg" ;;
    hashicorp) latest_hashicorp "$arg" ;;
    goproxy) latest_goproxy "$arg" ;;
    *) return 1 ;;
  esac
}

bump_deps() {
  if [[ ${#MAKEFILES[@]} -eq 0 ]]; then
    echo
    echo "== dependency pins =="
    echo "  skip: no Makefile_*.mk found in $TARGET" >&2
    return 0
  fi
  for MAKEFILE in "${MAKEFILES[@]}"; do
    bump_makefile_pins
  done
}

# bump_makefile_pins walks every *_VERSION pin in the makefile named by MAKEFILE.
bump_makefile_pins() {
  echo
  echo "== dependency pins (${MAKEFILE#"$TARGET"/}) =="

  local line var current
  while IFS= read -r line; do
    var="${line%%=*}"
    var="${var//[[:space:]]/}"
    current="${line#*=}"
    current="${current//[[:space:]]/}"

    # Strip a trailing comment, then any trailing comma or quote.
    current="${current%%#*}"
    current="${current%%,*}"

    if [[ -z "$var" || -z "$current" ]]; then
      continue
    fi
    if [[ "$current" == "latest" ]]; then
      printf '  %-22s %-10s floating, left alone\n' "$var" "$current"
      continue
    fi

    local src
    if ! src="$(source_for "${var%_VERSION}")"; then
      printf '  %-22s %-10s unmapped pin, not touched\n' "$var" "$current"
      continue
    fi
    if [[ "$src" == "skip" ]]; then
      printf '  %-22s %-10s no versioned upstream API, left alone\n' "$var" "$current"
      continue
    fi

    local newest
    if ! newest="$(latest_version "$src" "$var")" || [[ -z "$newest" ]]; then
      printf '  %-22s %-10s could not resolve %s\n' "$var" "$current" "$src"
      continue
    fi

    case "$(vercmp "$current" "$newest")" in
      same)
        printf '  %-22s %-10s up to date\n' "$var" "$current"
        continue
        ;;
      newer) ;;
      *)
        printf '  %-22s %-10s ahead of %s (%s), left alone\n' "$var" "$current" "$src" "$newest"
        continue
        ;;
    esac

    # Verify the candidate before rewriting: a release can exist while the
    # asset this platform downloads is renamed or missing.
    local pkg_url
    if [[ -f "$TARGET/Makefile" ]] && pkg_url="$(package_url "${var}_PACKAGE" "$newest")" &&
      [[ -n "$pkg_url" ]]; then
      if ! url_ok "$pkg_url"; then
        printf '  %-22s %-10s -> %s skipped: %s not reachable\n' "$var" "$current" "$newest" "$pkg_url"
        continue
      fi
    fi

    UPDATE_COUNT=$((UPDATE_COUNT + 1))
    if [[ "$DRY_RUN" == true ]]; then
      printf '  %-22s %-10s -> %s\n' "$var" "$current" "$newest"
      continue
    fi

    # Rewrite only the value so the file's own spacing style is preserved.
    sed -i -E "s/^(${var})([[:space:]]*=[[:space:]]*).*/\1\2${newest}/" "$MAKEFILE"
    if [[ "$(pin_value "$var")" != "$newest" ]]; then
      echo "  error: $var still not set to $newest after rewriting $MAKEFILE" >&2
      exit 1
    fi
    printf '  %-22s %-10s -> %s\n' "$var" "$current" "$newest"
  done < <(grep -E '^[A-Z0-9_]+_VERSION[[:space:]]*=' "$MAKEFILE")
}

# --- step 4: terraform provider pins ----------------------------------------

# tf_files lists every terraform config in the project, skipping build output.
tf_files() {
  find "$TARGET" -type f -name '*.tf' -not -path "$TARGET/build/*" | sort
}

# latest_tf_provider SOURCE prints the newest version the Terraform registry
# has for a provider address such as hashicorp/google.
latest_tf_provider() {
  local version
  version="$(curl -fsS --max-time 30 \
    "https://registry.terraform.io/v1/providers/$1" 2>/dev/null |
    jq -r '.version // empty')" || return 1
  [[ -n "$version" ]] || return 1
  echo "${version#v}"
}

# tf_provider_pairs prints "provider current_version" for every
# required_providers entry across the given .tf files. The (^|[^_[:alnum:]])
# guard stops required_version from being mistaken for a provider pin.
tf_provider_pairs() {
  awk '
    match($0, /source[[:space:]]*=[[:space:]]*"[^"]+"/) {
      seg = substr($0, RSTART, RLENGTH)
      sub(/^[^"]*"/, "", seg)
      sub(/".*$/, "", seg)
      src = seg
    }
    match($0, /(^|[^_[:alnum:]])version[[:space:]]*=[[:space:]]*"[^"]+"/) {
      if (src != "") {
        seg = substr($0, RSTART, RLENGTH)
        sub(/^[^"]*"/, "", seg)
        sub(/".*$/, "", seg)
        print src, seg
        src = ""
      }
    }
  ' "$@"
}

# apply_tf_update FILE SRC NEW rewrites the version string that follows the
# `source = "SRC"` line, leaving the rest of the line untouched. Files without
# that source are left completely alone so unrelated configs keep their exact
# bytes (including whether they end in a newline).
apply_tf_update() {
  local file="$1" src="$2" new="$3"
  grep -qE "source[[:space:]]*=[[:space:]]*\"$src\"" "$file" || return 0
  awk -v src="$src" -v new="$new" '
    !done && index($0, "source") && match($0, /source[[:space:]]*=[[:space:]]*"[^"]+"/) {
      seg = substr($0, RSTART, RLENGTH)
      sub(/^[^"]*"/, "", seg)
      sub(/".*$/, "", seg)
      if (seg == src) pending = 1
    }
    pending && match($0, /(^|[^_[:alnum:]])version[[:space:]]*=[[:space:]]*"[^"]+"/) {
      head = substr($0, 1, RSTART - 1)
      seg = substr($0, RSTART, RLENGTH)
      tail = substr($0, RSTART + RLENGTH)
      sub(/"[^"]*"/, "\"" new "\"", seg)
      print head seg tail
      pending = 0
      done = 1
      next
    }
    { print }
  ' "$file" >"$file.update-tmp" && mv -- "$file.update-tmp" "$file"
}

# apply_tf_required_version FILE MINOR rewrites a required_version constraint so
# it tracks the TERRAFORM_VERSION pin, as main.tf's comment requires.
apply_tf_required_version() {
  local file="$1" minor="$2"
  awk -v minor="$minor" '
    match($0, /required_version[[:space:]]*=[[:space:]]*"[^"]*"/) {
      head = substr($0, 1, RSTART - 1)
      seg = substr($0, RSTART, RLENGTH)
      tail = substr($0, RSTART + RLENGTH)
      sub(/"[^"]*"/, "\"~> " minor "\"", seg)
      print head seg tail
      next
    }
    { print }
  ' "$file" >"$file.update-tmp" && mv -- "$file.update-tmp" "$file"
}

bump_terraform() {
  echo
  echo "== terraform =="
  local files=()
  while IFS= read -r f; do
    files+=("$f")
  done < <(tf_files)
  if [[ ${#files[@]} -eq 0 ]]; then
    echo "  no *.tf files found, nothing to do"
    return 0
  fi
  for f in "${files[@]}"; do
    echo "  config: ${f#"$TARGET"/}"
  done

  local pairs src current newest pending=()
  pairs="$(tf_provider_pairs "${files[@]}")"
  if [[ -z "$pairs" ]]; then
    echo "  no required_providers pins found"
  else
    while read -r src current; do
      [[ -n "$src" ]] || continue
      if ! newest="$(latest_tf_provider "$src")" || [[ -z "$newest" ]]; then
        printf '  %-26s %-10s could not resolve registry version\n' "$src" "$current"
        continue
      fi
      case "$(vercmp "$current" "$newest")" in
        same)
          printf '  %-26s %-10s up to date\n' "$src" "$current"
          ;;
        newer)
          printf '  %-26s %-10s -> %s\n' "$src" "$current" "$newest"
          UPDATE_COUNT=$((UPDATE_COUNT + 1))
          if [[ "$DRY_RUN" == true ]]; then
            continue
          fi
          for f in "${files[@]}"; do
            apply_tf_update "$f" "$src" "$newest"
          done
          pending+=("$src $newest")
          ;;
        *)
          printf '  %-26s %-10s ahead of registry (%s), left alone\n' "$src" "$current" "$newest"
          ;;
      esac
    done <<<"$pairs"

    # Re-read the rewritten files: a provider whose pin did not land is a hard
    # error rather than a silently half-updated config.
    if [[ ${#pending[@]} -gt 0 ]]; then
      local want src_new landed
      for want in "${pending[@]}"; do
        src_new="${want##* }"
        landed="$(tf_provider_pairs "${files[@]}" |
          awk -v v="$src_new" '$2 == v { found = 1 } END { print found + 0 }')"
        if [[ "$landed" != "1" ]]; then
          echo "  error: no provider pin landed at $src_new" >&2
          exit 1
        fi
      done
    fi
  fi

  # required_version tracks the TERRAFORM_VERSION pin's minor, per the comment
  # in main.tf. Only rewrite it when the minor actually moves.
  local tv minor
  tv="$(global_pin_value TERRAFORM_VERSION)" || true
  if [[ -n "$tv" ]]; then
    minor="${tv%.*}"
    for f in "${files[@]}"; do
      if grep -qE "required_version[[:space:]]*=[[:space:]]*\"~> $minor\"" "$f"; then
        printf '  %-26s %-10s aligned with TERRAFORM_VERSION\n' "required_version" "~> $minor"
        continue
      fi
      if grep -qE 'required_version[[:space:]]*=' "$f"; then
        printf '  %-26s -> ~> %s\n' "required_version" "$minor"
        UPDATE_COUNT=$((UPDATE_COUNT + 1))
        [[ "$DRY_RUN" == true ]] || apply_tf_required_version "$f" "$minor"
      fi
    done
  fi
}

# --- step 5: re-vendor third_party ------------------------------------------

# The vendored proto trees are produced by make targets that download and
# unzip upstream archives (googleapis and grpc-gateway from their master
# branches, protoc's includes from PROTOC_VERSION). Those targets have no
# prerequisites, so a build keeps the checked-in trees; deleting a target's
# LICENSE file is what makes make vendor it again.
VENDOR_TARGETS=(
  "third_party/google_protobuf/include/google/LICENSE"
  "third_party/grpc_gateway/include/protoc-gen-openapiv2/LICENSE"
)

refresh_vendored() {
  echo
  echo "== third_party vendored sources =="
  local targets=() t mk
  for t in "${VENDOR_TARGETS[@]}"; do
    for mk in "$TARGET"/Makefile*.mk; do
      if grep -qE "^${t}:" "$mk"; then
        targets+=("$t")
        break
      fi
    done
  done
  if [[ ${#targets[@]} -eq 0 ]]; then
    echo "  no vendored-source targets in the makefiles, nothing to do"
    return 0
  fi
  if [[ "$DRY_RUN" == true ]]; then
    for t in "${targets[@]}"; do
      echo "  would re-vendor: $t"
    done
    return 0
  fi

  for t in "${targets[@]}"; do
    rm -f "$TARGET/$t"
  done
  if ! make -C "$TARGET" "${targets[@]}"; then
    echo "  error: re-vendor failed" >&2
    return 1
  fi
  if [[ -n "$(git_in status --porcelain -- third_party 2>/dev/null)" ]]; then
    echo "  re-vendored: third_party changed"
    UPDATE_COUNT=$((UPDATE_COUNT + 1))
  else
    echo "  re-vendored: already current"
  fi
}

# --- step 6: move the Go modules to their latest releases -------------------

# The bump is delegated to the repo's own "make deps" target (go get -u
# ./... + go mod tidy + go mod download) so a scheduled run moves Go modules
# exactly the way a local "make deps" does. "go mod verify" runs afterwards
# because the deps target checks nothing about the downloaded checksums.
update_go() {
  echo
  echo "== go.mod =="
  if [[ ! -f "$TARGET/go.mod" ]]; then
    echo "  skip: no go.mod" >&2
    return 0
  fi
  if ! command -v go >/dev/null 2>&1; then
    echo "  skip: go not installed" >&2
    return 0
  fi
  if [[ "$DRY_RUN" == true ]]; then
    echo "  would run: make deps && go mod verify"
    return 0
  fi
  if ! (
    cd "$TARGET" &&
      make deps &&
      go mod verify
  ); then
    echo "  error: go module update failed" >&2
    return 1
  fi
  if [[ -n "$(git_in status --porcelain -- go.mod go.sum 2>/dev/null)" ]]; then
    echo "  go modules: updated"
    UPDATE_COUNT=$((UPDATE_COUNT + 1))
  else
    echo "  go modules: already current"
  fi
}

# --- run --------------------------------------------------------------------

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required." >&2
  exit 1
fi

SYNC_TMP="$(mktemp -d)"
HELPERS_MK="$SYNC_TMP/print-vars.mk"
cat >"$HELPERS_MK" <<'EOF'
print-%:
	@echo " $($*)"

include $(PINFILE)
EOF

echo "Target : $TARGET"
if [[ "$DRY_RUN" == true ]]; then
  echo "(dry run - no changes will be made)"
fi
echo

if [[ "$DO_SYNC" == true ]]; then
  sync_template
fi
if [[ "$DO_DEPS" == true ]]; then
  bump_deps
fi
if [[ "$DO_TF" == true ]]; then
  bump_terraform
fi
if [[ "$DO_VENDORED" == true ]]; then
  refresh_vendored
fi
update_go

echo
if [[ "$UPDATE_COUNT" -eq 0 ]]; then
  echo "Already up to date - nothing to change."
else
  echo "Made $UPDATE_COUNT update(s)."
  if [[ "$DRY_RUN" != true ]]; then
    echo
    echo "Working tree:"
    git_in status --short || true
    if [[ -n "$(git_in status --porcelain -- Makefile_core.mk Makefile_*.mk install third_party go.mod go.sum 2>/dev/null)" ]]; then
      echo
      echo "Diff:"
      git_in --no-pager diff --stat -- Makefile_core.mk Makefile_*.mk install
      git_in --no-pager diff -- Makefile_core.mk Makefile_*.mk install go.mod go.sum || true
      echo
      echo "third_party changes (summary):"
      git_in --no-pager diff --stat -- third_party | tail -n 20 || true
    fi
    echo
    echo "Nothing was committed or pushed. Review, then run 'make tools' to fetch the new toolchain."
  fi
fi

if [[ "$CHECK" == true && "$UPDATE_COUNT" -gt 0 ]]; then
  exit 1
fi
