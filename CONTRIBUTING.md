# Contributing to blastmap

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22 and git ≥2.28 (for `init -b`); nothing else.

```bash
git clone https://github.com/JaydenCJ/blastmap && cd blastmap
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, fabricates a deterministic npm
monorepo and a Go workspace in a temp dir, and asserts on real CLI output
across every subcommand; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (89 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (parsers and the impact engine never shell out — only
   `gitio` does).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR.
- No network calls, ever — blastmap's only external interface is the
  local `git` binary, and even that is optional (`--stdin-files`).
  No telemetry.
- Manifest knowledge is data plus a parser: a new ecosystem is one
  loader file in `internal/workspace/` returning `[]*Package`, a row in
  `docs/manifests.md`, and tests reproducing real manifest shapes.
- Determinism first: identical input must produce byte-identical
  reports, including all orderings — sort before you print.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `blastmap version`, the full command you ran, the
report output, and — for wrong blast radii — the relevant manifest
snippets (root workspace manifest plus the member manifests on the
expected chain), since that is exactly what discovery sees. For range
issues, add `git diff --name-only <range>` output.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
