# Contributing

Thanks for contributing! This repo is meant to be forked and reused as a
template, so keeping the `make`-based workflow consistent matters — both
for this repo and every project that starts from it.

## Before you push

Run the full presubmit suite, which mirrors what CI runs on every push and
pull request:

```bash
make presubmit
```

This downloads the pinned toolchain (`tools`), runs the full lint suite
(`lint`), builds every supported platform (`all`), and re-runs the tests
under the race detector to catch flakes (`test-deflake`).

## Running checks individually

While iterating, it's usually faster to run just the piece you're
changing:

```bash
make lint   # gofmt/go vet, gofumpt, golangci-lint, revive, hadolint,
            # actionlint, govulncheck, tflint/terraform fmt
make test   # unit test suite
make run    # build and run the example binary
```

See the "Common make targets" table in the [README](README.md#common-make-targets)
for the rest (benchmarks, Terraform tests, Docker images, etc.).

## Adding a new `cmd/` binary

Per the README's [Project layout](README.md#project-layout), create a new
directory under `cmd/` with a `main` package, e.g. `cmd/myapp/myapp.go`.
The build system picks it up automatically — no Makefile changes needed
to build it with `make`. To also cross-compile, package, or release it,
add its name to `ALL_APPS` in the `Makefile`.

## Coverage

Codecov enforces coverage on pull requests (see `codecov.yml`): project
coverage can drop by at most 1%, and patched code should be covered at
80% or more.
