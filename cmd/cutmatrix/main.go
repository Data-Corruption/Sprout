// --- FILE template ---

// Command cutmatrix validates the canonical pruned source trees in temporary
// copies. It is upstream template tooling, not part of generated applications.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type variant struct {
	name     string
	cuts     []string
	hasHTTPS bool
}

var updateVariants = []variant{
	{name: "no-update", cuts: []string{"update"}},
	{name: "update-core", cuts: []string{"update.self", "update.notifications"}},
	{name: "update-self", cuts: []string{"update.notifications"}},
	{name: "update-notifications", cuts: []string{"update.self"}},
	{name: "update-self-notifications", cuts: []string{"update.auto"}},
	{name: "full-update"},
}

var serviceVariants = []variant{
	{name: "no-service", cuts: []string{"service"}},
	{name: "headless-service", cuts: []string{"service.https"}},
	{name: "https-service", hasHTTPS: true},
}

func canonicalVariants() []variant {
	var variants []variant
	for _, update := range updateVariants {
		for _, service := range serviceVariants {
			variants = append(variants, variant{
				name:     update.name + "-" + service.name,
				cuts:     append(append([]string{}, update.cuts...), service.cuts...),
				hasHTTPS: service.hasHTTPS,
			})
		}
	}
	return variants
}

func main() {
	os.Exit(run())
}

func run() int {
	rootFlag := flag.String("root", ".", "Sprout repository root")
	parallelFlag := flag.Int("parallel", min(2, runtime.NumCPU()), "maximum variants tested concurrently")
	timeoutFlag := flag.Duration("timeout", 10*time.Minute, "timeout per variant")
	flag.Parse()

	if *parallelFlag < 1 {
		fmt.Fprintln(os.Stderr, "cutmatrix: parallel must be at least 1")
		return 2
	}
	root, err := filepath.Abs(*rootFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cutmatrix: resolve root: %v\n", err)
		return 2
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		fmt.Fprintf(os.Stderr, "cutmatrix: %s is not a repository root: %v\n", root, err)
		return 2
	}
	variants := canonicalVariants()

	matrixRoot, err := os.MkdirTemp("", "sprout-cut-matrix-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cutmatrix: create temporary root: %v\n", err)
		return 1
	}
	esbuildPath, err := vendorTool(root, "esbuild", *timeoutFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cutmatrix: prepare frontend bundler: %v\nretained root: %s\n", err, matrixRoot)
		return 1
	}

	type result struct {
		variant variant
		path    string
		err     error
	}
	results := make(chan result, len(variants))
	semaphore := make(chan struct{}, *parallelFlag)
	var group sync.WaitGroup
	for _, item := range variants {
		group.Add(1)
		go func() {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			path := filepath.Join(matrixRoot, item.name)
			results <- result{
				variant: item,
				path:    path,
				err:     testVariant(root, path, esbuildPath, item, *timeoutFlag),
			}
		}()
	}
	group.Wait()
	close(results)

	failed := false
	for result := range results {
		if result.err != nil {
			failed = true
			fmt.Fprintf(
				os.Stderr,
				"FAIL %s (cut args: %s)\nretained tree: %s\n%v\n",
				result.variant.name,
				displayCuts(result.variant.cuts),
				result.path,
				result.err,
			)
			continue
		}
		fmt.Printf("PASS %s (cut args: %s)\n", result.variant.name, displayCuts(result.variant.cuts))
	}
	if failed {
		fmt.Fprintf(os.Stderr, "cutmatrix: retained failing trees under %s\n", matrixRoot)
		return 1
	}
	if err := os.RemoveAll(matrixRoot); err != nil {
		fmt.Fprintf(os.Stderr, "cutmatrix: remove successful temporary trees: %v\n", err)
		return 1
	}
	return 0
}

