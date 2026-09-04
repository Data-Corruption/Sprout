// --- FILE template ---

package cut

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Options controls one source-tree cut.
type Options struct {
	Root   string
	Cuts   Set
	Apply  bool
	Module string
	Stdout io.Writer
	// Go is the Go command used for the best-effort module tidy after a cut.
	// Empty falls back to PATH.
	Go string
	// Goimports is the path to the goimports binary used to prune imports
	// left unused by removed blocks. Required when Apply rewrites Go files;
	// scripts/cut supplies the pinned one from scripts/vendor.sh. Empty falls
	// back to PATH so a direct `go run ./cmd/cut` still works.
	Goimports string
}

// Result describes the files and blocks selected by a cut.
type Result struct {
	RemovedFiles               []string
	RemovedTemplateDirectories []string
	RemovedEmptyDirectories    []string
	ChangedFiles               []string
	RemovedBlocks              int
	StrippedMarkers            int
	RenamedModuleRefs          int
}

type filePlan struct {
	path        string
	relative    string
	mode        fs.FileMode
	data        []byte
	delete      bool
	fileFeature string
	blocks      int
	markers     int
	moduleRefs  int
}

type directoryPlan struct {
	path     string
	relative string
}

// Run validates every marker before changing any file, then applies the cut.
func Run(options Options) (Result, error) {
	root, err := validateRoot(options.Root)
	if err != nil {
		return Result{}, err
	}
	cuts, err := expandedCuts(options.Cuts)
	if err != nil {
		return Result{}, err
	}
	cuts[templateOwner] = struct{}{}
	rename, err := prepareModuleRename(root, options.Module)
	if err != nil {
		return Result{}, err
	}
	output := options.Stdout
	if output == nil {
		output = io.Discard
	}

	var plans []filePlan
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative != "." && skipDirectory(filepath.ToSlash(relative), entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if rename != nil {
				return fmt.Errorf(
					"%s: rename module: symlinks are not supported",
					filepath.ToSlash(relative),
				)
			}
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		isCandidate := candidateFile(relative)
		if !info.Mode().IsRegular() || (!isCandidate && rename == nil) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		plan := filePlan{
			path:     path,
			relative: slashed,
			mode:     info.Mode(),
		}
		changed := false
		if isCandidate {
			plan, changed, err = planFile(
				path,
				slashed,
				info.Mode(),
				data,
				cuts,
				true,
			)
			if err != nil {
				return err
			}
			if !changed {
				plan = filePlan{
					path:     path,
					relative: slashed,
					mode:     info.Mode(),
				}
			}
		}
		if plan.delete {
			plans = append(plans, plan)
			return nil
		}

		effective := data
		if changed {
			effective = plan.data
		}
		if rename != nil {
			switch {
			case slashed == "go.mod":
				if !bytes.Equal(effective, rename.modFile) {
					plan.data = append([]byte(nil), rename.modFile...)
					plan.moduleRefs++
					changed = true
				}
			case strings.EqualFold(filepath.Ext(relative), ".go"):
				rewritten, references, err := rename.rewriteGoImports(slashed, effective, data)
				if err != nil {
					return fmt.Errorf("%s: rename module: %w", slashed, err)
				}
				if references != 0 {
					plan.data = rewritten
					plan.moduleRefs += references
					changed = true
				}
			default:
				if err := rename.validateOtherFile(slashed, effective); err != nil {
					return fmt.Errorf("%s: rename module: %w", slashed, err)
				}
			}
		}
		if changed {
			plans = append(plans, plan)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	templateDirectories, err := planTemplateDirectories(plans)
	if err != nil {
		return Result{}, err
	}
	emptyDirectories, err := planEmptyDirectories(root, plans, templateDirectories)
	if err != nil {
		return Result{}, err
	}

	result := resultFor(plans, templateDirectories, emptyDirectories)
	if !options.Apply {
		if err := reportPlans(output, plans, templateDirectories, emptyDirectories, true); err != nil {
			return Result{}, err
		}
		if _, err := fmt.Fprintln(
			output,
			"Preview only; re-run with --finalize to apply this plan.\n"+
				"--finalize also runs goimports over the rewritten Go files, so imports\n"+
				"left unused by removed blocks disappear then rather than here. It then\n"+
				"runs go mod tidy to clean unused module requirements.",
		); err != nil {
			return Result{}, err
		}
		return result, nil
	}

	// Resolve before mutating anything, so a missing tool is not a half-cut tree.
	targets := goimportsTargets(plans)
	goimports := ""
	if len(targets) != 0 {
		if goimports, err = resolveGoimports(options.Goimports); err != nil {
			return Result{}, err
		}
	}
	if err := applyPlans(plans, templateDirectories, emptyDirectories); err != nil {
		return Result{}, err
	}
	if len(targets) != 0 {
		local := ""
		if rename != nil {
			local = rename.newPath
		}
		if err := runGoimports(goimports, root, local, targets); err != nil {
			return Result{}, err
		}
	}
	goTool := options.Go
	if goTool == "" {
		goTool = "go"
	}
	tidyErr := runGoModTidy(goTool, root)
	if err := Verify(root, result); err != nil {
		return Result{}, err
	}
	if err := reportPlans(output, plans, templateDirectories, emptyDirectories, false); err != nil {
		return Result{}, err
	}
	if tidyErr != nil {
		if _, err := fmt.Fprintf(
			output,
			"Warning: go mod tidy failed; the cut continued. Run it manually: %v\n",
			tidyErr,
		); err != nil {
			return Result{}, err
		}
	}
	if _, err := fmt.Fprintln(output, "Verify with: ./scripts/test.sh"); err != nil {
		return Result{}, err
	}
	return result, nil
}

// resolveGoimports picks the goimports binary to prune with. The pin lives in
// scripts/vendor.sh, never here.
func resolveGoimports(configured string) (string, error) {
	if configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("configured goimports %q is not executable: %w", configured, err)
		}
		return path, nil
	}
	path, err := exec.LookPath("goimports")
	if err != nil {
		return "", fmt.Errorf(
			"finalizing needs goimports: run ./scripts/cut or ./scripts/vendor.sh goimports, "+
				"either of which fetches the pinned version, or put goimports on PATH: %w",
			err,
		)
	}
	return path, nil
}

