#!/usr/bin/env bash
# Example CI gate built on blastmap: compute the affected set for the
# current branch versus its base, then run one command per target — and
# skip the whole job when nothing is affected. Adapt the `run_target`
# body to your build system (npm scripts, go test, cargo test, …).
#
# Usage: BASE=origin/main bash examples/ci-gate.sh [workspace-path]
set -euo pipefail

BASE="${BASE:-origin/main}"
WS="${1:-.}"

run_target() {
  # $1 = package name, $2 = package directory (workspace-relative).
  echo ">> would test $1 (in $2)"
}

# --paths prints directories; pair names+dirs from the JSON instead when
# you need both. `--unclaimed affect-all` is the safe default for CI:
# a file nobody owns reruns everything rather than silently skipping.
AFFECTED_JSON="$(blastmap affected --range "$BASE...HEAD" --unclaimed affect-all --format json "$WS")"

COUNT="$(printf '%s' "$AFFECTED_JSON" | grep -c '"status"' || true)"
if [ "$COUNT" -eq 0 ]; then
  echo "nothing affected by $BASE...HEAD — skipping tests"
  exit 0
fi

PLURAL="packages"
[ "$COUNT" -eq 1 ] && PLURAL="package"
echo "$COUNT $PLURAL affected by $BASE...HEAD"
# Iterate name/dir pairs without jq: names and dirs line up 1:1 in the
# schema (affected[] is a flat array).
paste \
  <(printf '%s\n' "$AFFECTED_JSON" | sed -n 's/^ *"name": "\(.*\)",$/\1/p') \
  <(printf '%s\n' "$AFFECTED_JSON" | sed -n 's/^ *"dir": "\(.*\)",$/\1/p') |
while IFS="$(printf '\t')" read -r name dir; do
  run_target "$name" "$dir"
done
