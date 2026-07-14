// blastmap — computes which workspace packages a git range affects and
// prints the targets for CI, from the manifests you already have.
//
// version:    0.1.0
// author:     JaydenCJ
// license:    MIT
// repository: https://github.com/JaydenCJ/blastmap
// keywords:   monorepo, ci, affected, workspaces, dependency-graph, git, build-targets
//
// Zero runtime dependencies: the require list below is intentionally empty
// and must stay that way (see CONTRIBUTING.md, "Ground rules").
module github.com/JaydenCJ/blastmap

go 1.22
