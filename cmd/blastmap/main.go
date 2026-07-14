// Command blastmap computes which workspace packages a git range affects
// and prints the targets for CI, from the manifests you already have.
package main

import (
	"os"

	"github.com/JaydenCJ/blastmap/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