func testVariant(root, destination, esbuildPath string, item variant, timeout time.Duration) error {
	if err := copyTree(root, destination); err != nil {
		return fmt.Errorf("copy source: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cutArgs := append(
		[]string{"--finalize", "--module", "example.com/sprout-cutmatrix"},
		item.cuts...,
	)
	if output, err := runCommand(ctx, destination, "./scripts/cut", cutArgs...); err != nil {
		return commandError(ctx, "cut "+strings.Join(cutArgs, " "), output, err)
	}
	if err := validateFinalTree(destination, item); err != nil {
		return err
	}
	if item.hasHTTPS {
		if err := createFrontendPlaceholders(destination); err != nil {
			return err
		}
		if output, err := runCommand(
			ctx,
			destination,
			esbuildPath,
			"internal/ui/assets/js/src/main.js",
			"--bundle",
			"--minify",
			"--outfile=internal/ui/assets/js/output.js",
		); err != nil {
			return commandError(ctx, "bundle frontend JavaScript", output, err)
		}
	}

	if output, err := runCommand(ctx, destination, "bash", "-n", "scripts/install.sh"); err != nil {
		return commandError(ctx, "syntax-check scripts/install.sh", output, err)
	}
	if output, err := runCommand(ctx, destination, "go", "test", "-race", "./..."); err != nil {
		return commandError(ctx, "go test -race ./...", output, err)
	}
	return nil
}

// validateFinalTree checks that the right branch survived for this variant.
// The structural post-conditions - no marker survives, every planned deletion
// happened - belong to the cutter and are asserted by cut.Verify during
// --finalize, so they are deliberately absent here.
func validateFinalTree(root string, item variant) error {
	scenario := "default"
	smokeArgs := `$SmokeArgs = @("users", "list")`
	servicePort := "$ServiceDefaultPort = 8484"
	hasHTTPS := true
	switch {
	case hasCut(item.cuts, "service"):
		scenario = "no-service"
		smokeArgs = `$SmokeArgs = @("config", "show")`
		servicePort = "$ServiceDefaultPort = 0"
		hasHTTPS = false
	case hasCut(item.cuts, "service.https"):
		scenario = "headless"
		smokeArgs = `$SmokeArgs = @("config", "show")`
		servicePort = "$ServiceDefaultPort = 0"
		hasHTTPS = false
	}

	script, err := os.ReadFile(filepath.Join(root, "scripts", "test-lifecycle-e2e.sh"))
	if err != nil {
		return fmt.Errorf("read finalized scripts/test-lifecycle-e2e.sh: %w", err)
	}
	if got := lastAssignment(string(script), `SCENARIO="`); got != `SCENARIO="`+scenario+`"` {
		return fmt.Errorf("finalized installer scenario assignment is %q, want %q", got, scenario)
	}

	powerShell, err := os.ReadFile(filepath.Join(root, "scripts", "test-lifecycle-e2e.ps1"))
	if err != nil {
		return fmt.Errorf("read finalized scripts/test-lifecycle-e2e.ps1: %w", err)
	}
	if got := lastAssignment(string(powerShell), "$SmokeArgs ="); got != smokeArgs {
		return fmt.Errorf("finalized PowerShell smoke assignment is %q, want %q", got, smokeArgs)
	}
	if got := lastAssignment(string(powerShell), "$ServiceDefaultPort ="); got != servicePort {
		return fmt.Errorf("finalized PowerShell service port assignment is %q, want %q", got, servicePort)
	}

	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		return fmt.Errorf("read finalized release workflow: %w", err)
	}
	hasPlaceholders := strings.Contains(string(workflow), "Create compile-only frontend placeholders")
	if hasPlaceholders != hasHTTPS {
		return fmt.Errorf("finalized frontend placeholders present = %t, want %t", hasPlaceholders, hasHTTPS)
	}
	return nil
}

func hasCut(cuts []string, target string) bool {
	for _, cut := range cuts {
		if cut == target {
			return true
		}
	}
	return false
}

func lastAssignment(source, prefix string) string {
	var assignment string
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			assignment = line
		}
	}
	return assignment
}

// vendorTool ensures a pinned build tool is present and returns the absolute
// path scripts/vendor.sh resolved for it. Versions, hashes, and the fetch
// itself live there; the matrix only invokes it, the same way it invokes
// scripts/cut.
func vendorTool(root, tool string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, "./scripts/vendor.sh", tool)
	command.Dir = root
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return "", commandError(ctx, "vendor "+tool, stdout.Bytes(), err)
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if path, found := strings.CutPrefix(strings.TrimSpace(line), tool+"="); found {
			return path, nil
		}
	}
	return "", fmt.Errorf("scripts/vendor.sh %s printed no path", tool)
}

func runCommand(ctx context.Context, directory, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	return command.CombinedOutput()
}

func commandError(ctx context.Context, label string, output []byte, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out: %w\n%s", label, ctx.Err(), output)
	}
	return fmt.Errorf("%s: %w\n%s", label, err, output)
}

func copyTree(source, destination string) error {
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	command := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	command.Dir = source
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("list repository source files: %w", err)
	}
	for _, raw := range bytes.Split(output, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		relative := filepath.FromSlash(string(raw))
		sourcePath := filepath.Join(source, relative)
		info, err := os.Lstat(sourcePath)
		if errors.Is(err, fs.ErrNotExist) {
			continue // tracked file deleted in the working tree
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if err := copyFile(sourcePath, filepath.Join(destination, relative), info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string, mode fs.FileMode) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := output.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(output, input)
	return err
}

func createFrontendPlaceholders(root string) error {
	files := map[string]string{
		"internal/ui/assets/css/output.css": "",
		"internal/ui/assets/js/output.js":   "",
		"internal/ui/assets/manifest.json":  `{"css/output.css":"test","js/output.js":"test"}`,
	}
	for relative, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("create frontend placeholder directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			return fmt.Errorf("write frontend placeholder %s: %w", relative, err)
		}
	}
	return nil
}

func displayCuts(cuts []string) string {
	if len(cuts) == 0 {
		return "(none)"
	}
	return strings.Join(cuts, " ")
}
