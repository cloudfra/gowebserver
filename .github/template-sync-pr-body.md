# Template-managed files re-synced from upstream

Produced by `scripts/template-go/sync-template.sh`, run on the Friday-night
schedule in `.github/workflows/template-sync.yaml`. It replaces this project's
template-managed files with the current versions from template-go's
`main` branch:

- every `Makefile_*.mk` (the root `Makefile` and
  `Makefile_testassets.mk` are preserved),
- every root dotfile config (`.gitignore`, `.golangci.yml`,
  `.yamllint.yml`, ...),
- the whole `.github/` directory (except `.github/workflows/deploy-*.yaml`,
  which a project owns and the sync preserves),
- the whole `scripts/template-go/` directory (the standard scripts).

Files upstream no longer ships are deleted and files it now ships are
added, so the diff is the whole gap since the last sync rather than a
merge. The script is safe to re-run: it converges on the upstream tree.

## This is a hard replace - read the diff before merging

Any local edit to a managed file is discarded by the sync, and this PR is
where that becomes visible. Check the diff for:

- Project-specific edits under `.github/` - workflow triggers, build
  matrix. Releases are built by `create-release.yaml` on a `v*` tag push;
  its release files are globs that match every app in `ALL_APPS`, so check
  they match the binaries the project actually publishes. Workflows a
  project needs to keep should
  be named `deploy-*.yaml`; those are never touched.
- Customized root dotfiles - `.gitignore` entries for local paths, lint
  config tweaks.

The other files directly under `scripts/` (such as the per-project CI
hooks `scripts/prepare-ci.sh` and `scripts/Initialize-CI.ps1`) are the
exception and are never overwritten: they are copied in only when
missing, so a project's customization of them survives every sync.

Nothing here merges automatically - merging is a human decision.
