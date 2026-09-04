// --- FILE template ---

// Command goimports is a test double for the external formatter. Unit tests
// only need to verify invocation and formatting; the cut matrix exercises the
// real pinned goimports binary against complete source trees.
package main

import (
	"fmt"
	"go/format"
	"os"
	"strings"
)

func main() {
	files, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, path := range files {
		source, err := os.ReadFile(path)
		if err != nil {
			fail(path, err)
		}
		formatted, err := format.Source(source)
		if err != nil {
			fail(path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			fail(path, err)
		}
		if err := os.WriteFile(path, formatted, info.Mode().Perm()); err != nil {
			fail(path, err)
		}
	}
}

func parseArgs(args []string) ([]string, error) {
	write := false
	for len(args) > 0 {
		switch args[0] {
		case "-w":
			write = true
			args = args[1:]
		case "-local":
			if len(args) < 2 {
				return nil, fmt.Errorf("-local requires a value")
			}
			args = args[2:]
		default:
			if strings.HasPrefix(args[0], "-") {
				return nil, fmt.Errorf("unsupported flag %q", args[0])
			}
			if !write {
				return nil, fmt.Errorf("-w is required")
			}
			return args, nil
		}
	}
	return nil, fmt.Errorf("no input files")
}

func fail(path string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
	os.Exit(1)
}
