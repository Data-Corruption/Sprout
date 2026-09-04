// --- FILE template ---

package cut

import (
	"bytes"
	"errors"
	"go/format"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestParseCuts(t *testing.T) {
	t.Parallel()

	cuts, err := ParseCuts([]string{"update", "service.https", "update"})
	if err != nil {
		t.Fatal(err)
	}
	want := Set{
		"update":               {},
		"update.self":          {},
		"update.notifications": {},
		"update.auto":          {},
		"service.https":        {},
	}
	if !reflect.DeepEqual(cuts, want) {
		t.Fatalf("cuts = %#v, want %#v", cuts, want)
	}

	child, err := ParseCuts([]string{"update.self"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Set{"update.self": {}}); !reflect.DeepEqual(child, want) {
		t.Fatalf("child cut = %#v, want %#v", child, want)
	}

	if _, err := ParseCuts(nil); err == nil {
		t.Fatal("ParseCuts(nil) succeeded")
	}
	if _, err := ParseCuts([]string{"updates"}); err == nil {
		t.Fatal("ParseCuts accepted an unknown feature")
	}
	if _, err := ParseCuts([]string{templateOwner}); err == nil {
		t.Fatal("ParseCuts accepted reserved template ownership")
	}
}

func TestRunGoimports(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	source := []byte("package sample\n\nvar value=struct { Name string }{Name:\"test\"}\n")
	writeTestFile(t, path, source, 0o644)

	binary, err := resolveGoimports("")
	if err != nil {
		t.Fatal(err)
	}
	if err := runGoimports(binary, root, "example.com/sample", []string{path}); err != nil {
		t.Fatal(err)
	}
	want, err := format.Source(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, path); !bytes.Equal(got, want) {
		t.Fatalf("goimports output is wrong:\n%s", got)
	}
}

func TestRunRejectsInvalidGoimportsBeforeApplying(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	source := []byte(markerLine("//", "FILE", "update") + "\npackage sample\n")
	writeTestFile(t, path, source, 0o644)

	_, err := Run(Options{
		Root:      root,
		Apply:     true,
		Goimports: filepath.Join(root, "missing-goimports"),
	})
	if err == nil || !strings.Contains(err.Error(), "is not executable") {
		t.Fatalf("Run error = %v, want invalid goimports error", err)
	}
	if got := readTestFile(t, path); !bytes.Equal(got, source) {
		t.Fatalf("Run changed the tree before rejecting goimports:\n%s", got)
	}
}

func TestRunFinalizesTemplateOwnership(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	toolDir := filepath.Join(root, "cmd", "cut")
	toolPath := filepath.Join(toolDir, "tool.go")
	tool := markerLine("//", "FILE", templateOwner) + "\npackage tool\n"
	writeTestFile(t, toolPath, []byte(tool), 0o644)
	writeTestFile(
		t,
		filepath.Join(toolDir, "testdata", "fixture.txt"),
		[]byte("template test fixture\n"),
		0o644,
	)

	sharedPath := filepath.Join(root, "workflow.yml")
	shared := "keep: true\n" +
		markerLine("#", "BEGIN", templateOwner) +
		"template: true\n" +
		markerLine("#", "END", templateOwner) +
		markerLine("#", "BEGIN", "service") +
		"service: true\n" +
		markerLine("#", "END", "service")
	writeTestFile(t, sharedPath, []byte(shared), 0o644)

	preview, err := Run(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.RemovedFiles, []string{"cmd/cut/tool.go"}) ||
		!reflect.DeepEqual(preview.RemovedDirectories, []string{"cmd/cut"}) ||
		preview.RemovedBlocks != 1 || preview.StrippedMarkers != 2 {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	if got := readTestFile(t, toolPath); !bytes.Equal(got, []byte(tool)) {
		t.Fatal("preview removed template file")
	}
	if got := readTestFile(t, sharedPath); !bytes.Equal(got, []byte(shared)) {
		t.Fatal("preview rewrote shared file")
	}

	applied, err := Run(Options{Root: root, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied, preview) {
		t.Fatalf("applied result = %#v, want preview %#v", applied, preview)
	}
	if _, err := os.Stat(toolPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("template file still exists: %v", err)
	}
	if _, err := os.Stat(toolDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty template directory still exists: %v", err)
	}
	got := readTestFile(t, sharedPath)
	if bytes.Contains(got, []byte("template: true")) ||
		bytes.Contains(got, []byte("---")) ||
		!bytes.Contains(got, []byte("service: true")) {
		t.Fatalf("shared finalization is wrong:\n%s", got)
	}
}

func TestApplyPlansDeletesTemplateFilesLast(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	templatePath := filepath.Join(root, "cut.go")
	writeTestFile(t, templatePath, []byte("tool\n"), 0o644)
	plans := []filePlan{
		{
			path:        templatePath,
			relative:    "cut.go",
			delete:      true,
			fileFeature: templateOwner,
		},
		{
			path:     filepath.Join(root, "missing", "main.go"),
			relative: "missing/main.go",
			mode:     0o644,
			data:     []byte("package main\n"),
		},
	}
	if err := applyPlans(plans, nil); err == nil {
		t.Fatal("applyPlans succeeded with an unwritable retained file")
	}
	if _, err := os.Stat(templatePath); err != nil {
		t.Fatalf("template tool was removed before retained-file failure: %v", err)
	}
}

func TestRunParentCutAndIdempotency(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	source := "package sample\n\n" +
		markerLine("//", "BEGIN", "update") +
		"func Check() {}\n\n" +
		markerLine("//", "BEGIN", "update.self") +
		"func Apply() {}\n" +
		markerLine("//", "END", "update.self") +
		markerLine("//", "END", "update") + "\n" +
		markerLine("//", "BEGIN", "service.https") +
		"func Dashboard() {}\n" +
		markerLine("//", "END", "service.https")
	writeTestFile(t, path, []byte(source), 0o644)

	cuts := mustCuts(t, "update")
	result, err := Run(Options{Root: root, Cuts: cuts, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedBlocks != 2 || !reflect.DeepEqual(result.ChangedFiles, []string{"main.go"}) {
		t.Fatalf("unexpected result: %#v", result)
	}

	got := readTestFile(t, path)
	if bytes.Contains(got, []byte("Check")) || bytes.Contains(got, []byte("Apply")) ||
		bytes.Contains(got, []byte("BEGIN update")) {
		t.Fatalf("update code survived:\n%s", got)
	}
	if !bytes.Contains(got, []byte("Dashboard")) ||
		bytes.Contains(got, []byte("BEGIN service.https")) {
		t.Fatalf("retained feature was removed:\n%s", got)
	}
	formatted, err := format.Source(got)
	if err != nil {
		t.Fatalf("changed source is invalid: %v\n%s", err, got)
	}
	if !bytes.Equal(got, formatted) {
		t.Fatalf("changed Go source was not formatted:\n%s", got)
	}

	before := append([]byte(nil), got...)
	result, err = Run(Options{Root: root, Cuts: cuts, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ChangedFiles) != 0 || len(result.RemovedFiles) != 0 || result.RemovedBlocks != 0 {
		t.Fatalf("repeated cut was not a no-op: %#v", result)
	}
	if got = readTestFile(t, path); !bytes.Equal(got, before) {
		t.Fatal("repeated cut changed the file")
	}
}

func TestRunMarkerStylesLineEndingsAndMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scriptPath := filepath.Join(root, "script.sh")
	script := "#!/bin/sh\r\n\r\nkeep=true\r\n" +
		toCRLF(markerLine("#", "BEGIN", "update.self")) +
		"self=true\r\n" +
		toCRLF(markerLine("#", "END", "update.self"))
	writeTestFile(t, scriptPath, []byte(script), 0o751)

	htmlPath := filepath.Join(root, "page.html")
	html := "<p>keep</p>\n" +
		markerLine("html", "BEGIN", "service.https") +
		"<p>dashboard</p>\n" +
		markerLine("html", "END", "service.https")
	writeTestFile(t, htmlPath, []byte(html), 0o644)

	cssPath := filepath.Join(root, "page.css")
	css := "body { color: black; }\n" +
		markerLine("css", "BEGIN", "service.https") +
		".dashboard { color: green; }\n" +
		markerLine("css", "END", "service.https")
	writeTestFile(t, cssPath, []byte(css), 0o644)

	result, err := Run(Options{
		Root:  root,
		Cuts:  mustCuts(t, "update.self", "service.https"),
		Apply: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedBlocks != 3 || len(result.ChangedFiles) != 3 {
		t.Fatalf("unexpected result: %#v", result)
	}

	scriptAfter := readTestFile(t, scriptPath)
	if bytes.Contains(scriptAfter, []byte("self=true")) {
		t.Fatalf("shell block survived:\n%s", scriptAfter)
	}
	if hasLoneLF(scriptAfter) {
		t.Fatalf("CRLF file gained an LF line ending: %q", scriptAfter)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o751 {
			t.Fatalf("mode = %o, want 751", got)
		}
	}

	htmlAfter := readTestFile(t, htmlPath)
	if bytes.Contains(htmlAfter, []byte("dashboard")) || !bytes.Contains(htmlAfter, []byte("<p>keep</p>")) {
		t.Fatalf("HTML cut is wrong:\n%s", htmlAfter)
	}
}

func TestRunWholeFileAndPreview(t *testing.T) {
	t.Parallel()

	t.Run("whole file", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "linux.go")
		source := "//go:build linux\n\n" +
			markerLine("//", "FILE", "update.self") +
			"\npackage platform\n"
		writeTestFile(t, path, []byte(source), 0o644)

		result, err := Run(Options{Root: root, Cuts: mustCuts(t, "update.self"), Apply: true})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.RemovedFiles, []string{"linux.go"}) {
			t.Fatalf("removed files = %#v", result.RemovedFiles)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("whole feature file still exists: %v", err)
		}
	})

	t.Run("preview", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "main.go")
		source := "package sample\n\n" +
			markerLine("//", "BEGIN", "update.self") +
			"func Apply() {}\n" +
			markerLine("//", "END", "update.self")
		writeTestFile(t, path, []byte(source), 0o644)

		var output bytes.Buffer
		result, err := Run(Options{
			Root:   root,
			Cuts:   mustCuts(t, "update.self"),
			Stdout: &output,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.ChangedFiles) != 1 || !strings.Contains(output.String(), "would update main.go") {
			t.Fatalf("unexpected preview result/output: %#v\n%s", result, output.String())
		}
		if got := readTestFile(t, path); !bytes.Equal(got, []byte(source)) {
			t.Fatal("preview changed the source")
		}
	})
}

func TestRunFinalizesMarkers(t *testing.T) {
	t.Parallel()

	t.Run("retains all code without feature arguments", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "main.go")
		source := markerLine("//", "FILE", "update") +
			"\npackage sample\n\n" +
			"func Check() {}\n\n" +
			markerLine("//", "BEGIN", "update.self") +
			"func Apply() {}\n" +
			markerLine("//", "END", "update.self")
		writeTestFile(t, path, []byte(source), 0o644)

		result, err := Run(Options{Root: root, Apply: true})
		if err != nil {
			t.Fatal(err)
		}
		if result.StrippedMarkers != 3 || !reflect.DeepEqual(result.ChangedFiles, []string{"main.go"}) {
			t.Fatalf("unexpected result: %#v", result)
		}
		got := readTestFile(t, path)
		if bytes.Contains(got, []byte("---")) ||
			!bytes.Contains(got, []byte("func Check")) ||
			!bytes.Contains(got, []byte("func Apply")) {
			t.Fatalf("finalization changed retained code:\n%s", got)
		}

		result, err = Run(Options{Root: root, Apply: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.ChangedFiles) != 0 || result.StrippedMarkers != 0 {
			t.Fatalf("repeated finalization was not a no-op: %#v", result)
		}
	})

	t.Run("combines cuts with stripping retained markers", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "main.go")
		source := "package sample\n\n" +
			markerLine("//", "BEGIN", "update.self") +
			"func Apply() {}\n" +
			markerLine("//", "END", "update.self") + "\n" +
			markerLine("//", "BEGIN", "service.https") +
			"func Dashboard() {}\n" +
			markerLine("//", "END", "service.https")
		writeTestFile(t, path, []byte(source), 0o644)

		result, err := Run(Options{
			Root:  root,
			Cuts:  mustCuts(t, "update.self"),
			Apply: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.RemovedBlocks != 1 || result.StrippedMarkers != 2 {
			t.Fatalf("unexpected result: %#v", result)
		}
		got := readTestFile(t, path)
		if bytes.Contains(got, []byte("Apply")) ||
			bytes.Contains(got, []byte("---")) ||
			!bytes.Contains(got, []byte("Dashboard")) {
			t.Fatalf("combined cut/strip result is wrong:\n%s", got)
		}
	})

	t.Run("preview reports markers without writing", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "main.go")
		source := "package sample\n\n" +
			markerLine("//", "BEGIN", "update") +
			"func Check() {}\n" +
			markerLine("//", "END", "update")
		writeTestFile(t, path, []byte(source), 0o644)

		var output bytes.Buffer
		result, err := Run(Options{
			Root:   root,
			Stdout: &output,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.StrippedMarkers != 2 || !strings.Contains(output.String(), "would strip 2 marker(s)") {
			t.Fatalf("unexpected preview result/output: %#v\n%s", result, output.String())
		}
		if got := readTestFile(t, path); !bytes.Equal(got, []byte(source)) {
			t.Fatal("preview changed the source")
		}
	})
}

func TestRunRenamesModuleAfterCut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	goModPath := filepath.Join(root, "go.mod")
	writeTestFile(t, goModPath, []byte("module sprout\n\ngo 1.26.5\n"), 0o644)

	mainPath := filepath.Join(root, "main.go")
	source := "package sample\n\nimport (\n" +
		"\t\"sprout/internal/keep\"\n" +
		markerLine("//", "BEGIN", "update") +
		"\t\"sprout/internal/drop\"\n" +
		markerLine("//", "END", "update") +
		")\n\nvar _ = keep.Value\n"
	writeTestFile(t, mainPath, []byte(source), 0o644)

	result, err := Run(Options{
		Root:   root,
		Cuts:   mustCuts(t, "update"),
		Apply:  true,
		Module: "example.com/acme/app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedBlocks != 1 || result.StrippedMarkers != 0 || result.RenamedModuleRefs != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}

	goMod := readTestFile(t, goModPath)
	if !bytes.Contains(goMod, []byte("module example.com/acme/app")) {
		t.Fatalf("go.mod was not renamed:\n%s", goMod)
	}
	main := readTestFile(t, mainPath)
	if !bytes.Contains(main, []byte(`"example.com/acme/app/internal/keep"`)) ||
		bytes.Contains(main, []byte("sprout/internal")) ||
		bytes.Contains(main, []byte("drop")) ||
		bytes.Contains(main, []byte("---")) {
		t.Fatalf("source was not cut and renamed correctly:\n%s", main)
	}
}

func TestRunModuleRenamePreviewAndPreflight(t *testing.T) {
	t.Parallel()

	t.Run("preview", func(t *testing.T) {
		root := t.TempDir()
		goModPath := filepath.Join(root, "go.mod")
		mainPath := filepath.Join(root, "main.go")
		goMod := []byte("module sprout\n\ngo 1.26.5\n")
		main := []byte("package sample\n\nimport \"sprout/internal/build\"\n")
		writeTestFile(t, goModPath, goMod, 0o644)
		writeTestFile(t, mainPath, main, 0o644)

		var output bytes.Buffer
		result, err := Run(Options{
			Root:   root,
			Module: "example.com/acme/app",
			Stdout: &output,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.RenamedModuleRefs != 2 ||
			!strings.Contains(output.String(), "would update go.mod (1 module reference(s))") ||
			!strings.Contains(output.String(), "would update main.go (1 module reference(s))") {
			t.Fatalf("unexpected preview result/output: %#v\n%s", result, output.String())
		}
		if got := readTestFile(t, goModPath); !bytes.Equal(got, goMod) {
			t.Fatal("preview changed go.mod")
		}
		if got := readTestFile(t, mainPath); !bytes.Equal(got, main) {
			t.Fatal("dry run changed Go source")
		}
	})

	t.Run("unknown non-Go reference prevents all writes", func(t *testing.T) {
		root := t.TempDir()
		goModPath := filepath.Join(root, "go.mod")
		mainPath := filepath.Join(root, "main.go")
		goMod := []byte("module sprout\n\ngo 1.26.5\n")
		main := []byte(markerLine("//", "FILE", "update") + "\npackage sample\n")
		writeTestFile(t, goModPath, goMod, 0o644)
		writeTestFile(t, mainPath, main, 0o644)
		writeTestFile(
			t,
			filepath.Join(root, "build.sh"),
			[]byte("pkg=sprout/internal/build\n"),
			0o755,
		)

		_, err := Run(Options{
			Root:   root,
			Module: "example.com/acme/app",
		})
		if err == nil || !strings.Contains(err.Error(), "outside go.mod or a Go import") {
			t.Fatalf("Run error = %v, want unresolved module reference", err)
		}
		if got := readTestFile(t, goModPath); !bytes.Equal(got, goMod) {
			t.Fatal("go.mod changed before preflight completed")
		}
		if got := readTestFile(t, mainPath); !bytes.Equal(got, main) {
			t.Fatal("source changed before preflight completed")
		}
	})

	t.Run("binary reference is rejected", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "go.mod"), []byte("module sprout\n"), 0o644)
		binary := append([]byte{0xff, 0x00}, []byte("sprout/internal/build")...)
		writeTestFile(t, filepath.Join(root, "fixture.bin"), binary, 0o644)

		_, err := Run(Options{
			Root:   root,
			Module: "example.com/acme/app",
		})
		if err == nil || !strings.Contains(err.Error(), "binary file") {
			t.Fatalf("Run error = %v, want binary module reference rejection", err)
		}
	})

	t.Run("symlink is rejected", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "go.mod"), []byte("module sprout\n"), 0o644)
		target := filepath.Join(root, "target.txt")
		writeTestFile(t, target, []byte("ordinary data\n"), 0o644)
		if err := os.Symlink(target, filepath.Join(root, "link.txt")); err != nil {
			t.Skipf("symlink test unavailable: %v", err)
		}

		_, err := Run(Options{
			Root:   root,
			Module: "example.com/acme/app",
		})
		if err == nil || !strings.Contains(err.Error(), "symlinks are not supported") {
			t.Fatalf("Run error = %v, want symlink rejection", err)
		}
	})
}

func TestRunRejectsInvalidModulePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), []byte("module sprout\n"), 0o644)

	if _, err := Run(Options{
		Root:   root,
		Module: "https://example.com/acme/app",
	}); err == nil || !strings.Contains(err.Error(), "invalid module path") {
		t.Fatalf("Run accepted invalid module path: %v", err)
	}
}

func TestRunPreflightsBeforeWriting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	validPath := filepath.Join(root, "a.go")
	valid := "package sample\n\n" +
		markerLine("//", "BEGIN", "update") +
		"func Check() {}\n" +
		markerLine("//", "END", "update")
	writeTestFile(t, validPath, []byte(valid), 0o644)

	invalidPath := filepath.Join(root, "z.go")
	invalid := "package sample\n\n" + markerLine("//", "BEGIN", "service")
	writeTestFile(t, invalidPath, []byte(invalid), 0o644)

	if _, err := Run(Options{Root: root, Cuts: mustCuts(t, "update")}); err == nil {
		t.Fatal("Run succeeded with an unclosed fence")
	}
	if got := readTestFile(t, validPath); !bytes.Equal(got, []byte(valid)) {
		t.Fatal("valid file changed before preflight completed")
	}
}

func TestRunRejectsMalformedMarkers(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unknown feature": "package bad\n\n" +
			markerLine("//", "BEGIN", "nope"),
		"mismatched end": "package bad\n\n" +
			markerLine("//", "BEGIN", "update") +
			markerLine("//", "END", "update.self"),
		"duplicate nesting": "package bad\n\n" +
			markerLine("//", "BEGIN", "update") +
			markerLine("//", "BEGIN", "update") +
			markerLine("//", "END", "update") +
			markerLine("//", "END", "update"),
		"late file marker": "package bad\n\nfunc X() {}\n" +
			markerLine("//", "FILE", "update"),
		"malformed marker": "package bad\n\n// --- BEGIN update --\n",
	}

	for name, source := range tests {
		name, source := name, source
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "main.go"), []byte(source), 0o644)
			if _, err := Run(Options{Root: root, Cuts: mustCuts(t, "update")}); err == nil {
				t.Fatal("Run accepted malformed source")
			}
		})
	}
}

func TestRunSkipsOutputsVCSAndSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, relative := range []string{"out/generated.go", ".git/secret.go"} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		writeTestFile(t, path, []byte("package skipped\n\n"+markerLine("//", "BEGIN", "nope")), 0o644)
	}

	externalRoot := t.TempDir()
	external := filepath.Join(externalRoot, "external.go")
	writeTestFile(t, external, []byte("package skipped\n\n"+markerLine("//", "BEGIN", "nope")), 0o644)
	if err := os.Symlink(external, filepath.Join(root, "link.go")); err != nil {
		t.Logf("symlink test unavailable: %v", err)
	}

	kept := filepath.Join(root, "main.go")
	source := "package sample\n\n" +
		markerLine("//", "BEGIN", "update.self") +
		"func Apply() {}\n" +
		markerLine("//", "END", "update.self")
	writeTestFile(t, kept, []byte(source), 0o644)

	if _, err := Run(Options{Root: root, Cuts: mustCuts(t, "update.self"), Apply: true}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(readTestFile(t, kept), []byte("Apply")) {
		t.Fatal("ordinary source file was not cut")
	}
}

func markerLine(style, verb, feature string) string {
	switch style {
	case "html":
		return "<!-- --- " + verb + " " + feature + " --- -->\n"
	case "css":
		return "/* --- " + verb + " " + feature + " --- */\n"
	default:
		return style + " --- " + verb + " " + feature + " ---\n"
	}
}

func mustCuts(t *testing.T, names ...string) Set {
	t.Helper()
	cuts, err := ParseCuts(names)
	if err != nil {
		t.Fatal(err)
	}
	return cuts
}

func writeTestFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func toCRLF(text string) string {
	return strings.ReplaceAll(text, "\n", "\r\n")
}

func hasLoneLF(data []byte) bool {
	for index, value := range data {
		if value == '\n' && (index == 0 || data[index-1] != '\r') {
			return true
		}
	}
	return false
}
