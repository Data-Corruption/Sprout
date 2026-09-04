//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const (
	pathBootstrapOpenMarker  = "# >>> PATH bootstrap: ~/.local/bin >>>"
	pathBootstrapCloseMarker = "# <<< PATH bootstrap <<<"
)

func TestInstallScriptCreatesLoginProfile(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatalf("create local bin: %v", err)
	}

	for _, name := range []string{".bashrc", ".zshrc", ".bash_profile"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("keep "+name+"\n"), 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	shell, path := pathBootstrapTools(t)
	runPathBootstrap(t, shell, path, home)

	profile := filepath.Join(home, ".profile")
	info, err := os.Lstat(profile)
	if err != nil {
		t.Fatalf("stat created profile: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("created profile mode = %v, want regular file", info.Mode())
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("created profile permissions = %04o, want 0600", got)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("created profile has no Unix stat metadata")
	}
	if got, want := int(stat.Uid), os.Getuid(); got != want {
		t.Errorf("created profile owner = %d, want %d", got, want)
	}

	for _, name := range []string{".bashrc", ".zshrc", ".profile", ".bash_profile"} {
		contents := readTestFile(t, filepath.Join(home, name))
		assertPathBootstrapCount(t, name, contents, 1)
		if name != ".profile" && !strings.HasPrefix(contents, "keep "+name+"\n") {
			t.Errorf("%s did not preserve its existing content", name)
		}
	}

	command := exec.Command(shell, "-c", `. "$HOME/.profile"; printf '%s\n' "$PATH"`)
	command.Env = []string{"HOME=" + home, "PATH=" + path}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("source created profile: %v: %s", err, output)
	}
	if !pathContains(strings.TrimSpace(string(output)), filepath.Join(home, ".local", "bin")) {
		t.Fatalf("PATH after sourcing created profile = %q; local bin is missing", output)
	}

	// A repeated install must not duplicate any bootstrap block.
	runPathBootstrap(t, shell, path, home)
	for _, name := range []string{".bashrc", ".zshrc", ".profile", ".bash_profile"} {
		contents := readTestFile(t, filepath.Join(home, name))
		assertPathBootstrapCount(t, name+" after second run", contents, 1)
	}
}

func TestInstallScriptPreservesExistingProfile(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".profile")
	const original = "export EXISTING_PROFILE_VALUE=kept\n"
	if err := os.WriteFile(profile, []byte(original), 0o640); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if err := os.Chmod(profile, 0o640); err != nil {
		t.Fatalf("set profile permissions: %v", err)
	}

	shell, path := pathBootstrapTools(t)
	runPathBootstrap(t, shell, path, home)
	runPathBootstrap(t, shell, path, home)

	contents := readTestFile(t, profile)
	if !strings.HasPrefix(contents, original) {
		t.Fatalf("profile content was not preserved: %q", contents)
	}
	assertPathBootstrapCount(t, ".profile", contents, 1)
	info, err := os.Stat(profile)
	if err != nil {
		t.Fatalf("stat profile: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("existing profile permissions = %04o, want 0640", got)
	}
}

func TestInstallScriptLeavesIncompleteProfileBootstrapUntouched(t *testing.T) {
	partial := "export EXISTING_PROFILE_VALUE=kept\n" + pathBootstrapOpenMarker + "\nif [ -d \"$HOME/.local/bin\" ]; then\n"
	complete := pathBootstrapOpenMarker + "\n# later complete block\n" + pathBootstrapCloseMarker + "\n"

	for _, test := range []struct {
		name     string
		contents string
	}{
		{name: "trailing_partial", contents: partial},
		{name: "partial_masked_by_later_pair", contents: partial + complete},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			profile := filepath.Join(home, ".profile")
			if err := os.WriteFile(profile, []byte(test.contents), 0o600); err != nil {
				t.Fatalf("create partial profile: %v", err)
			}

			shell, path := pathBootstrapTools(t)
			output := runPathBootstrap(t, shell, path, home)
			if !strings.Contains(output, "Found an incomplete PATH bootstrap") {
				t.Fatalf("incomplete-bootstrap warning missing from output: %q", output)
			}
			if got := readTestFile(t, profile); got != test.contents {
				t.Fatalf("incomplete user profile was rewritten:\nwant: %q\n got: %q", test.contents, got)
			}
		})
	}
}

