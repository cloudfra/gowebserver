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

# Re-sync the "template-managed" files of a project back to the matching
# upstream template-go versions, so a repo created from this template can pick
# up template changes (new lint config, CI workflows, Makefile targets, ...)
# without hand-merging.
#
# In the target project it replaces:
#   * every Makefile_*.mk        (Makefile and Makefile_testassets.mk are preserved)
#   * every root dotfile config   (e.g. .gitignore, .golangci.yml, .yamllint.yml)
#   * the whole .github/ directory
# with the upstream equivalents. Files upstream no longer ships are deleted,
# files it now ships are added, and shared files are overwritten.
#
# This is a HARD replace: local edits to any of those files are discarded and
# project-specific versions (e.g. a customized .gitignore) are clobbered.
# Prefer running it on a branch/PR, or with --dry-run first, so the change is
# reviewable.
#
# Usage: scripts/sync-template.sh [options]
#
# Safe to re-run: it converges on the source tree every time.

set -euo pipefail

# Defaults live here (not just in usage) so --help and the actual behavior
# always agree.
SCRIPT_SOURCE_REPO="git@github.com:cloudfra/template-go.git"
SCRIPT_SOURCE_REF="main"

# --- argument parsing -------------------------------------------------------

usage() {
  cat >&2 <<EOF
Usage: $0 [options]

Sync template-managed files (Makefile_*.mk, root dotfiles, .github/) from
upstream template-go into the target project. This is a hard replace and is
safe to re-run.

Options:
  -s, --source DIR   Read upstream files from local DIR instead of cloning
  -r, --repo URL     Clone URL to use (default: $SCRIPT_SOURCE_REPO)
      --ref REF      Branch/tag to clone (default: $SCRIPT_SOURCE_REF)
  -t, --target DIR   Target project root (default: the project this script
                     sits in, i.e. the parent of its scripts/ directory)
  -n, --dry-run      Print the changes without touching anything
  -h, --help         Show this help

Environment overrides: TEMPLATE_GO_DIR, TEMPLATE_GO_REPO, TEMPLATE_GO_REF.
Requires git (only when not using --source).
EOF
}

TEMPLATE_GO_DIR="${TEMPLATE_GO_DIR:-}"
TEMPLATE_GO_REPO="${TEMPLATE_GO_REPO:-$SCRIPT_SOURCE_REPO}"
TEMPLATE_GO_REF="${TEMPLATE_GO_REF:-$SCRIPT_SOURCE_REF}"
TARGET=""
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    -s | --source)
      TEMPLATE_GO_DIR="${2:?value required for $1}"
      shift 2
      ;;
    -r | --repo)
      TEMPLATE_GO_REPO="${2:?value required for $1}"
      shift 2
      ;;
    --ref)
      TEMPLATE_GO_REF="${2:?value required for $1}"
      shift 2
      ;;
    -t | --target)
      TARGET="${2:?value required for $1}"
      shift 2
      ;;
    -n | --dry-run)
      DRY_RUN=true
      shift
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
  # This script is expected to live in <project>/scripts/, so the target is
  # the parent of the directory that holds it.
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  TARGET="$(dirname "$SCRIPT_DIR")"
fi
if [[ ! -d "$TARGET" ]]; then
  echo "Target project root not found: $TARGET" >&2
  exit 1
fi
TARGET="$(cd "$TARGET" && pwd)"
TARGET="${TARGET%/}"

# --- resolve source ---------------------------------------------------------

SYNC_TMP=""
cleanup() {
  if [[ -n "${SYNC_TMP:-}" && -d "${SYNC_TMP:-}" ]]; then
    rm -rf -- "$SYNC_TMP"
  fi
  return 0
}
trap cleanup EXIT INT TERM

SOURCE="$TEMPLATE_GO_DIR"
if [[ -n "$SOURCE" ]]; then
  if [[ ! -d "$SOURCE" ]]; then
    echo "Source directory not found: $SOURCE" >&2
    exit 1
  fi
else
  if ! command -v git >/dev/null 2>&1; then
    echo "git is required to clone the upstream template (or pass --source DIR)." >&2
    exit 1
  fi
  SYNC_TMP="$(mktemp -d)"
  SRC_CLONE="$SYNC_TMP/template-go"
  echo "Fetching upstream template: $TEMPLATE_GO_REPO @ $TEMPLATE_GO_REF"
  git clone --quiet --depth 1 --branch "$TEMPLATE_GO_REF" "$TEMPLATE_GO_REPO" "$SRC_CLONE"
  SOURCE="$SRC_CLONE"
