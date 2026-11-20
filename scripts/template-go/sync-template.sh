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
#   * every root dotfile config   (e.g. .golangci.yml, .yamllint.yml)
#   * the whole .github/ directory (.github/workflows/deploy-*.yaml are preserved)
#   * the whole scripts/template-go/ directory (the standard scripts, this one
#     included)
# with the upstream equivalents. Files upstream no longer ships are deleted,
# files it now ships are added, and shared files are overwritten.
#
# Nothing is deleted inside those two directories beyond the wholesale
# replacement. A project customizes what lives in there, so a file that stops
# shipping upstream is removed by name in RETIRED below instead: when the
# template retires a workflow, its paths get listed here so every consumer
# drops it and the sync says which file went away.
#
# That includes .github/workflows/create-release.yaml, the tag-triggered
# release pipeline. Its release files are globs (build/release/*-linux_amd64
# and so on) that match every app in ALL_APPS, so it needs no per-project
# edits after the sync.
#
# Every other file directly under scripts/ (e.g. the CI hooks prepare-ci.sh
# and Initialize-CI.ps1), plus AGENTS.md and a few root dotfiles
# (.codespellignore and .gitignore), is the exception: it is copied in only
# when the target does not already have one and is never overwritten, because
# the whole point of those is to be customized. A project can edit them freely
# and re-running the sync keeps its version.
#
# This is a HARD replace: local edits to any of the managed files are
# discarded and project-specific versions (e.g. a customized .golangci.yml) are
# clobbered. Prefer running it on a branch/PR, or with --dry-run first, so the
# change is reviewable.
#
# Nothing runs it for you: the template ships no workflow that syncs on a
# schedule, because reviewing a hard replace needs a pull request and a
# scheduled job can only open one with a long-lived token handed to a
# third-party action. Run it from a branch and open the pull request yourself.
#
# Usage: scripts/template-go/sync-template.sh [options]
#
# Safe to re-run: it converges on the source tree every time.

set -euo pipefail

# Defaults live here (not just in usage) so --help and the actual behavior
# always agree.
SCRIPT_SOURCE_REPO="git@github.com:cloudfra/template-go.git"
SCRIPT_SOURCE_REF="main"

# The standard scripts: always replaced wholesale by the sync.
STANDARD_DIR="scripts/template-go"
# Everything directly under here (outside STANDARD_DIR) is customizable.
SEED_DIR="scripts"
# Root files that are seeded like SEED_DIR rather than replaced like the
# other managed files: a project is expected to customize them, so the sync
# only copies them in when missing.
SEED_ROOT_FILES=(".codespellignore" ".gitignore" "AGENTS.md")

# Files the template has retired, deleted from the target by name. Listed one
# by one rather than worked out from the replaced directories: a project
# customizes what lives inside .github/ and scripts/template-go/, so the sync
# removes nothing in there beyond the replacement it already makes. Add the
# paths of a workflow (and any file that existed only for it) when the
# template stops shipping it.
RETIRED=(
  ".github/dependency-update-pr-body.md"
  ".github/template-sync-pr-body.md"
  ".github/workflows/dependency-update.yaml"
  ".github/workflows/template-sync.yaml"
  "scripts/template-go/existing-pathspecs.sh"
)

# --- argument parsing -------------------------------------------------------