func TestInstallScriptDoesNotClobberUnsafeProfile(t *testing.T) {
	shell, path := pathBootstrapTools(t)

	tests := []struct {
		name  string
		setup func(t *testing.T, home, profile string)
		check func(t *testing.T, home, profile string)
	}{
		{
			name: "directory",
			setup: func(t *testing.T, _, profile string) {
				t.Helper()
				if err := os.Mkdir(profile, 0o700); err != nil {
					t.Fatalf("create profile directory: %v", err)
				}
			},
			check: func(t *testing.T, _, profile string) {
				t.Helper()
				info, err := os.Lstat(profile)
				if err != nil || !info.IsDir() {
					t.Fatalf("profile directory was replaced: info=%v err=%v", info, err)
				}
			},
		},
		{
			name: "fifo",
			setup: func(t *testing.T, _, profile string) {
				t.Helper()
				if err := syscall.Mkfifo(profile, 0o600); err != nil {
					t.Fatalf("create profile fifo: %v", err)
				}
			},
			check: func(t *testing.T, _, profile string) {
				t.Helper()
				info, err := os.Lstat(profile)
				if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
					t.Fatalf("profile fifo was replaced: info=%v err=%v", info, err)
				}
			},
		},
		{
			name: "symlink",
			setup: func(t *testing.T, home, profile string) {
				t.Helper()
				target := filepath.Join(home, "managed-profile")
				if err := os.WriteFile(target, []byte("managed\n"), 0o600); err != nil {
					t.Fatalf("create symlink target: %v", err)
				}
				if err := os.Symlink(target, profile); err != nil {
					t.Fatalf("create profile symlink: %v", err)
				}
			},
			check: func(t *testing.T, home, profile string) {
				t.Helper()
				info, err := os.Lstat(profile)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("profile symlink was replaced: info=%v err=%v", info, err)
				}
				if got := readTestFile(t, filepath.Join(home, "managed-profile")); got != "managed\n" {
					t.Fatalf("profile symlink target was changed: %q", got)
				}
			},
		},
		{
			name: "dangling_symlink",
			setup: func(t *testing.T, home, profile string) {
				t.Helper()
				if err := os.Symlink(filepath.Join(home, "missing-target"), profile); err != nil {
					t.Fatalf("create dangling profile symlink: %v", err)
				}
			},
			check: func(t *testing.T, home, profile string) {
				t.Helper()
				info, err := os.Lstat(profile)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("dangling profile symlink was replaced: info=%v err=%v", info, err)
				}
				if _, err := os.Stat(filepath.Join(home, "missing-target")); !os.IsNotExist(err) {
					t.Fatalf("dangling symlink target was created: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			profile := filepath.Join(home, ".profile")
			test.setup(t, home, profile)

			output := runPathBootstrap(t, shell, path, home)
			if !strings.Contains(output, "add ~/.local/bin to PATH yourself") {
				t.Errorf("warning missing from output: %q", output)
			}
			test.check(t, home, profile)
		})
	}
}

func TestInstallScriptPathBootstrapIsBestEffort(t *testing.T) {
	shell, path := pathBootstrapTools(t)
	home := filepath.Join(t.TempDir(), "missing", "home")
	output := runPathBootstrap(t, shell, path, home)
	if !strings.Contains(output, "Cannot safely create") {
		t.Fatalf("creation failure warning missing from output: %q", output)
	}
}

func TestInstallScriptPersistsProfilesWhenCurrentPathIsConfigured(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatalf("create local bin: %v", err)
	}
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("unchanged\n"), 0o600); err != nil {
		t.Fatalf("create bashrc: %v", err)
	}

	shell, path := pathBootstrapTools(t)
	path = filepath.Join(home, ".local", "bin") + string(os.PathListSeparator) + path
	runPathBootstrap(t, shell, path, home)

	bashContents := readTestFile(t, bashrc)
	if !strings.HasPrefix(bashContents, "unchanged\n") {
		t.Fatalf("bashrc content was not preserved: %q", bashContents)
	}
	assertPathBootstrapCount(t, ".bashrc", bashContents, 1)
	profileContents := readTestFile(t, filepath.Join(home, ".profile"))
	assertPathBootstrapCount(t, ".profile", profileContents, 1)

	command := exec.Command(shell, "-c", `. "$HOME/.profile"; printf '%s\n' "$PATH"`)
	command.Env = []string{"HOME=" + home, "PATH=" + pathBootstrapToolsPath(t)}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("source persisted profile: %v: %s", err, output)
	}
	if !pathContains(strings.TrimSpace(string(output)), filepath.Join(home, ".local", "bin")) {
		t.Fatalf("PATH after sourcing persisted profile = %q; local bin is missing", output)
	}
}

func pathBootstrapTools(t *testing.T) (shell, path string) {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("POSIX shell unavailable: %v", err)
	}
	awk, err := exec.LookPath("awk")
	if err != nil {
		t.Skipf("awk unavailable: %v", err)
	}
	path = filepath.Dir(awk) + string(os.PathListSeparator) + "/usr/bin:/bin"
	return shell, path
}

func pathBootstrapToolsPath(t *testing.T) string {
	t.Helper()
	_, path := pathBootstrapTools(t)
	return path
}

func runPathBootstrap(t *testing.T, shell, path, home string) string {
	t.Helper()
	script := `set -eu
umask 077
warnf() { warning_format=$1; shift; printf "$warning_format\n" "$@" >&2; }
` + pathBootstrapSnippet(t)
	command := exec.Command(shell)
	command.Env = []string{"HOME=" + home, "PATH=" + path}
	command.Stdin = strings.NewReader(script)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run PATH bootstrap: %v: %s", err, output)
	}
	return string(output)
}

func pathBootstrapSnippet(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	const startMarker = "# Add to PATH "
	const endMarker = "# Success! "
	start := strings.Index(string(source), startMarker)
	if start < 0 {
		t.Fatalf("install.sh has no %q section", startMarker)
	}
	endOffset := strings.Index(string(source[start:]), endMarker)
	if endOffset < 0 {
		t.Fatalf("install.sh PATH section has no %q terminator", endMarker)
	}
	return string(source[start : start+endOffset])
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}

func pathContains(path, entry string) bool {
	for _, candidate := range filepath.SplitList(path) {
		if candidate == entry {
			return true
		}
	}
	return false
}

func assertPathBootstrapCount(t *testing.T, name, contents string, want int) {
	t.Helper()
	if got := strings.Count(contents, pathBootstrapOpenMarker); got != want {
		t.Errorf("%s opening marker count = %d, want %d", name, got, want)
	}
	if got := strings.Count(contents, pathBootstrapCloseMarker); got != want {
		t.Errorf("%s closing marker count = %d, want %d", name, got, want)
	}
}