// goimportsTargets lists the Go files the cut rewrites. Passing an explicit
// list rather than the tree keeps deleted paths and the separate module under
// docs/ out of the pass.
func goimportsTargets(plans []filePlan) []string {
	files := make([]string, 0, len(plans))
	for _, plan := range plans {
		if plan.delete || !strings.EqualFold(filepath.Ext(plan.relative), ".go") {
			continue
		}
		files = append(files, plan.path)
	}
	return files
}

// runGoimports prunes imports the cut left unreferenced. format.Source keeps
// the planned bytes valid Go but cannot tell whether an import is still used;
// goimports resolves that, which is what lets Go sources carry no import-block
// fences at all.
func runGoimports(binary, root, localPrefix string, files []string) error {
	args := make([]string, 0, len(files)+3)
	args = append(args, "-w")
	if localPrefix != "" {
		args = append(args, "-local", localPrefix)
	}
	args = append(args, files...)
	command := exec.Command(binary, args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("prune imports with goimports: %w\n%s", err, output)
	}
	return nil
}

func runGoModTidy(binary, root string) error {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect go.mod: %w", err)
	}
	command := exec.Command(binary, "mod", "tidy")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("run go mod tidy: %w\n%s", err, output)
	}
	return nil
}

func planTemplateDirectories(plans []filePlan) ([]directoryPlan, error) {
	plannedFiles := make(map[string]map[string]struct{})
	relativeDirectories := make(map[string]string)
	for _, plan := range plans {
		if !plan.delete || plan.fileFeature != templateOwner {
			continue
		}
		directory := filepath.Dir(plan.path)
		if plannedFiles[directory] == nil {
			plannedFiles[directory] = make(map[string]struct{})
		}
		plannedFiles[directory][filepath.Base(plan.path)] = struct{}{}
		relativeDirectories[directory] = filepath.ToSlash(filepath.Dir(plan.relative))
	}

	var directories []directoryPlan
	for directory, files := range plannedFiles {
		// A root-level template file may be removed, but the repository root is
		// never itself a removable template directory.
		if relativeDirectories[directory] == "." {
			continue
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, fmt.Errorf("read template directory %s: %w", relativeDirectories[directory], err)
		}
		owned := true
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if _, ok := files[entry.Name()]; !ok {
				owned = false
				break
			}
		}
		if owned {
			directories = append(directories, directoryPlan{
				path:     directory,
				relative: relativeDirectories[directory],
			})
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		if len(directories[i].relative) == len(directories[j].relative) {
			return directories[i].relative < directories[j].relative
		}
		return len(directories[i].relative) > len(directories[j].relative)
	})
	return directories, nil
}

