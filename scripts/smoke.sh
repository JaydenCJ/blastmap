#!/usr/bin/env bash
# End-to-end smoke test for blastmap: builds the binary, fabricates a
# deterministic npm monorepo and a Go workspace in a temp dir, and asserts
# on real CLI output. No network, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/blastmap"
REPO="$WORKDIR/repo"

# Isolate git completely from the host user's configuration.
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME="Dev"
export GIT_AUTHOR_EMAIL="dev@example.test"
export GIT_COMMITTER_NAME="Dev"
export GIT_COMMITTER_EMAIL="dev@example.test"

commit_on() {
  # commit_on <seq> <message>: stage everything, commit with pinned date.
  local seq="$1" date
  shift
  date="$(printf '2026-03-%02dT10:00:00+00:00' "$seq")"
  git -C "$REPO" add -A
  GIT_AUTHOR_DATE="$date" GIT_COMMITTER_DATE="$date" \
    git -C "$REPO" commit -q --no-gpg-sign -m "$*"
}

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/blastmap) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "blastmap 0.1.0" || fail "--version mismatch"

echo "3. fabricate an npm monorepo (web -> ui -> utils, api -> utils)"
git init -q -b main "$REPO"
mkdir -p "$REPO/packages/utils/src" "$REPO/packages/ui" "$REPO/apps/web" "$REPO/apps/api"
cat > "$REPO/package.json" <<'EOF'
{"name":"demo","private":true,"workspaces":["packages/*","apps/*"]}
EOF
echo '{"name":"@demo/utils","version":"1.0.0"}' > "$REPO/packages/utils/package.json"
echo '{"name":"@demo/ui","version":"1.0.0","dependencies":{"@demo/utils":"workspace:*"}}' > "$REPO/packages/ui/package.json"
echo '{"name":"@demo/web","version":"1.0.0","dependencies":{"@demo/ui":"workspace:*"}}' > "$REPO/apps/web/package.json"
echo '{"name":"@demo/api","version":"1.0.0","dependencies":{"@demo/utils":"workspace:*"}}' > "$REPO/apps/api/package.json"
echo 'exports.id = (x) => x' > "$REPO/packages/utils/src/index.js"
commit_on 1 "baseline"
echo 'exports.fmt = (x) => `${x}`' > "$REPO/packages/utils/src/format.js"
commit_on 2 "change utils"

echo "4. text report shows the blast radius with evidence chains"
OUT="$("$BIN" affected "$REPO")"
echo "$OUT" | grep -q "blastmap affected" || fail "missing report header"
echo "$OUT" | grep -q "@demo/utils" || fail "changed package missing"
echo "$OUT" | grep -q "via @demo/web -> @demo/ui -> @demo/utils" || fail "dependency chain missing"
echo "$OUT" | grep -q "4 of 4 packages affected" || fail "affected count wrong"

echo "5. lines format is xargs-ready and sorted"
LINES="$("$BIN" affected --format lines "$REPO")"
[ "$LINES" = "@demo/api
@demo/ui
@demo/utils
@demo/web" ] || fail "lines output wrong: $LINES"

echo "6. JSON is machine-readable and versioned"
JSON="$("$BIN" affected --format json "$REPO")"
echo "$JSON" | grep -q '"tool": "blastmap"' || fail "json envelope missing"
echo "$JSON" | grep -q '"schema_version": 1' || fail "schema version missing"
echo "$JSON" | grep -q '"status": "dependent"' || fail "dependent status missing"

echo "7. --direct-only stops propagation"
"$BIN" affected --direct-only --format lines "$REPO" | grep -qx "@demo/utils" \
  || fail "direct-only should print only utils"

echo "8. a lockfile change blasts every package"
echo '{"lockfileVersion":3}' > "$REPO/package-lock.json"
commit_on 3 "lockfile update"
"$BIN" affected "$REPO" | grep -q "global blast" || fail "lockfile should trigger global blast"

echo "9. --unclaimed error gates unowned files with exit 1"
echo "scratch" > "$REPO/NOTES.md"
commit_on 4 "stray root file"
if "$BIN" affected --unclaimed error "$REPO" >/dev/null 2>&1; then
  fail "unclaimed file should exit 1 under --unclaimed error"
fi

echo "10. a Go workspace works with the same commands"
GOREPO="$WORKDIR/gorepo"
REPO="$GOREPO"
git init -q -b main "$GOREPO"
mkdir -p "$GOREPO/svc/api" "$GOREPO/libs/core"
printf 'go 1.22\n\nuse (\n\t./svc/api\n\t./libs/core\n)\n' > "$GOREPO/go.work"
printf 'module example.test/api\n\ngo 1.22\n\nrequire example.test/core v0.0.0\n' > "$GOREPO/svc/api/go.mod"
printf 'module example.test/core\n\ngo 1.22\n' > "$GOREPO/libs/core/go.mod"
commit_on 1 "baseline"
printf 'package core\n' > "$GOREPO/libs/core/core.go"
commit_on 2 "core change"
GOLINES="$("$BIN" affected --format lines "$GOREPO")"
[ "$GOLINES" = "example.test/api
example.test/core" ] || fail "go workspace affected wrong: $GOLINES"

echo "11. graph renders Graphviz dot"
"$BIN" graph --format dot "$GOREPO" | grep -q '"go:example.test/api" -> "go:example.test/core";' \
  || fail "dot edge missing"

echo "12. usage errors exit 2"
set +e
"$BIN" affected --format yaml "$GOREPO" >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --format should exit 2"
set -e

echo "SMOKE OK"
