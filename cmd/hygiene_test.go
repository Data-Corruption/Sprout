package main

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test working directory")
		}
		dir = parent
	}
}

// TestNoMisnamedTestFiles rejects Go files that read like tests but are not.
// Only a "_test.go" suffix makes a file a test file, so a name such as
// "feature_test_unix.go" compiles into the package proper: its tests never run
// and its imports reach the shipped binary. Neither go build nor go vet
// reports this.
func TestNoMisnamedTestFiles(t *testing.T) {
	root := repoRoot(t)
	skipDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		"vendor":       true,
		"testdata":     true,
		"out":          true,
		"tools":        true,
		"docs":         true,
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if strings.Contains(name, "_test_") {
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				relative = path
			}
			t.Errorf("%s compiles as ordinary source; rename it to end in _test.go", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
}

// TestBinaryDoesNotLinkTesting keeps the testing package out of the shipped
// binary. It is the property-level companion to TestNoMisnamedTestFiles, which
// only catches one way of breaking it.
func TestBinaryDoesNotLinkTesting(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go tool not available: %v", err)
	}

	// Source-cut matrices run in clean temporary trees without .git metadata;
	// dependency inspection does not need VCS stamping.
	command := exec.Command(goTool, "list", "-buildvcs=false", "-deps", ".")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v: %s", err, stderr.String())
	}

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		if scanner.Text() == "testing" {
			t.Fatal("the main binary links the testing package; check for a misnamed test file or a stray testing import")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read go list output: %v", err)
	}
}