usage() {
  cat >&2 <<EOF
Usage: $0 [options]

Sync template-managed files (Makefile_*.mk, root dotfiles, .github/,
scripts/template-go/) from upstream template-go into the target project. This
is a hard replace and is safe to re-run. .github/workflows/deploy-*.yaml are
preserved, and the other files directly under scripts/ (e.g.
scripts/prepare-ci.sh, scripts/Initialize-CI.ps1), AGENTS.md,
.codespellignore and .gitignore are added only when missing and never
overwritten. A file the template no longer ships - a retired workflow, for
example - is deleted and reported by name.

Options:
  -s, --source DIR   Read upstream files from local DIR instead of cloning
  -r, --repo URL     Clone URL to use (default: $SCRIPT_SOURCE_REPO)
      --ref REF      Branch/tag to clone (default: $SCRIPT_SOURCE_REF)
  -t, --target DIR   Target project root (default: the project this script
                     sits in, i.e. two levels above scripts/template-go/)
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

# --- resolve source ---------------------------------------------------------

# Scratch space for the upstream clone and for the files preserved across the
# .github/ replacement.
SYNC_TMP="$(mktemp -d)"
cleanup() {
  if [[ -n "${SYNC_TMP:-}" && -d "${SYNC_TMP:-}" ]]; then
    rm -rf -- "$SYNC_TMP"
  fi
  return 0
}
trap cleanup EXIT INT TERM

CLONED=false
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
  SRC_CLONE="$SYNC_TMP/template-go"
  echo "Fetching upstream template: $TEMPLATE_GO_REPO @ $TEMPLATE_GO_REF"
  git clone --quiet --depth 1 --branch "$TEMPLATE_GO_REF" "$TEMPLATE_GO_REPO" "$SRC_CLONE"
  SOURCE="$SRC_CLONE"
  CLONED=true
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
#   root dotfiles    (e.g. .golangci.yml, .yamllint.yml)
# .git and .github are excluded (.github is synced as a directory instead),
# as are SEED_ROOT_FILES (seeded instead).
list_managed_files() {
  local root="$1" f
  local excludes=()
  for f in "${SEED_ROOT_FILES[@]}"; do
    excludes+=(! -name "$f")
  done
  ( cd "$root" && find . -maxdepth 1 -type f \( -name 'Makefile_*.mk' -o -name '.*' \) ! -name 'Makefile_testassets.mk' ! -name '.git' ! -name '.github' "${excludes[@]}" )
}

# list_seed_files ROOT prints the SEED_ROOT_FILES present in ROOT and the
# regular files directly under ROOT/SEED_DIR (so not the standard scripts in
# STANDARD_DIR) as paths relative to ROOT.
list_seed_files() {
  local root="$1" f
  for f in "${SEED_ROOT_FILES[@]}"; do
    if [[ -f "$root/$f" ]]; then
      echo "$f"
    fi
  done
  if [[ -d "$root/$SEED_DIR" ]]; then
    ( cd "$root" && find "$SEED_DIR" -maxdepth 1 -type f | LC_ALL=C sort )
  fi
}

# list_preserved_files ROOT prints the project-owned workflows that survive
# the .github/ replacement, as paths relative to ROOT.
list_preserved_files() {
  local root="$1"
  if [[ -d "$root/.github/workflows" ]]; then
    ( cd "$root" && find .github/workflows -maxdepth 1 -type f -name 'deploy-*.yaml' | LC_ALL=C sort )
  fi
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

T_HAS_STANDARD=0
S_HAS_STANDARD=0
[[ -d "$TARGET/$STANDARD_DIR" ]] && T_HAS_STANDARD=1
[[ -d "$SOURCE/$STANDARD_DIR" ]] && S_HAS_STANDARD=1

# --- compute the plan -------------------------------------------------------

ADD=()
UPDATE=()
DELETE=()
SEED=()
KEEP=()
PRESERVE=()
LEGACY=()

# Seed files are per-project customization points, so they are treated
# differently from everything above: copied in only when the target has no
# copy of its own, and never overwritten once it does.
while IFS= read -r f; do
  [[ -n "$f" ]] || continue
  if [[ -e "$TARGET/$f" ]]; then
    KEEP+=("$f")
  else
    SEED+=("$f")
  fi
done < <(list_seed_files "$SOURCE")

# Projects synced before the standard scripts moved into STANDARD_DIR still
# have stale copies directly under SEED_DIR. The workflows only call the
# STANDARD_DIR copies now, so the old ones are removed rather than left to
# drift.
if (( S_HAS_STANDARD )); then
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue
    legacy="$SEED_DIR/$(basename "$f")"
    if [[ -f "$TARGET/$legacy" && ! -f "$SOURCE/$legacy" ]]; then
      LEGACY+=("$legacy")
    fi
  done < <(cd "$SOURCE" && find "$STANDARD_DIR" -maxdepth 1 -type f | LC_ALL=C sort)
fi

if (( T_HAS_GITHUB )); then
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue
    PRESERVE+=("$f")
  done < <(list_preserved_files "$TARGET")
fi

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
    seed)
      # A fresh project may not have a scripts/ directory yet.
      mkdir -p -- "$(dirname "$TARGET/$label")"
      cp -a -- "$SOURCE/$label" "$TARGET/$label"
      ;;
    keep | preserve)
      # Printed in both modes: it tells the caller that a file they are
      # expected to customize survived the sync untouched.
      printf '  %-7s %s\n' "$action" "$label"
      ;;
    delete)
      rm -rf -- "${TARGET:?}/$label"
      ;;
    replace)
      # Whole-directory replacement (used for .github/ and scripts/template-go/).
      # Removing before copying gives every file a new inode, which is what
      # lets this script replace its own running copy: bash keeps reading the
      # old, now unlinked, file.
      rm -rf -- "${TARGET:?}/$label"
      mkdir -p -- "$(dirname "$TARGET/$label")"
      cp -a -- "$SOURCE/$label" "$TARGET/$label"
      ;;
  esac
}