// planEmptyDirectories finds only directories that the cut itself makes
// empty. Pre-existing empty directories are not part of the cut plan and are
// left alone.
func planEmptyDirectories(
	root string,
	plans []filePlan,
	templateDirectories []directoryPlan,
) ([]directoryPlan, error) {
	removedFiles := make(map[string]struct{})
	candidates := make(map[string]string)
	addAncestors := func(path string) {
		for directory := filepath.Dir(path); directory != root; directory = filepath.Dir(directory) {
			relative, err := filepath.Rel(root, directory)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return
			}
			candidates[directory] = filepath.ToSlash(relative)
		}
	}
	for _, plan := range plans {
		if !plan.delete {
			continue
		}
		removedFiles[plan.path] = struct{}{}
		addAncestors(plan.path)
	}

	removedTemplateDirectories := make(map[string]struct{}, len(templateDirectories))
	for _, directory := range templateDirectories {
		removedTemplateDirectories[directory.path] = struct{}{}
		addAncestors(directory.path)
	}

	ordered := make([]directoryPlan, 0, len(candidates))
	for path, relative := range candidates {
		insideTemplateDirectory := false
		for directory := range removedTemplateDirectories {
			if path == directory || strings.HasPrefix(path, directory+string(os.PathSeparator)) {
				insideTemplateDirectory = true
				break
			}
		}
		if !insideTemplateDirectory {
			ordered = append(ordered, directoryPlan{path: path, relative: relative})
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i].relative) == len(ordered[j].relative) {
			return ordered[i].relative < ordered[j].relative
		}
		return len(ordered[i].relative) > len(ordered[j].relative)
	})

	plannedEmpty := make(map[string]struct{})
	emptyDirectories := make([]directoryPlan, 0, len(ordered))
	for _, directory := range ordered {
		entries, err := os.ReadDir(directory.path)
		if err != nil {
			return nil, fmt.Errorf("read possible empty directory %s: %w", directory.relative, err)
		}
		empty := true
		for _, entry := range entries {
			path := filepath.Join(directory.path, entry.Name())
			if _, removed := removedFiles[path]; removed {
				continue
			}
			if _, removed := removedTemplateDirectories[path]; removed {
				continue
			}
			if _, removed := plannedEmpty[path]; removed {
				continue
			}
			empty = false
			break
		}
		if empty {
			plannedEmpty[directory.path] = struct{}{}
			emptyDirectories = append(emptyDirectories, directory)
		}
	}
	return emptyDirectories, nil
}

func applyPlans(
	plans []filePlan,
	templateDirectories []directoryPlan,
	emptyDirectories []directoryPlan,
) error {
	for _, removeTemplate := range []bool{false, true} {
		for _, plan := range plans {
			isTemplateFile := plan.delete && plan.fileFeature == templateOwner
			if isTemplateFile != removeTemplate {
				continue
			}
			if plan.delete {
				if err := os.Remove(plan.path); err != nil {
					return fmt.Errorf("remove %s: %w", plan.relative, err)
				}
				continue
			}
			if err := replaceFile(plan.path, plan.data, plan.mode); err != nil {
				return fmt.Errorf("rewrite %s: %w", plan.relative, err)
			}
		}
	}
	for _, directory := range templateDirectories {
		entries, err := os.ReadDir(directory.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read finalized directory %s: %w", directory.relative, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				return fmt.Errorf(
					"finalized directory %s retained unplanned file %s",
					directory.relative,
					entry.Name(),
				)
			}
		}
		// Template packages may retain non-candidate fixture directories after
		// their marked source files are gone. With no ordinary files left at
		// the package root, those fixtures are template-owned too.
		if err := os.RemoveAll(directory.path); err != nil {
			return fmt.Errorf("remove finalized directory %s: %w", directory.relative, err)
		}
	}
	for _, directory := range emptyDirectories {
		if err := os.Remove(directory.path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("remove directory emptied by cut %s: %w", directory.relative, err)
		}
	}
	return nil
}

func validateRoot(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("repository root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("stat repository root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repository root %s is not a directory", absolute)
	}
	return absolute, nil
}

func expandedCuts(cuts Set) (Set, error) {
	if len(cuts) == 0 {
		return make(Set), nil
	}
	if err := validateCuts(cuts); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cuts))
	for name := range cuts {
		names = append(names, name)
	}
	return ParseCuts(names)
}

func skipDirectory(relative, name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor":
		return true
	}
	switch relative {
	case "out", "tools", "docs/resources/_gen":
		return true
	default:
		return false
	}
}

func candidateFile(relative string) bool {
	switch strings.ToLower(filepath.Ext(relative)) {
	case ".go", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx",
		".sh", ".bash", ".zsh", ".yaml", ".yml", ".html", ".htm", ".ps1",
		".css":
		return true
	}
	return filepath.Ext(relative) == "" &&
		strings.HasPrefix(filepath.ToSlash(relative), "scripts/")
}

