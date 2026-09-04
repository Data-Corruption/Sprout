// --- FILE template ---

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	dir, err := os.MkdirTemp("", "sprout-test-goimports-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create fake goimports directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	name := "goimports"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(dir, name)
	command := exec.Command(
		"go",
		"build",
		"-o",
		binary,
		filepath.Join("..", "..", "internal", "cut", "testdata", "goimports"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fake goimports: %v\n%s", err, output)
		return 1
	}

	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		fmt.Fprintf(os.Stderr, "set fake goimports PATH: %v\n", err)
		return 1
	}
	defer os.Setenv("PATH", oldPath)
	return m.Run()
}
