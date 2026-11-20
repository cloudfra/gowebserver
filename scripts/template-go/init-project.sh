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

# Turn a fresh copy of template-go into a new project, in one pass:
#
#   1. set the Go module path (go.mod, imports, Makefile GO_PACKAGE, proto
#      go_package) from the `origin` remote, e.g. github.com/OWNER/REPO
#   2. rename the starter binary: cmd/exampleapp/exampleapp.go becomes
#      cmd/APP/APP.go, internal/exampleapp becomes internal/PKG, and
#      proto/exampleapp.proto becomes proto/PKG.proto
#   3. point ALL_APPS and REGISTRY in the Makefile at the new binary and
#      container registry
#   4. rewrite the remaining template references (README title, intro and
#      badges, Dockerfile labels and entrypoints, CONTRIBUTING, SECURITY) to
#      the new project name, description and URLs
#   5. tidy up: `go mod tidy`, then `make lint`, which also rewrites whatever
#      the renames left unformatted (e.g. a README table whose column got
#      wider)
#
# APP is the binary name. It is also the cmd/ directory, the docker image name
# and the ALL_APPS entry, so it must be lowercase ([a-z0-9_-]). PKG is the Go
# package name of internal/PKG; it defaults to APP with the `-` and `_` removed,
# since Go package names are plain identifiers.
#
# Template-managed files are deliberately left alone: .github/,
# scripts/template-go/ and every Makefile_*.mk are replaced wholesale by
# sync-template.sh, so editing them here would be undone by the next sync.
# third_party/ is vendored upstream code and is never touched either.
#
# Each value comes from its flag if one is passed, otherwise it is inferred
# (the module path from the `origin` remote, the rest from the module path).
# Only the values that are still missing - always the description, unless
# --description is passed - are asked for on the terminal, and the script fails
# if a required value is missing and there is no terminal to ask on. The
# settings are then shown for a final check before anything changes; pass
# --no-confirm to skip that pause, e.g. in CI.
#
# Nothing is committed and nothing is pushed: the script stages its changes
# (renames, edits and the tidy-up) so they are ready for the initial commit
# once reviewed. If the tree had uncommitted changes to begin with (--force),
# only the files the script itself edited are staged.
#
# Usage: scripts/template-go/init-project.sh [options]
#
# Safe to re-run: once cmd/exampleapp is gone it reports that the project is
# already initialized and exits.

# The *_SUBST variables are perl programs: $ENV{...} is expanded by perl, not
# by the shell, hence the single quotes.
# shellcheck disable=SC2016

set -euo pipefail

# The template's own values: the strings this script searches for.
TEMPLATE_MODULE="github.com/cloudfra/template-go"
TEMPLATE_REPO_PATH="cloudfra/template-go"
TEMPLATE_APP="exampleapp"
TEMPLATE_APP_URL="github.com/cloudfra/exampleapp"
TEMPLATE_PROTO_PACKAGE="cloudfra.exampleapp"
TEMPLATE_REGISTRY="ghcr.io/cloudfra"
TEMPLATE_DESCRIPTION="Example application."
TEMPLATE_CODECOV_TOKEN="?token=UVApxhg6z7"

# --- argument parsing -------------------------------------------------------

usage() {
  cat >&2 <<EOF
Usage: $0 [options]

Initialize a new project created from template-go: set the Go module path,
rename the exampleapp binary, and replace the remaining template references.
Values not passed as flags are inferred from the 'origin' git remote where
possible and prompted for otherwise; the settings are shown for confirmation
before anything changes. Changes are staged but not committed.

Options:
  -m, --module PATH        Go module path (default: from the origin remote,
                           e.g. github.com/OWNER/REPO)
  -a, --app NAME           Binary name, replacing exampleapp
                           (default: the repository name)
  -p, --package NAME       Go package name for internal/NAME
                           (default: the binary name without '-' and '_')
  -d, --description TEXT   One-line project description (README, image labels;
                           prompted for if not passed)
      --name NAME          Project name for the README title
                           (default: the repository name)
      --registry REGISTRY  Container registry for 'make images'
                           (default: ghcr.io/OWNER)
      --remote NAME        Git remote to infer the module path from
                           (default: origin)
  -y, --no-confirm         Don't pause to confirm the settings before changing
                           files
  -f, --force              Run even if the working tree has uncommitted changes
      --no-tidy            Skip the final go mod tidy and make lint
  -n, --dry-run            Report what would change without touching anything
  -t, --target DIR         Project root to initialize (default: the project this
                           script sits in, i.e. two levels above
                           scripts/template-go/)
  -h, --help               Show this help

Requires git and perl; the tidy step also needs go and make.
EOF
}