fi
SOURCE="$(cd "$SOURCE" && pwd)"
SOURCE="${SOURCE%/}"

# The source must actually look like a template-go tree before we delete
# anything in the target.
if [[ ! -f "$SOURCE/Makefile" ]]; then
  echo "Source doesn't look like a template-go tree (no Makefile in $SOURCE)." >&2
  exit 1
fi

# Guard against deleting the target's managed files and then reading the very
# same ones back from the same tree.
if [[ "$SOURCE" == "$TARGET" ]]; then
  echo "Source and target are the same directory: $TARGET" >&2
  echo "Refusing to clobber itself. Pass a different --source." >&2
  exit 1
fi

echo "Target : $TARGET"
echo "Source : $SOURCE"
if [[ "$DRY_RUN" == true ]]; then
  echo "(dry run - no changes will be made)"
fi
echo

# --- discover the managed file sets ----------------------------------------

# list_managed_files ROOT prints the managed regular files directly under ROOT
# as bare relative paths (no leading ./), one per line:
#   Makefile_*.mk   (excluding Makefile_testassets.mk)
#   root dotfiles    (e.g. .gitignore, .golangci.yml)
# .git and .github are excluded (.github is synced as a directory instead).
list_managed_files() {
  local root="$1"
  ( cd "$root" && find . -maxdepth 1 -type f \( -name 'Makefile_*.mk' -o -name '.*' \) ! -name 'Makefile_testassets.mk' ! -name '.git' ! -name '.github' )
}

declare -A T_SET=() S_SET=()
TARGET_FILES=()
SOURCE_FILES=()

while IFS= read -r f; do
  f="${f#./}"
  [[ -n "$f" ]] || continue
  TARGET_FILES+=("$f")
  T_SET["$f"]=1
done < <(list_managed_files "$TARGET")

while IFS= read -r f; do
  f="${f#./}"
  [[ -n "$f" ]] || continue
  SOURCE_FILES+=("$f")
  S_SET["$f"]=1
done < <(list_managed_files "$SOURCE")

T_HAS_GITHUB=0
S_HAS_GITHUB=0
[[ -d "$TARGET/.github" ]] && T_HAS_GITHUB=1
[[ -d "$SOURCE/.github" ]] && S_HAS_GITHUB=1

# --- compute the plan -------------------------------------------------------

ADD=()
UPDATE=()
DELETE=()

for f in "${TARGET_FILES[@]}"; do
  if [[ -n "${S_SET["$f"]:-}" ]]; then
    UPDATE+=("$f")
  else
    DELETE+=("$f")
  fi
done
for f in "${SOURCE_FILES[@]}"; do
  if [[ -z "${T_SET["$f"]:-}" ]]; then
    ADD+=("$f")
  fi
done

# --- apply (or dry-run) -----------------------------------------------------

# act ACTION LABEL prints the label under --dry-run; otherwise performs it.
act() {
  local action="$1" label="$2"
  if [[ "$DRY_RUN" == true ]]; then
    printf '  %-7s %s\n' "$action" "$label"
    return 0
  fi
  case "$action" in
    add | update)
      cp -a -- "$SOURCE/$label" "$TARGET/$label"
      ;;
    delete)
      rm -f -- "$TARGET/$label"
      ;;
    replace)
      # Whole-directory replacement (used for .github/).
      rm -rf -- "${TARGET:?}/$label"
      cp -a -- "$SOURCE/$label" "$TARGET/$label"
      ;;
  esac
}

echo "Plan:"
for f in ${ADD[@]+"${ADD[@]}"}; do
  act add "$f"
done
for f in ${UPDATE[@]+"${UPDATE[@]}"}; do
  act update "$f"
done
for f in ${DELETE[@]+"${DELETE[@]}"}; do
  act delete "$f"
done
if (( S_HAS_GITHUB )); then
  act replace ".github"
elif (( T_HAS_GITHUB )); then
  act delete ".github"
fi
echo

echo "Done."
if [[ "$DRY_RUN" != true ]]; then
  echo "Review the changes (e.g. 'git status' / 'git diff') and commit if they look right."
fi
if [[ -n "${SYNC_TMP:-}" ]]; then
  echo "(cleaned up temporary clone)"
fi