# delete_retired_files removes the RETIRED paths from the target, by name and
# before the wholesale replacements. Under --dry-run act prints them; in a
# real run this prints them, so a project always sees which file the template
# took away instead of finding it in a diff afterwards.
delete_retired_files() {
  local f
  for f in "${RETIRED[@]}"; do
    if [[ -e "$TARGET/$f" ]]; then
      [[ "$DRY_RUN" == true ]] || printf '  %-7s %s\n' delete "$f"
      act delete "$f"
    fi
  done
}

# stash_preserved / restore_preserved carry PRESERVE across the .github/
# replacement. Under --dry-run there is nothing to carry.
stash_preserved() {
  local f
  [[ "$DRY_RUN" == true ]] && return 0
  for f in ${PRESERVE[@]+"${PRESERVE[@]}"}; do
    mkdir -p -- "$(dirname "$SYNC_TMP/preserve/$f")"
    cp -a -- "$TARGET/$f" "$SYNC_TMP/preserve/$f"
  done
}
restore_preserved() {
  local f
  for f in ${PRESERVE[@]+"${PRESERVE[@]}"}; do
    act preserve "$f"
    [[ "$DRY_RUN" == true ]] && continue
    mkdir -p -- "$(dirname "$TARGET/$f")"
    cp -a -- "$SYNC_TMP/preserve/$f" "$TARGET/$f"
  done
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
for f in ${LEGACY[@]+"${LEGACY[@]}"}; do
  act delete "$f"
done
for f in ${SEED[@]+"${SEED[@]}"}; do
  act seed "$f"
done
for f in ${KEEP[@]+"${KEEP[@]}"}; do
  act keep "$f"
done
delete_retired_files
if (( S_HAS_STANDARD )); then
  act replace "$STANDARD_DIR"
elif (( T_HAS_STANDARD )); then
  act delete "$STANDARD_DIR"
fi
stash_preserved
if (( S_HAS_GITHUB )); then
  act replace ".github"
elif (( T_HAS_GITHUB )); then
  act delete ".github"
fi
restore_preserved
echo

echo "Done."
if [[ "$DRY_RUN" != true ]]; then
  echo "Review the changes (e.g. 'git status' / 'git diff') and commit if they look right."
fi
if [[ "$CLONED" == true ]]; then
  echo "(cleaned up temporary clone)"
fi
