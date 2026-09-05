// --- FILE template ---

package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sprout/internal/cut"
)

func TestRunFeatureContractWithoutCheckout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = original; r.Close(); w.Close() }()
	code := run([]string{"--list-features-json", "--root", filepath.Join(t.TempDir(), "missing")})
	w.Close()
	os.Stdout = original
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("contract returned %d", code)
	}
	var got cut.Contract
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("not pure JSON: %v\n%s", err, data)
	}
	if got.Version != 1 || len(got.Features) != len(cut.Features()) {
		t.Fatalf("unexpected contract: %+v", got)
	}
	for i, feature := range got.Features {
		name := cut.Features()[i]
		if feature.Name != name || strings.Join(feature.Prerequisites, ",") != strings.Join(cut.Prerequisites(name), ",") {
			t.Fatalf("contract disagrees with cutter: %+v", feature)
		}
	}
	for _, args := range [][]string{{"--finalize"}, {"--module", "example.com/me/app"}, {"service"}} {
		if code := run(append([]string{"--list-features-json"}, args...)); code != 2 {
			t.Fatalf("accepted mixed discovery args: %v", args)
		}
	}
}

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