func planFile(
	path, relative string,
	mode fs.FileMode,
	original []byte,
	cuts Set,
	stripMarkers bool,
) (filePlan, bool, error) {
	lines := bytes.SplitAfter(original, []byte{'\n'})
	var output bytes.Buffer
	stack := make([]string, 0, 3)
	preambleOK := true
	fileMarkerSeen := false
	deleteFile := false
	fileFeature := ""
	removedBlocks := 0
	strippedMarkers := 0

	for index, line := range lines {
		lineNumber := index + 1
		found, isMarker, err := parseMarker(line)
		if err != nil {
			return filePlan{}, false, fmt.Errorf("%s:%d: %w", relative, lineNumber, err)
		}
		if !isMarker {
			if !activeCut(stack, cuts) {
				output.Write(line)
			}
			if preambleOK && !allowedFileMarkerPreamble(line, lineNumber) {
				preambleOK = false
			}
			continue
		}

		switch found.kind {
		case markerFile:
			if len(stack) != 0 {
				return filePlan{}, false, fmt.Errorf("%s:%d: FILE marker inside feature block", relative, lineNumber)
			}
			if fileMarkerSeen {
				return filePlan{}, false, fmt.Errorf("%s:%d: duplicate FILE marker", relative, lineNumber)
			}
			if !preambleOK {
				return filePlan{}, false, fmt.Errorf("%s:%d: FILE marker must precede source code", relative, lineNumber)
			}
			fileMarkerSeen = true
			fileFeature = found.feature
			if contains(cuts, found.feature) {
				deleteFile = true
			} else if stripMarkers {
				strippedMarkers++
			} else {
				output.Write(line)
			}

		case markerBegin:
			if stackContains(stack, found.feature) {
				return filePlan{}, false, fmt.Errorf("%s:%d: feature %q nested inside itself", relative, lineNumber, found.feature)
			}
			stack = append(stack, found.feature)
			if contains(cuts, found.feature) {
				removedBlocks++
			}
			if !activeCut(stack, cuts) {
				if stripMarkers {
					strippedMarkers++
				} else {
					output.Write(line)
				}
			}
			preambleOK = false

		case markerEnd:
			if len(stack) == 0 {
				return filePlan{}, false, fmt.Errorf("%s:%d: END %q without matching BEGIN", relative, lineNumber, found.feature)
			}
			open := stack[len(stack)-1]
			if open != found.feature {
				return filePlan{}, false, fmt.Errorf("%s:%d: END %q does not match BEGIN %q", relative, lineNumber, found.feature, open)
			}
			if !activeCut(stack, cuts) {
				if stripMarkers {
					strippedMarkers++
				} else {
					output.Write(line)
				}
			}
			stack = stack[:len(stack)-1]
			preambleOK = false
		}
	}
	if len(stack) != 0 {
		return filePlan{}, false, fmt.Errorf("%s: unclosed BEGIN %q", relative, stack[len(stack)-1])
	}

	plan := filePlan{
		path:        path,
		relative:    relative,
		mode:        mode,
		delete:      deleteFile,
		fileFeature: fileFeature,
		blocks:      removedBlocks,
		markers:     strippedMarkers,
	}
	if deleteFile {
		plan.markers = 0
		return plan, true, nil
	}

	rewritten := output.Bytes()
	if bytes.Equal(rewritten, original) {
		return filePlan{}, false, nil
	}
	if strings.EqualFold(filepath.Ext(relative), ".go") {
		formatted, err := formatGo(rewritten, original)
		if err != nil {
			return filePlan{}, false, fmt.Errorf("%s: format changed Go source: %w", relative, err)
		}
		rewritten = formatted
	}
	plan.data = append([]byte(nil), rewritten...)
	return plan, true, nil
}

func allowedFileMarkerPreamble(line []byte, lineNumber int) bool {
	text := strings.TrimSpace(string(line))
	if text == "" {
		return true
	}
	if lineNumber == 1 && strings.HasPrefix(text, "#!") {
		return true
	}
	return strings.HasPrefix(text, "//go:build ") || strings.HasPrefix(text, "// +build ")
}

func activeCut(stack []string, cuts Set) bool {
	for _, feature := range stack {
		if contains(cuts, feature) {
			return true
		}
	}
	return false
}

func stackContains(stack []string, feature string) bool {
	for _, open := range stack {
		if open == feature {
			return true
		}
	}
	return false
}

func contains(set Set, feature string) bool {
	_, ok := set[feature]
	return ok
}