MODULE=""
APP=""
PACKAGE=""
DESCRIPTION=""
NAME=""
REGISTRY=""
REMOTE="origin"
CONFIRM=true
FORCE=false
DRY_RUN=false
DO_TIDY=true
TARGET=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -m | --module)
      MODULE="${2:?value required for $1}"
      shift 2
      ;;
    -a | --app)
      APP="${2:?value required for $1}"
      shift 2
      ;;
    -p | --package)
      PACKAGE="${2:?value required for $1}"
      shift 2
      ;;
    -d | --description)
      DESCRIPTION="${2:?value required for $1}"
      shift 2
      ;;
    --name)
      NAME="${2:?value required for $1}"
      shift 2
      ;;
    --registry)
      REGISTRY="${2:?value required for $1}"
      shift 2
      ;;
    --remote)
      REMOTE="${2:?value required for $1}"
      shift 2
      ;;
    -y | --no-confirm)
      CONFIRM=false
      shift
      ;;
    -f | --force)
      FORCE=true
      shift
      ;;
    --no-tidy)
      DO_TIDY=false
      shift
      ;;
    -n | --dry-run)
      DRY_RUN=true
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

if [[ -z "$TARGET" ]]; then
  # This script is expected to live in <project>/scripts/template-go/, so the
  # project root is two levels up.
  TARGET="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fi
cd "$TARGET"

if [[ ! -d "cmd/$TEMPLATE_APP" ]]; then
  echo "cmd/$TEMPLATE_APP not found in $TARGET - this project looks already initialized."
  exit 0
fi

IN_GIT=false
STARTED_CLEAN=true
if git rev-parse --git-dir >/dev/null 2>&1; then
  IN_GIT=true
  [[ -z "$(git status --porcelain)" ]] || STARTED_CLEAN=false
fi
if ! $FORCE && ! $DRY_RUN && ! $STARTED_CLEAN; then
  echo "The working tree has uncommitted changes; commit or stash them first (or pass --force)." >&2
  exit 1
fi

# --- inferring defaults -----------------------------------------------------

# module_from_remote turns the remote's URL into a Go module path
# (HOST/OWNER/REPO). Handles the scp-like SSH, ssh:// and https:// forms
# `git remote -v` prints, for any host.
module_from_remote() {
  local url
  url="$(git remote get-url "$REMOTE" 2>/dev/null)" || return 1
  url="${url%.git}"
  url="${url%/}"
  case "$url" in
    *://*)
      url="${url#*://}" # drop the scheme
      url="${url#*@}"   # drop any user[:password]@
      ;;
    *@*:*)
      url="${url#*@}"
      url="${url/://}" # host:owner/repo -> host/owner/repo
      ;;
    *) return 1 ;;
  esac
  if [[ "$url" =~ ^([^/:]+):[0-9]+(/.*)$ ]]; then
    url="${BASH_REMATCH[1]}${BASH_REMATCH[2]}" # drop an explicit port
  fi
  printf '%s\n' "$url"
}

# require VAR "Question" "--flag" leaves VAR alone if it is already set (from
# its flag or by inference); otherwise it asks for it on the terminal, and fails
# if there is no terminal or the answer is empty.
require() {
  local var="$1" question="$2" flag="$3" answer=""
  if [[ -n "${!var}" ]]; then
    return
  fi
  if [[ -t 0 ]]; then
    read -r -p "$question: " answer
  fi
  if [[ -z "$answer" ]]; then
    echo "$question is required: pass $flag." >&2
    exit 1
  fi
  printf -v "$var" '%s' "$answer"
}

