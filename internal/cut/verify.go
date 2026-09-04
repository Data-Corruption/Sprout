// --- FILE template ---

package cut

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Verify asserts the two post-conditions of a finalized tree:
//
//   - no ownership marker survives anywhere the cutter would parse one, and
//   - every path the plan removed is really gone.
//
// Run calls this after applying, so the cutter is the single authority on what
// a finalized tree looks like. It is exported so a tree finalized out of band
// can be held to the same contract.
func Verify(root string, result Result) error {
	absolute, err := validateRoot(root)
	if err != nil {
		return err
	}

	removedPaths := append([]string(nil), result.RemovedFiles...)
	removedPaths = append(removedPaths, result.RemovedTemplateDirectories...)
	removedPaths = append(removedPaths, result.RemovedEmptyDirectories...)
	for _, relative := range removedPaths {
		_, err := os.Lstat(filepath.Join(absolute, filepath.FromSlash(relative)))
		switch {
		case err == nil:
			return fmt.Errorf("finalized tree retained %s, which the plan removed", relative)
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("check removed path %s: %w", relative, err)
		}
	}

	return filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(absolute, path)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative != "." && skipDirectory(slashed, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !candidateFile(relative) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for index, line := range bytes.SplitAfter(data, []byte{'\n'}) {
			found, isMarker, err := parseMarker(line)
			if err != nil {
				return fmt.Errorf("%s:%d: finalized tree retained an unparseable marker: %w", slashed, index+1, err)
			}
			if isMarker {
				return fmt.Errorf(
					"%s:%d: finalized tree retained a %s marker for %q",
					slashed,
					index+1,
					found.kind,
					found.feature,
				)
			}
		}
		return nil
	})
}
