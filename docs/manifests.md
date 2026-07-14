# What blastmap reads, per ecosystem

blastmap never executes a package manager or build tool. It reads the
manifests your repository already has, builds the internal dependency
graph from them, and stops there. This page is the exact contract.

## Discovery

`blastmap … [path]` treats `path` (default `.`) as the workspace root and
probes for, in order:

| Ecosystem | Root manifest probed | Members come from |
|---|---|---|
| `cargo` | `Cargo.toml` containing a `[workspace]` table | `members` globs minus `exclude` (plus the root `[package]` itself) |
| `go` | `go.work` | `use` directives (single-line and block form) |
| `npm` | `pnpm-workspace.yaml`, else `package.json` with `workspaces` | the pattern list (`!pattern` negations supported) |

With `--ecosystem auto` (the default) every ecosystem found is loaded
side by side; names never collide across ecosystems because graph keys
are `<ecosystem>:<name>`. Member globs support `*` (one segment), `?`
(one character), and `**` (any depth); expansion skips `.git`,
`node_modules`, `target`, `vendor`, and dot-directories.

## npm / yarn / pnpm

- Member = a matched directory containing `package.json`. A missing
  `name` falls back to the directory path.
- Internal edge = any entry in `dependencies`, `peerDependencies`, or
  `optionalDependencies` whose **name** is another member — the version
  spec is irrelevant, so `workspace:*`, `^1.2.3`, and `file:` all count.
- `devDependencies` entries become **dev edges**, dropped by `--no-dev`.
- Global files (affect everything when changed): root `package.json`,
  `pnpm-workspace.yaml`, `package-lock.json`, `pnpm-lock.yaml`,
  `yarn.lock` — whichever exist.

## Go (go.work workspaces)

- Member = each `use` directory; its `go.mod` must exist and declare a
  `module` path.
- Internal edge = a `require` whose module path belongs to another
  member, or a `replace` whose target is a relative path (`./…`, `../…`)
  resolving to a member directory. `// indirect` makes no difference.
- Go has no dev-dependency notion, so `--no-dev` is a no-op here.
- Global files: `go.work`, `go.work.sum`.
- Single-module repositories (no `go.work`) are out of scope for 0.1.0 —
  package-level analysis inside one module would require `go list`.

## Cargo

- Member = `[workspace] members` globs minus `exclude`, each containing
  `Cargo.toml` with a `[package] name`; a root `[package]` next to
  `[workspace]` is a member at `.`.
- Internal edge, in resolution order:
  1. `path = "…"` resolving to a member directory (the edge lands on
     that member),
  2. otherwise the dependency **name** — after applying a
     `package = "real-name"` rename — matching a member (this covers
     `workspace = true` entries).
- `[dev-dependencies]` become dev edges; `[build-dependencies]` are
  runtime edges (they break the build just the same). Target-specific
  tables (`[target.'cfg(…)'.dependencies]`) are read too.
- Global files: root `Cargo.toml`, `Cargo.lock`.
- The TOML reader is a purpose-built subset (tables, strings, booleans,
  string arrays, inline tables). Numbers and dates are tolerated and
  skipped; feature-activation analysis is not attempted.

## File-to-package mapping

Changed paths come from `git diff --name-only --no-renames -z` (rename
detection off, so a moved file counts in both its old and new package)
or from stdin with `--stdin-files`. Each file maps to the member with
the **deepest** directory prefix; files owned by nobody are *unclaimed*
and handled per `--unclaimed ignore|affect-all|error`.