if [[ -z "$MODULE" ]]; then
  MODULE="$(module_from_remote || true)"
  if [[ "$MODULE" == "$TEMPLATE_MODULE" ]]; then
    # Still pointing at the template itself (a plain clone): not the new
    # project's path.
    echo "The '$REMOTE' remote points at template-go itself, so the module path can't be inferred from it." >&2
    MODULE=""
  fi
fi
require MODULE "Go module path (e.g. github.com/OWNER/REPO)" "--module"
if [[ ! "$MODULE" =~ ^[A-Za-z0-9.-]+(/[A-Za-z0-9._~-]+)+$ ]]; then
  echo "Invalid module path '$MODULE' (expected e.g. github.com/OWNER/REPO)." >&2
  exit 1
fi

REPO_NAME="${MODULE##*/}"
MODULE_PARENT="${MODULE%/*}"
OWNER="${MODULE_PARENT##*/}"
MODULE_HOST="${MODULE%%/*}"
OWNER_LOWER="$(printf '%s' "$OWNER" | tr '[:upper:]' '[:lower:]')"
REPO_LOWER="$(printf '%s' "$REPO_NAME" | tr '[:upper:]' '[:lower:]')"

NAME="${NAME:-$REPO_NAME}"
REGISTRY="${REGISTRY:-ghcr.io/$OWNER_LOWER}"

