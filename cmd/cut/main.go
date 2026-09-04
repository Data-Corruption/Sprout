// --- FILE template ---

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"sprout/internal/cut"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("cut", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	finalize := fs.Bool("finalize", false, "apply the displayed final-tree plan")
	modulePath := fs.String("module", "", "rename the Go module in the final tree")
	root := fs.String("root", "", "repository root (defaults to parent of scripts/ when invoked via scripts/cut)")
	goimports := fs.String("goimports", "", "goimports binary used to prune imports on --finalize (defaults to PATH)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: cut [flags] [feature...]

Plan the final consumer tree: remove optional Sprout features, remove
template-only tooling, prune directories those removals leave empty, strip
surviving ownership markers, and optionally rename the Go module. Arguments are
a union of features to cut; cutting a parent removes every dotted descendant.
With no feature arguments, every application feature is retained.

The default is a read-only preview. Pass --finalize to apply the same plan.

Valid features:
`)
		for _, name := range cut.Features() {
			fmt.Fprintf(os.Stderr, "  %s\n", name)
		}
		fmt.Fprintf(os.Stderr, `
Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var cuts cut.Set
	var err error
	if len(fs.Args()) != 0 {
		cuts, err = cut.ParseCuts(fs.Args())
		if err != nil {
			fmt.Fprintf(os.Stderr, "cut: %v\n", err)
			return 2
		}
	}

	repoRoot := *root
	if repoRoot == "" {
		repoRoot, err = findRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cut: %v\n", err)
			return 1
		}
	}

	_, err = cut.Run(cut.Options{
		Root:      repoRoot,
		Cuts:      cuts,
		Apply:     *finalize,
		Module:    *modulePath,
		Stdout:    os.Stdout,
		Goimports: *goimports,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cut: %v\n", err)
		return 1
	}
	return 0
}

// findRoot walks up from the working directory looking for go.mod. Prefer
// scripts/cut, which passes --root from the script path.
func findRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate repository root from %s (pass --root)", wd)
		}
		dir = parent
	}
}
