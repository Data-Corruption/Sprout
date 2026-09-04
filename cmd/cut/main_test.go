// --- FILE template ---

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPreviewsFinalTreeByDefault(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	source := "// --- FILE template ---\n\npackage sample\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"--root", root}); code != 0 {
		t.Fatalf("run(preview) = %d, want 0", code)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != source {
		t.Fatalf("preview changed source:\n%s", data)
	}
}

func TestRunFinalizeWithoutFeatures(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	source := "// --- FILE update ---\n\npackage sample\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"--root", root, "--finalize"}); code != 0 {
		t.Fatalf("run(finalize) = %d, want 0", code)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "--- FILE") || !strings.Contains(string(data), "package sample") {
		t.Fatalf("unexpected finalized source:\n%s", data)
	}
}

func TestRunRenameModulePreviewAndFinalize(t *testing.T) {
	root := t.TempDir()
	goModPath := filepath.Join(root, "go.mod")
	mainPath := filepath.Join(root, "main.go")
	if err := os.WriteFile(goModPath, []byte("module sprout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "internal", "build", "build.go"),
		[]byte("package build\n\nconst Name = \"sample\"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	// The import has to be referenced: finalizing runs goimports, which would
	// otherwise (correctly) drop it before the rename could be observed.
	if err := os.WriteFile(
		mainPath,
		[]byte("package sample\n\nimport \"sprout/internal/build\"\n\nvar name = build.Name\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{
		"--root", root,
		"--module", "example.com/acme/app",
	}); code != 0 {
		t.Fatalf("run(module preview) = %d, want 0", code)
	}
	if got, err := os.ReadFile(goModPath); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(got), "module sprout") {
		t.Fatalf("preview changed go.mod:\n%s", got)
	}

	if code := run([]string{
		"--root", root,
		"--finalize",
		"--module", "example.com/acme/app",
	}); code != 0 {
		t.Fatalf("run(module finalize) = %d, want 0", code)
	}
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goMod), "module example.com/acme/app") ||
		!strings.Contains(string(main), `"example.com/acme/app/internal/build"`) {
		t.Fatalf("module was not renamed:\n%s\n%s", goMod, main)
	}
}

func TestRunRejectsRemovedFlags(t *testing.T) {
	for _, flag := range []string{"--dry-run", "--strip-markers"} {
		if code := run([]string{flag}); code != 2 {
			t.Fatalf("run(%s) = %d, want 2", flag, code)
		}
	}
}