func formatGo(source, original []byte) ([]byte, error) {
	lineEnding := []byte{'\n'}
	if firstLF := bytes.IndexByte(original, '\n'); firstLF > 0 && original[firstLF-1] == '\r' {
		lineEnding = []byte{'\r', '\n'}
	}
	source = bytes.ReplaceAll(source, []byte{'\r', '\n'}, []byte{'\n'})
	formatted, err := format.Source(source)
	if err != nil {
		return nil, err
	}
	if len(lineEnding) == 2 {
		formatted = bytes.ReplaceAll(formatted, []byte{'\n'}, lineEnding)
	}
	return formatted, nil
}

func replaceFile(path string, data []byte, mode fs.FileMode) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".cut-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() {
		temporary.Close()
		if removeErr := os.Remove(temporaryName); err == nil && removeErr != nil && !os.IsNotExist(removeErr) {
			err = removeErr
		}
	}()

	if err = temporary.Chmod(mode.Perm()); err != nil {
		return err
	}
	if _, err = temporary.Write(data); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func resultFor(
	plans []filePlan,
	templateDirectories []directoryPlan,
	emptyDirectories []directoryPlan,
) Result {
	result := Result{
		RemovedTemplateDirectories: make([]string, 0, len(templateDirectories)),
		RemovedEmptyDirectories:    make([]string, 0, len(emptyDirectories)),
	}
	for _, plan := range plans {
		result.RemovedBlocks += plan.blocks
		result.StrippedMarkers += plan.markers
		result.RenamedModuleRefs += plan.moduleRefs
		if plan.delete {
			result.RemovedFiles = append(result.RemovedFiles, plan.relative)
		} else {
			result.ChangedFiles = append(result.ChangedFiles, plan.relative)
		}
	}
	for _, directory := range templateDirectories {
		result.RemovedTemplateDirectories = append(result.RemovedTemplateDirectories, directory.relative)
	}
	for _, directory := range emptyDirectories {
		result.RemovedEmptyDirectories = append(result.RemovedEmptyDirectories, directory.relative)
	}
	return result
}

func reportPlans(
	output io.Writer,
	plans []filePlan,
	templateDirectories []directoryPlan,
	emptyDirectories []directoryPlan,
	dryRun bool,
) error {
	if len(plans) == 0 && len(templateDirectories) == 0 && len(emptyDirectories) == 0 {
		_, err := fmt.Fprintln(output, "No changes planned.")
		return err
	}
	for _, plan := range plans {
		action := "updated"
		if dryRun {
			action = "would update"
		}
		details := make([]string, 0, 3)
		if plan.blocks != 0 {
			details = append(details, fmt.Sprintf("%d block(s)", plan.blocks))
		}
		if plan.markers != 0 {
			details = append(details, fmt.Sprintf("%d marker(s)", plan.markers))
		}
		if plan.moduleRefs != 0 {
			details = append(details, fmt.Sprintf("%d module reference(s)", plan.moduleRefs))
		}
		if plan.delete {
			action = "removed"
			if dryRun {
				action = "would remove"
			}
			details = []string{"whole file for " + plan.fileFeature}
		}
		if len(details) == 0 {
			details = append(details, "content")
		}
		detail := strings.Join(details, ", ")
		if _, err := fmt.Fprintf(output, "%s %s (%s)\n", action, plan.relative, detail); err != nil {
			return err
		}
	}
	for _, directory := range templateDirectories {
		action := "removed"
		if dryRun {
			action = "would remove"
		}
		if _, err := fmt.Fprintf(
			output,
			"%s %s/ (template directory and fixtures)\n",
			action,
			directory.relative,
		); err != nil {
			return err
		}
	}
	for _, directory := range emptyDirectories {
		action := "removed"
		if dryRun {
			action = "would remove"
		}
		if _, err := fmt.Fprintf(
			output,
			"%s %s/ (empty after planned removals)\n",
			action,
			directory.relative,
		); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(output); err != nil {
		return err
	}
	result := resultFor(plans, templateDirectories, emptyDirectories)
	verb := "Removed"
	if dryRun {
		verb = "Would remove"
	}
	markerVerb := "stripped"
	if dryRun {
		markerVerb = "would strip"
	}
	_, err := fmt.Fprintf(
		output,
		"%s %d whole file(s), %d template directory(s), %d empty directory(s), and %d block(s); %s %d marker(s); renamed %d module reference(s); %d file(s) rewritten.\n",
		verb,
		len(result.RemovedFiles),
		len(result.RemovedTemplateDirectories),
		len(result.RemovedEmptyDirectories),
		result.RemovedBlocks,
		markerVerb,
		result.StrippedMarkers,
		result.RenamedModuleRefs,
		len(result.ChangedFiles),
	)
	return err
}
