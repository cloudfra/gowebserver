# Agent guide

Rules for an AI agent changing a repo built from this template. They are here
to keep a change small enough that a reviewer can check the whole diff in one
pass.

## Before writing

- Read the file being changed and the ones beside it. The repo's existing
  style wins over any default.
- Find where the change belongs before typing it. Build logic goes in the
  `Makefile_*.mk` that owns the subject; workflow payloads go in a
  committed file under `scripts/`, never inline in YAML.
- Make the smallest change that satisfies the request. No abstraction for
  a single caller, no validation for input that cannot occur, no feature
  nobody asked for. An API that only a test needs stays unexported in the
  `_test.go` file.

## Small changes

Break a large change into small ones. Each one does a single thing — a fix,
a feature, a rename — because a small diff gets reviewed as a whole while a
large one gets skimmed no matter how careful the reviewer intends to be.

A mechanical change is allowed to be large: a rename across the tree, a flag
renamed in every workflow, code moved between files. Then it is only that
refactor. No behavior change rides along, so the review question is "does
every call site still do the same thing" and nothing else.

Stack work that is genuinely big rather than bundling it into one pull
request. Branch each piece off the one before it and open a pull request per
piece, so each step stays reviewable on its own and they merge in order. Keep
the stack shallow, and keep each link rebased — the bottom on latest `main`,
the rest on the pull request below them.

## Comments are rare

An inline comment is justified only when a reader of the code alone would
still be missing something: a constraint the types do not express, an
ordering that has to be kept, a workaround for a specific upstream bug, a
choice whose rejected alternative is not visible.

- A comment that restates the next line gets deleted, not reworded.
- Do not narrate the change itself ("added for #123", "moved here"); the
  commit message and the PR carry that history.
- A better name usually removes the need for the comment.

## Doc comments

Follow the language's own convention, and use one or two sentences about
what the thing is for and why a caller would want it. Not how it is
implemented.

```go
// Run executes the exampleapp application logic.
func Run(args Args) error

// Version of the package.
func Version() string
```

Both say what the function is good for. Neither mentions the flag parsing,
the logging or the `ldflags` that produce the value, because reading the
body is cheaper and never goes stale.

In Go the comment starts with the name of the thing it documents. The
pinned `revive` that `make lint-go` runs rejects a missing or mis-shaped
comment, so this is a build gate and not a preference.

The same rule covers types, struct fields, commands, and the helper, fake
and benchmark functions added in `_test.go` files.

## Package comments

This is the one comment allowed to be long: it can say what the package is
for, the invariants it holds, and what it deliberately does not do.

Keep it inside a one to two minute read, which is a few short paragraphs.
Anything longer belongs in a document the package comment points to, since
a package comment is read alongside every change in the package.

Go wants one per package, starting with `Package <name>`, and `Command
<name>` for a `main` package.

## Small files

Prefer many small files to a few large ones. A file holding one concern
conflicts less often, and both a reviewer and an agent can hold it in
context at once.

Java's one-class-per-file rule is the right instinct; the fallout here is
the same shape.

- Go: one type, or one family of closely related functions, per file, with
  its `_test.go` beside it.
- Make: the build is already split this way — `Makefile_core.mk`,
  `Makefile_build.mk`, `Makefile_toolchain.mk`, `Makefile_proto.mk`,
  `Makefile_testassets.mk`. A new subject gets its own `.mk` rather than a
  longer `Makefile_build.mk`.
- Shell: one script per job under `scripts/`, as it already is.

Two limits. Do not fragment a file that reads top to bottom as one story,
and do not split a long file just because it is long: split when it holds
concerns that change for different reasons, and only when the current
change touches it anyway.

## New files

Every source and config file carries the same Apache header as its
neighbors; copy it from a file in the same directory. Root markdown docs
do not take a header.

## Verify with the repo's own gates

Run them before pushing, not after:

```bash
make presubmit   # tools, lint, every platform, tests under -race
```

Narrower while iterating: `make lint-go`, `make lint-shell`,
`make lint-markdown`, `make test-go`. CI adds reviewdog's `misspell`
(prose is American English), `codespell`, and `yamllint` on `.github/`
(80-column lines).

Report only what was actually run, with its output. A check that was not
run is reported as not run.

## This file

`AGENTS.md` is copied into a new project and never overwritten by
`scripts/template-go/sync-template.sh`, so project-specific rules go here.
Tool-specific instruction files (`CLAUDE.md`, `.cursor/rules`,
`.github/copilot-instructions.md`) point at this file instead of
repeating it.
