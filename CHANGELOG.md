# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-12

### Added

- Workspace discovery from existing manifests, no build-system adoption:
  npm/yarn/pnpm (`package.json` workspaces in array and object form,
  `pnpm-workspace.yaml` with `!negations`), Go multi-module workspaces
  (`go.work` `use` directives, `go.mod` `require` and relative `replace`
  edges), and Cargo workspaces (`[workspace]` member globs with
  `exclude`, path deps, `workspace = true`, `package = "…"` renames,
  dev/build/target-specific dependency tables) — including
  mixed-ecosystem repositories loaded side by side.
- A purpose-built minimal TOML reader (`internal/tomlmin`) covering the
  Cargo-manifest subset, keeping the binary dependency-free.
- Impact engine: deepest-directory file ownership, BFS over reverse
  dependency edges with shortest evidence chains, dev-edge separation
  (`--no-dev`), `--direct-only`, and `--with-deps` build-closure mode.
- Global-file rules: root lockfiles and workspace manifests affect every
  package by default, extendable with repeatable `--global` globs and
  removable with `--no-default-globals`.
- Unclaimed-file policy `--unclaimed ignore|affect-all|error`, the last
  gating CI with exit code 1.
- Change sources: git ranges (`A..B` and merge-base `A...B`, rename
  detection disabled so moves count on both sides), `--uncommitted`
  working-tree/staged/untracked changes, and git-free `--stdin-files`.
- `affected` (default), `list`, and `graph` subcommands with text,
  sorted `lines` (names or `--paths` dirs), stable JSON
  (`schema_version: 1`), and Graphviz dot output.
- Runnable examples (`examples/make-demo-repo.sh`, `examples/ci-gate.sh`)
  and a per-ecosystem manifest contract (`docs/manifests.md`).
- 89 deterministic offline tests (unit + in-process CLI integration
  against fabricated git repositories) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/blastmap/releases/tag/v0.1.0
