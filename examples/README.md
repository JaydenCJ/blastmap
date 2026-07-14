# blastmap examples

Two runnable scripts, both offline and self-contained.

## make-demo-repo.sh

Fabricates a small npm-workspaces monorepo with five wired-up packages
(`web -> ui -> utils`, `api -> utils`, a dev-only `tsconfig`) and two
commits: a baseline, then a change to `utils` plus a stray root file.

```bash
bash examples/make-demo-repo.sh /tmp/blastmap-demo
blastmap affected /tmp/blastmap-demo
blastmap graph --format dot /tmp/blastmap-demo
```

Author dates and git configuration are pinned, so the fabricated history
— and therefore blastmap's output — is identical on every machine.

## ci-gate.sh

Shows the CI pattern blastmap is built for: diff the branch against its
base with merge-base semantics (`origin/main...HEAD`), treat unclaimed
files as affecting everything (the safe default for CI), and run one
command per affected package — or skip the job entirely when the set is
empty.

```bash
bash examples/make-demo-repo.sh /tmp/blastmap-demo
cd /tmp/blastmap-demo && BASE=HEAD~1 bash /path/to/blastmap/examples/ci-gate.sh
```

The script deliberately avoids jq: the JSON schema keeps `name`/`dir`
pairs on stable single lines precisely so shell-only pipelines can
consume them.