# The repository name is only a usable binary name if it fits the rules below
# once lowercased (e.g. not "my.repo"); otherwise ask.
if [[ -z "$APP" && "$REPO_LOWER" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
  APP="$REPO_LOWER"
fi
require APP "Binary name" "--app"
if [[ ! "$APP" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
  echo "Invalid binary name '$APP': use lowercase letters, digits, '-' and '_'." >&2
  exit 1
fi

if [[ -z "$PACKAGE" && "${APP//[-_]/}" =~ ^[a-z] ]]; then
  PACKAGE="${APP//[-_]/}"
fi
require PACKAGE "Go package name" "--package"
if [[ ! "$PACKAGE" =~ ^[a-z][a-z0-9]*$ ]]; then
  echo "Invalid Go package name '$PACKAGE': use lowercase letters and digits, starting with a letter." >&2
  exit 1
fi

require DESCRIPTION "Project description" "--description"

# Protobuf packages are dot-separated identifiers, so derive one from the
# owner the same way the Go package name is derived from the binary name.
PROTO_OWNER="$(printf '%s' "$OWNER_LOWER" | tr -c 'a-z0-9\n' '_')"
[[ "$PROTO_OWNER" =~ ^[a-z] ]] || PROTO_OWNER="p$PROTO_OWNER"
PROTO_PACKAGE="$PROTO_OWNER.$PACKAGE"

# README badges only make sense for GitHub-hosted projects; elsewhere, the
# OWNER/REPO path is still what the clone instructions should show.
REPO_PATH="$OWNER/$REPO_NAME"

cat <<EOF
Initializing $TARGET
  module:       $MODULE
  project name: $NAME
  binary:       $APP (cmd/$APP)
  go package:   $PACKAGE (internal/$PACKAGE)
  proto:        $PROTO_PACKAGE (proto/$PACKAGE.proto)
  registry:     $REGISTRY
  description:  $DESCRIPTION
EOF
if [[ "$MODULE_HOST" != "github.com" ]]; then
  echo "  note:         $MODULE_HOST is not github.com; review the README badges by hand."
fi

if $CONFIRM && ! $DRY_RUN; then
  if [[ ! -t 0 ]]; then
    echo "No terminal to confirm the settings on; pass --no-confirm to proceed without confirming." >&2
    exit 1
  fi
  read -r -p "Proceed with these settings? [y/N] " answer
  [[ "$answer" =~ ^[Yy] ]] || {
    echo "Aborted; nothing was changed."
    exit 1
  }
fi

# --- renaming files ---------------------------------------------------------

# Renamed before the rewrite below, so the files it edits (and later stages)
# are listed at their final paths.

RENAMED=()

# move SRC DST renames with git when the file is tracked, so history follows.
move() {
  local src="$1" dst="$2"
  [[ "$src" == "$dst" || ! -e "$src" ]] && return
  RENAMED+=("$src -> $dst")
  $DRY_RUN && return
  mkdir -p "$(dirname "$dst")"
  if git ls-files --error-unmatch "$src" >/dev/null 2>&1; then
    git mv "$src" "$dst"
  else
    mv "$src" "$dst"
  fi
}

move "cmd/$TEMPLATE_APP/$TEMPLATE_APP.go" "cmd/$TEMPLATE_APP/$APP.go"
move "cmd/$TEMPLATE_APP" "cmd/$APP"
move "internal/$TEMPLATE_APP/${TEMPLATE_APP}_test.go" "internal/$TEMPLATE_APP/${PACKAGE}_test.go"
move "internal/$TEMPLATE_APP/$TEMPLATE_APP.go" "internal/$TEMPLATE_APP/$PACKAGE.go"
move "internal/$TEMPLATE_APP" "internal/$PACKAGE"
move "proto/$TEMPLATE_APP.proto" "proto/$PACKAGE.proto"

# --- rewriting files --------------------------------------------------------

# Every project file the script may edit: tracked (or, outside git, present)
# text files, minus the template-managed and vendored trees described above.
list_files() {
  if git rev-parse --git-dir >/dev/null 2>&1; then
    git ls-files
  else
    find . -type f -not -path './.git/*' | sed 's|^\./||'
  fi | grep -Ev '^(\.github/|scripts/template-go/|third_party/|build/|Makefile_.*\.mk$)' |
    while IFS= read -r f; do
      [[ -f "$f" ]] && grep -Iq . "$f" && printf '%s\n' "$f"
    done
}

CHANGED=()

# rewrite FILE PERL_PROGRAM applies a perl substitution to FILE in place (or,
# in dry-run mode, only records that it would change). The replacement values
# are passed through the environment so no quoting can leak into the regex.
rewrite() {
  local file="$1" program="$2" tmp
  tmp="$(mktemp)"
  perl -0777 -pe "$program" "$file" >"$tmp"
  if ! cmp -s "$file" "$tmp"; then
    CHANGED+=("$file")
    if ! $DRY_RUN; then
      cat "$tmp" >"$file" # keep the file's mode
    fi
  fi
  rm -f "$tmp"
}

# One export per line, not a single one continued with backslashes: a Windows
# checkout can have CRLF line endings, and a backslash followed by CR does not
# continue the line.
export TEMPLATE_MODULE TEMPLATE_REPO_PATH TEMPLATE_APP TEMPLATE_APP_URL
export TEMPLATE_PROTO_PACKAGE TEMPLATE_REGISTRY TEMPLATE_DESCRIPTION
export TEMPLATE_CODECOV_TOKEN
export MODULE APP PACKAGE DESCRIPTION NAME REGISTRY PROTO_PACKAGE REPO_PATH

# Order matters: the most specific strings go first, so e.g. the internal
# import path is rewritten before the bare module path would claim it.
COMMON_SUBST='
  s{\Qhttps://$ENV{TEMPLATE_APP_URL}\E\b}{https://$ENV{MODULE}}g;
  s{\Q$ENV{TEMPLATE_APP_URL}\E/}{$ENV{MODULE}/}g;
  s{\Q$ENV{TEMPLATE_MODULE}\E/internal/\Q$ENV{TEMPLATE_APP}\E\b}{$ENV{MODULE}/internal/$ENV{PACKAGE}}g;
  s{\Q$ENV{TEMPLATE_MODULE}\E\b}{$ENV{MODULE}}g;
  s{\Q$ENV{TEMPLATE_REPO_PATH}\E\b}{$ENV{REPO_PATH}}g;
  s{\Q$ENV{TEMPLATE_PROTO_PACKAGE}\E\b}{$ENV{PROTO_PACKAGE}}g;
  s{\Q$ENV{TEMPLATE_DESCRIPTION}\E}{$ENV{DESCRIPTION}}g;
'

# Go code refers to the package name; the doc comment of the command names the
# binary.
GO_SUBST='
  s{^// Command \Q$ENV{TEMPLATE_APP}\E\b}{// Command $ENV{APP}}mg;
  s{\b\Q$ENV{TEMPLATE_APP}\E\b}{$ENV{PACKAGE}}g;
'

# Proto declarations sit in the PACKAGE namespace; everything else (Makefile
# ALL_APPS, Dockerfile entrypoints, docs) names the binary.
PROTO_SUBST='s{\b\Q$ENV{TEMPLATE_APP}\E\b}{$ENV{PACKAGE}}g;'
OTHER_SUBST='s{\b\Q$ENV{TEMPLATE_APP}\E\b}{$ENV{APP}}g;'

MAKEFILE_SUBST='
  s{^(REGISTRY\s*=\s*)\Q$ENV{TEMPLATE_REGISTRY}\E\s*$}{$1$ENV{REGISTRY}}mg;
'

# The README title and intro paragraph describe the template; swap them for the
# project name and description. The codecov token belongs to template-go, and
# the section on running this script has served its purpose.
README_SUBST='
  s{\A# template-go\n}{# $ENV{NAME}\n};
  s{^### Starting a new project\n.*?(?=^## )}{}ms;
  s{^A starter template for Go projects.*?\n\n}{$ENV{DESCRIPTION}\n\n}ms;
  s{\Q$ENV{TEMPLATE_CODECOV_TOKEN}\E}{}g;
'

while IFS= read -r file; do
  case "$file" in
    *.go) program="$COMMON_SUBST$GO_SUBST" ;;
    *.proto) program="$COMMON_SUBST$PROTO_SUBST" ;;
    Makefile) program="$COMMON_SUBST$MAKEFILE_SUBST$OTHER_SUBST" ;;
    README.md) program="$COMMON_SUBST$README_SUBST$OTHER_SUBST" ;;
    *) program="$COMMON_SUBST$OTHER_SUBST" ;;
  esac
  rewrite "$file" "$program"
done < <(list_files)

# --- report -----------------------------------------------------------------

if $DRY_RUN; then
  echo "Dry run - would edit ${#CHANGED[@]} file(s) and make ${#RENAMED[@]} rename(s):"
else
  echo "Edited ${#CHANGED[@]} file(s) and made ${#RENAMED[@]} rename(s):"
fi
for f in "${CHANGED[@]}"; do echo "  edit   $f"; done
for r in "${RENAMED[@]}"; do echo "  rename $r"; done

LEFTOVER="$(list_files | xargs grep -nE "\\b$TEMPLATE_APP\\b|$TEMPLATE_REPO_PATH" 2>/dev/null || true)"
if ! $DRY_RUN && [[ -n "$LEFTOVER" ]]; then
  echo
  echo "Template references still left (review by hand):"
  printf '%s\n' "$LEFTOVER" | sed 's/^/  /'
fi

# --- tidying up -------------------------------------------------------------

# Run after the report, so the edit/rename list isn't buried under make's
# output. A lint failure doesn't undo anything: the changes stay in place and
# the script exits non-zero so the issues get looked at before committing.
LINT_OK=true
if ! $DRY_RUN && $DO_TIDY; then
  echo
  echo "== tidying up =="
  if [[ -f go.mod ]]; then
    go mod tidy
  fi
  if ! make lint; then
    LINT_OK=false
  fi
fi

# --- staging ----------------------------------------------------------------

# git mv already staged the renames; stage the edits too, so the whole
# initialization is ready to commit. From a clean start every change is the
# script's (including go mod tidy and make lint's fixes); otherwise only the
# files it edited are staged, leaving the pre-existing changes alone.
if ! $DRY_RUN && $IN_GIT; then
  if $STARTED_CLEAN; then
    git add -A
  elif [[ ${#CHANGED[@]} -gt 0 ]]; then
    git add -- "${CHANGED[@]}"
  fi
fi

if ! $DRY_RUN; then
  echo
  echo "Next steps:"
  if $IN_GIT; then
    echo "  * review the staged changes with 'git status' and 'git diff --cached'"
    $STARTED_CLEAN || echo "  * stage any formatting fixes to files that had uncommitted changes before"
  else
    echo "  * review the changes"
  fi
  if ! $DO_TIDY; then
    echo "  * run 'make lint'"
  elif ! $LINT_OK; then
    echo "  * fix the issues 'make lint' reported above"
  fi
  cat <<EOF
  * run 'make test'
  * run scripts/template-go/configure-github-repo.sh to apply the repo settings
  * commit
EOF
fi

if ! $LINT_OK; then
  echo
  echo "make lint reported issues; the changes were made but need fixing." >&2
  exit 1
fi
