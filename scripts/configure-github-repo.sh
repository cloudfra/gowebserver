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

# Applies the GitHub repository settings this template expects, so they
# can be reproduced on any repo created from it (or restored here) with
# one command instead of clicking through Settings by hand.
#
# Usage: scripts/configure-github-repo.sh [-r|--repo OWNER/REPO] [-n|--dry-run]
# Defaults to the `origin` remote of the current git repo; pass --repo to
# target a different one. Requires `gh` to be authenticated with admin
# rights on the target repo. Pass --dry-run to print what would run
# without changing anything.
#
# Safe to re-run: every step is idempotent. Settings that require a plan
# this repo doesn't have (e.g. branch protection or secret scanning on a
# private repo without GitHub Advanced Security) are best-effort - they
# print a warning and the script keeps going instead of failing outright,
# so the same script still works once the repo is public or the org
# upgrades.

set -euo pipefail

usage() {
  echo "Usage: $0 [-r|--repo OWNER/REPO] [-n|--dry-run]" >&2
}

# infer_repo reads OWNER/REPO from the `origin` remote's URL, so nobody
# has to look it up and pass it by hand. Handles the SSH, ssh://, and
# HTTPS forms `git remote -v` prints for a github.com remote.
infer_repo() {
  local url
  if ! url="$(git remote get-url origin 2>/dev/null)"; then
    echo "Not in a git repo with an 'origin' remote - pass --repo OWNER/REPO instead." >&2
    exit 1
  fi
  case "$url" in
    git@github.com:*) url="${url#git@github.com:}" ;;
    ssh://git@github.com/*) url="${url#ssh://git@github.com/}" ;;
    https://github.com/*) url="${url#https://github.com/}" ;;
    *)
      echo "Could not parse OWNER/REPO from origin remote '$url' - pass --repo OWNER/REPO instead." >&2
      exit 1
      ;;
  esac
  echo "${url%.git}"
}

REPO=""
DRY_RUN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    -r | --repo)
      REPO="$2"
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

if [[ -z "$REPO" ]]; then
  REPO="$(infer_repo)"
fi

echo "Configuring $REPO"
if $DRY_RUN; then
  echo "(dry run - no changes will be made)"
fi

# run prints the command it's about to invoke (to stderr, so it survives
# the >/dev/null callers use to hide noisy JSON responses), then either
# runs it for real or, under --dry-run, skips it and reports success so
# callers don't mistake "we didn't try" for "the plan doesn't support it".
run() {
  echo "+ $*" >&2
  if $DRY_RUN; then
    return 0
  fi
  "$@"
}

# best_effort runs a command that may legitimately fail on some plans
# (branch protection, secret scanning, GHAS-only features on private
# repos) without aborting the rest of the script.
best_effort() {
  local description="$1"
  shift
  if ! "$@" >/dev/null; then
    echo "  (skipped) $description - not available for this repo/plan"
  fi
}

## Merge / branch hygiene ##

# Delete head branches once their PR merges, so stale branches don't pile up.
run gh repo edit "$REPO" --delete-branch-on-merge

# Let a PR branch be updated from its base with a button click instead of a
# manual merge commit, so CI keeps testing against current main.
run gh repo edit "$REPO" --allow-update-branch

# Merge a PR itself the moment its required checks pass, instead of relying
# on someone noticing and clicking merge. GitHub silently no-ops this one
# instead of erroring when the plan doesn't support it (private repos need
# Pro/Team/Enterprise), so check the result rather than trust the exit code.
run gh repo edit "$REPO" --enable-auto-merge >/dev/null
if $DRY_RUN; then
  echo "  (dry-run) skipping live check of whether auto-merge actually took effect"
elif [[ "$(gh api "repos/$REPO" --jq .allow_auto_merge)" != "true" ]]; then
  echo "  (skipped) auto-merge - not available for this repo/plan"
fi

## Dependency / vulnerability management ##

# Dependabot alerts: flag dependencies with known vulnerabilities.
run gh api --method PUT "repos/$REPO/vulnerability-alerts"

# Dependabot security updates: auto-PR fixes for the alerts above.
run gh api --method PUT "repos/$REPO/automated-security-fixes"

# Private vulnerability reporting: lets someone report a vulnerability
# privately instead of filing a public issue. Unlike the GHAS-gated
# features below, this isn't a licensing/plan check GitHub can pass or
# fail with an explanation - it 404s unconditionally on private
# repositories (confirmed against GitHub's docs), because the feature
# is restricted to public repos, full stop. See SECURITY.md for the
# fallback reporting path this repo uses until it goes public.
best_effort "private vulnerability reporting" \
  run gh api --method PUT "repos/$REPO/private-vulnerability-reporting"

## Secret scanning ##
# Free for public repos; on private repos it needs GitHub Advanced
# Security, which this doesn't enable automatically since it's a billed,
# org-level decision.

best_effort "secret scanning" \
  run gh api --method PATCH "repos/$REPO" \
  -f "security_and_analysis[secret_scanning][status]=enabled"

best_effort "secret scanning push protection" \
  run gh api --method PATCH "repos/$REPO" \
  -f "security_and_analysis[secret_scanning_push_protection][status]=enabled"

## Branch protection ##
# Classic branch protection needs GitHub Team/Enterprise for private
# repos (GitHub Free rejects it with a 403). Encodes the desired state
# so it takes effect the moment the repo goes public or the org upgrades.
# Required reviews are deliberately left out: this template is currently
# maintained solo, and requiring a second approver would just block
# merges rather than improve quality.
#
# Keep this context list in sync with the jobs actually defined in
# deploy.yaml/reviewdog.yaml - it doesn't derive from them automatically,
# so a renamed/added/removed job silently drifts out of sync otherwise
# (see #64, which is how runner / yamllint ended up missing here).
# runner / dependency-review is deliberately left out: it needs GitHub
# Advanced Security and always fails without it (see #14/#49), so
# requiring it would block every merge once this actually takes effect.
best_effort "branch protection on main" \
  run gh api --method PUT "repos/$REPO/branches/main/protection" --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "Build Linux",
      "Build Windows",
      "runner / misspell",
      "runner / hadolint",
      "runner / codespell",
      "runner / yamllint"
    ]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null
}
EOF

echo "Done."
