#!/usr/bin/env bash
# Fabricates a small npm-workspaces monorepo with two commits, so every
# blastmap subcommand has something real to chew on. Usage:
#
#   bash examples/make-demo-repo.sh /tmp/blastmap-demo
#   blastmap affected /tmp/blastmap-demo
#
# Author dates and git config are pinned, so the repository is identical
# on every machine.
set -euo pipefail

DEST="${1:?usage: make-demo-repo.sh <target-dir>}"
rm -rf "$DEST"
mkdir -p "$DEST"

export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME="Dev"
export GIT_AUTHOR_EMAIL="dev@example.test"
export GIT_COMMITTER_NAME="Dev"
export GIT_COMMITTER_EMAIL="dev@example.test"

commit_on() {
  local seq="$1" date
  shift
  date="$(printf '2026-03-%02dT10:00:00+00:00' "$seq")"
  git -C "$DEST" add -A
  GIT_AUTHOR_DATE="$date" GIT_COMMITTER_DATE="$date" \
    git -C "$DEST" commit -q --no-gpg-sign -m "$*"
}

git init -q -b main "$DEST"
mkdir -p "$DEST/packages/utils/src" "$DEST/packages/ui/src" \
  "$DEST/packages/tsconfig" "$DEST/apps/web/pages" "$DEST/apps/api/src"

cat > "$DEST/package.json" <<'EOF'
{"name":"demo","private":true,"workspaces":["packages/*","apps/*"]}
EOF
cat > "$DEST/packages/utils/package.json" <<'EOF'
{"name":"@demo/utils","version":"1.0.0"}
EOF
cat > "$DEST/packages/ui/package.json" <<'EOF'
{"name":"@demo/ui","version":"1.0.0","dependencies":{"@demo/utils":"workspace:*"}}
EOF
cat > "$DEST/packages/tsconfig/package.json" <<'EOF'
{"name":"@demo/tsconfig","version":"1.0.0"}
EOF
cat > "$DEST/apps/web/package.json" <<'EOF'
{"name":"@demo/web","version":"1.0.0","dependencies":{"@demo/ui":"workspace:*"},"devDependencies":{"@demo/tsconfig":"workspace:*"}}
EOF
cat > "$DEST/apps/api/package.json" <<'EOF'
{"name":"@demo/api","version":"1.0.0","dependencies":{"@demo/utils":"workspace:*"}}
EOF
echo 'exports.id = (x) => x' > "$DEST/packages/utils/src/index.js"
echo 'exports.Button = () => null' > "$DEST/packages/ui/src/button.js"
echo 'export default () => null' > "$DEST/apps/web/pages/index.js"
echo 'exports.handler = () => 204' > "$DEST/apps/api/src/handler.js"
commit_on 1 "baseline: five packages, wired up"

echo 'exports.fmt = (x) => `${x}`' > "$DEST/packages/utils/src/format.js"
echo '# scratch' > "$DEST/NOTES.md"
commit_on 2 "feat(utils): add fmt helper"

echo "demo repository ready at $DEST"
echo "try:  blastmap affected $DEST"
