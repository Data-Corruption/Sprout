// --- FILE template ---

package cut

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerify(t *testing.T) {
	t.Parallel()

	t.Run("clean finalized tree", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "main.go"), []byte("package sample\n"), 0o644)
		result := Result{
			RemovedFiles:               []string{"removed.go"},
			RemovedTemplateDirectories: []string{"cmd/cut"},
			RemovedEmptyDirectories:    []string{"internal/old"},
		}
		if err := Verify(root, result); err != nil {
			t.Fatalf("Verify rejected clean tree: %v", err)
		}
	})

	t.Run("surviving marker", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(
			t,
			filepath.Join(root, "main.go"),
			[]byte("package sample\n\n"+markerLine("//", "BEGIN", "update")+"func Check() {}\n"+markerLine("//", "END", "update")),
			0o644,
		)
		if err := Verify(root, Result{}); err == nil || !strings.Contains(err.Error(), "retained a BEGIN marker") {
			t.Fatalf("Verify error = %v, want surviving marker error", err)
		}
	})

	t.Run("surviving removed path", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "cmd", "cut")
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Verify(root, Result{RemovedTemplateDirectories: []string{"cmd/cut"}}); err == nil ||
			!strings.Contains(err.Error(), "retained cmd/cut") {
			t.Fatalf("Verify error = %v, want retained path error", err)
		}
	})
}
